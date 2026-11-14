// SPDX-License-Identifier: MIT

// Package server is the protection equipment of IEC 60870-5-103: the
// secondary station of unbalanced transmission (clause 6.2.3). It answers
// the link procedures of the controlling system, hands control-direction
// ASDUs to a [Handler], transmits monitor-direction ASDUs as class 1 data
// (events, answers) or class 2 data (measurands) when polled, and runs the
// system functions itself:
//
//   - identification after reset CU and reset FCB, and once after start
//     (clause 7.4.1, table 8);
//   - time synchronization (clause 7.4.2): the server keeps the clock offset,
//     answers with ASDU 6 and flags its time tags invalid until the first
//     synchronization and when none came for [Config.SyncTimeout]. A
//     synchronization whose time is flagged invalid, or lies outside the
//     years 1970 to 2069 that CP56Time2a carries, is not applied: the clock
//     stays as it was, and an addressed one is answered with ASDU 6
//     carrying the station's time with IV set;
//   - the disturbance data transfer of clause 7.4.7 from a
//     [DisturbanceSource], when one is configured; the selection of a
//     record whose number of channels (NOC) or of values per channel
//     (NOE) does not fit its field is answered with TOO 34.
//
// The application answers general interrogations, general commands and
// generic services through the helpers [Server.AnswerGI],
// [Server.AckCommand], [Server.GenericAnswer] and
// [Server.GenericIdentAnswer].
package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	iec60870 "github.com/otfabric/go-iec60870-5"
	"github.com/otfabric/go-iec60870-5/cp"
	"github.com/otfabric/go-iec60870-5/ft12"
	"github.com/otfabric/go-iec60870-5/iec103"
	"github.com/otfabric/go-iec60870-5/internal/logx"
	"github.com/otfabric/go-iec60870-5/link"
)

// Handler handles the control-direction ASDUs of the controlling system
// that the server does not handle itself: general interrogation (ASDU 7),
// general commands (ASDU 20), generic services (ASDUs 10 and 21), the
// disturbance orders (ASDUs 24 and 25) when no [DisturbanceSource] is
// configured, and any private ASDU. Calls are made one at a time from a
// goroutine of the server; the handler answers through the Server, whose
// methods it may call, all but Close. Close waits for the goroutine that
// calls the handler: a handler that stops the station calls Close in a new
// goroutine (go s.Close()), which returns once the handler has returned.
//
// The ASDU is the handler's own: a new one is decoded for each call and the
// server keeps no reference to it, so the handler may keep or modify it.
type Handler interface {
	HandleASDU(s *Server, a *iec103.ASDU)
}

// HandlerFunc adapts a function to [Handler].
type HandlerFunc func(s *Server, a *iec103.ASDU)

// HandleASDU calls f.
func (f HandlerFunc) HandleASDU(s *Server, a *iec103.ASDU) { f(s, a) }

// Defaults of [Config].
const (
	DefaultQueueSize   = 1024
	DefaultSyncTimeout = 23 * time.Hour // clause 7.4.2
	// DefaultBitRate is the lower of the two standard rates of clause
	// 6.2.4 (9.6 and 19.2 kbit/s).
	DefaultBitRate = 9600
)

// syncFrameBits is the length in bits of the FT 1.2 frame of an ASDU 6
// with a one-octet link address: 68H L L 68H, C, A, 13 octets of ASDU, CS,
// 16H, 21 characters of 11 bits (start, 8 data, even parity, stop).
const syncFrameBits = 21 * 11

// Config configures a server.
type Config struct {
	// Link holds the link options. The address field is always one octet
	// (clause 6.2.3); Link.FT12 is set accordingly.
	Link link.Config
	// LinkAddr is this station's link address, 0..254.
	LinkAddr uint8
	// CommonAddr is the common address of ASDU. Zero means LinkAddr
	// (clause 7.2.4).
	CommonAddr uint8
	// Fun is the main function type of the equipment, which the
	// identification messages and the list of recorded disturbances carry
	// (table 8, note 2; clause 7.3.1.11). It is required: zero is refused
	// with [iec103.ErrInvalidConfig].
	Fun iec103.FunctionType
	// Identification is the body of the identification (ASDU 5).
	Identification iec103.Identification
	// StartCause is the cause of the identification sent once after start:
	// [iec103.CausePowerOn] (the default) or [iec103.CauseStartRestart]
	// for equipment that cannot tell power on from restart (clause 7.4.1).
	StartCause iec103.Cause

	// Handler handles control-direction ASDUs. Nil answers general
	// interrogations with an empty cycle and general commands negatively.
	Handler Handler
	// Disturbances, when set, makes the server run the disturbance data
	// transfer of clause 7.4.7 itself.
	Disturbances DisturbanceSource

	// QueueSize bounds each of the class 1 and class 2 queues. Zero means
	// [DefaultQueueSize]. Sending to a full queue returns [ErrQueueFull].
	QueueSize int
	// Location is the time zone of the time tags; nil means UTC.
	Location *time.Location
	// SyncTimeout is how long time tags stay valid after the last time
	// synchronization. Zero means [DefaultSyncTimeout].
	SyncTimeout time.Duration
	// SyncDelay is added to a received synchronization time: the
	// transmission time of the ASDU 6, its length in bits times the bit
	// time (clause 7.4.2). Zero means the length of the FT 1.2 frame (21
	// characters of 11 bits) at BitRate; negative means no correction, for
	// a transport that is not a serial line.
	SyncDelay time.Duration
	// BitRate is the line speed in bit/s from which the default SyncDelay
	// is computed. Zero means [DefaultBitRate].
	BitRate int
	// AnswerBroadcastSync makes the server answer a broadcast time
	// synchronization (send/no reply to address 255) with the
	// monitor-direction ASDU 6 too. Off by default: figure 5 of clause
	// 7.4.2 has broadcast messages need no answer; an addressed
	// synchronization is always answered.
	AnswerBroadcastSync bool
	// OnTimeSync is called with each synchronization time the server
	// applies, from the goroutine that calls the Handler.
	OnTimeSync func(t time.Time, broadcast bool)

	// OnState is called with each change of the link state: from the
	// station's link goroutine on a reset, and from Close for
	// StateClosed. It must return quickly and must not call Close, which
	// waits for that goroutine; call Close in a new goroutine instead.
	OnState func(s iec103.State, err error)
	// Logger receives the log entries; nil is silent.
	Logger iec60870.Logger
}

// ErrQueueFull reports that an ASDU could not be queued for transmission.
var ErrQueueFull = errors.New("iec103: send queue full")

// Class selects the data class of a monitor-direction ASDU.
type Class = link.Class

// Data classes: class 1 for events and answers, class 2 for cyclic
// measurands.
const (
	Class1 = link.Class1
	Class2 = link.Class2
)

// Server is the protection equipment.
type Server struct {
	cfg Config
	ca  uint8
	log *logx.Logger
	sec *link.Secondary

	mu         sync.Mutex
	class      [2][]queued
	state      iec103.State
	started    bool // the start identification was sent
	cmdPending bool // a general command waits for its acknowledgement
	offset     time.Duration
	synced     time.Time
	dist       *distState

	in     chan inbound
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	closeOnce sync.Once
	closeErr  error
}

// queued is a monitor-direction ASDU waiting to be polled; cycle marks the
// answers of a general interrogation (of generic data), which a new one
// replaces (clauses 7.4.3 and 7.4.8.7).
type queued struct {
	raw   []byte
	cycle cycle
}

type cycle uint8

const (
	noCycle  cycle = iota
	giCycle        // answers to ASDU 7, the termination, the list after it
	ggiCycle       // answers to the general interrogation of generic data
)

type inbound struct {
	a         *iec103.ASDU
	broadcast bool
	at        time.Time
}

// New starts a protection equipment station on rw. Close stops it and
// closes rw when it is an io.Closer.
func New(rw io.ReadWriter, cfg Config) (*Server, error) {
	if cfg.LinkAddr == 255 {
		return nil, fmt.Errorf("%w: link address 255 is the broadcast address", iec103.ErrInvalidConfig)
	}
	if cfg.Fun == 0 {
		return nil, fmt.Errorf("%w: no function type (Config.Fun)", iec103.ErrInvalidConfig)
	}
	if cfg.BitRate < 0 {
		return nil, fmt.Errorf("%w: bit rate %d", iec103.ErrInvalidConfig, cfg.BitRate)
	}
	if cfg.BitRate == 0 {
		cfg.BitRate = DefaultBitRate
	}
	switch {
	case cfg.SyncDelay == 0:
		cfg.SyncDelay = time.Duration(syncFrameBits) * time.Second / time.Duration(cfg.BitRate)
	case cfg.SyncDelay < 0:
		cfg.SyncDelay = 0
	}
	cfg.Link.FT12 = ft12.Params{AddrSize: 1}
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = DefaultQueueSize
	}
	if cfg.SyncTimeout <= 0 {
		cfg.SyncTimeout = DefaultSyncTimeout
	}
	if cfg.StartCause == 0 {
		cfg.StartCause = iec103.CausePowerOn
	}
	if cfg.StartCause != iec103.CausePowerOn && cfg.StartCause != iec103.CauseStartRestart {
		return nil, fmt.Errorf("%w: start cause %s", iec103.ErrInvalidConfig, cfg.StartCause)
	}
	s := &Server{
		cfg: cfg,
		ca:  cfg.CommonAddr,
		log: logx.New(cfg.Logger, "iec103.server", "link", cfg.LinkAddr),
		in:  make(chan inbound, 64),
	}
	if s.ca == 0 {
		s.ca = cfg.LinkAddr
	}
	s.ctx, s.cancel = context.WithCancel(context.Background())
	var err error
	s.sec, err = link.NewSecondary(rw, cfg.Link, uint16(cfg.LinkAddr), secondaryUser{s})
	if err != nil {
		return nil, fmt.Errorf("%w: %w", iec103.ErrInvalidConfig, err)
	}
	s.wg.Add(1)
	go s.handle()
	return s, nil
}

// Close stops the station. Close is safe to call more than once and from
// several goroutines: the first call closes the transport and returns its
// error; later calls return nil. It must not be called from the Handler,
// OnState or OnTimeSync (see [Handler]).
func (s *Server) Close() error {
	first := false
	s.closeOnce.Do(func() {
		first = true
		s.cancel()
		s.closeErr = s.sec.Close()
		s.wg.Wait()
		s.setState(iec103.StateClosed, nil)
	})
	if first {
		return s.closeErr
	}
	return nil
}

// State returns the link state: up once the controlling system reset the
// link.
func (s *Server) State() iec103.State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

// CommonAddr returns the common address of ASDU of the station.
func (s *Server) CommonAddr() uint8 { return s.ca }

func (s *Server) setState(st iec103.State, err error) {
	s.mu.Lock()
	if s.state == st || s.state == iec103.StateClosed {
		s.mu.Unlock()
		return
	}
	s.state = st
	s.mu.Unlock()
	s.log.Info("link state", "state", st.String(), "error", err)
	if s.cfg.OnState != nil {
		s.cfg.OnState(st, err)
	}
}

// Now returns the station's time as a time tag: the local clock corrected
// by the last synchronization, with IV set before the first one and when
// the last is older than [Config.SyncTimeout] (clause 7.4.2).
func (s *Server) Now() cp.Timestamp {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	return cp.Timestamp{Time: now.Add(s.offset), Invalid: s.synced.IsZero() || now.Sub(s.synced) > s.cfg.SyncTimeout}
}

// Send queues a monitor-direction ASDU as class 1 or class 2 data.
func (s *Server) Send(a *iec103.ASDU, c Class) error { return s.send(a, c, noCycle) }

func (s *Server) send(a *iec103.ASDU, c Class, cy cycle) error {
	raw, err := a.AppendIn(nil, s.cfg.Location)
	if err != nil {
		return err
	}
	return s.queue(queued{raw: raw, cycle: cy}, c, false)
}

// purge drops the queued answers of an interrupted interrogation cycle.
func (s *Server) purge(cy cycle) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var q []queued
	for _, x := range s.class[0] {
		if x.cycle != cy {
			q = append(q, x)
		}
	}
	if n := len(s.class[0]) - len(q); n > 0 {
		s.log.Debug("interrogation cycle aborted", "dropped", n)
	}
	s.class[0] = q
}

func (s *Server) queue(raw queued, c Class, front bool) error {
	if s.ctx.Err() != nil {
		return iec103.ErrClosed
	}
	i := 0
	if c == Class2 {
		i = 1
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.class[i]) >= s.cfg.QueueSize {
		return ErrQueueFull
	}
	if front {
		s.class[i] = append([]queued{raw}, s.class[i]...)
	} else {
		s.class[i] = append(s.class[i], raw)
	}
	return nil
}

// Pending returns the number of queued ASDUs of class c.
func (s *Server) Pending(c Class) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c == Class2 {
		return len(s.class[1])
	}
	return len(s.class[0])
}

// Event queues a time-tagged message (ASDU 1) with the station's time as
// class 1 data.
func (s *Server) Event(cause iec103.Cause, fun iec103.FunctionType, inf iec103.InfoNumber, dpi iec103.DPI) error {
	return s.Send(iec103.New(cause, s.ca, fun, inf, iec103.TimeTagged{DPI: dpi, Time: s.Now()}), Class1)
}

// AnswerGI answers the general interrogation req (ASDU 7, clause 7.4.3):
// it queues data with cause 9, the scan number as SIN of time-tagged
// messages, then the termination (ASDU 8) with the scan number. With a
// [DisturbanceSource] the list of recorded disturbances follows (ASDU 23,
// cause 31, with the function type of the equipment, clauses 7.3.1.11 and
// 7.4.7). A new ASDU 7 that arrives before the cycle was polled drops what
// is left of it, termination included (clause 7.4.3). A req whose body is
// not a general interrogation ([iec103.GIInit]), such as an ASDU 7 with a
// private cause that decodes as [iec103.Raw], returns an error wrapping
// [iec103.ErrInvalid] and queues nothing.
func (s *Server) AnswerGI(req *iec103.ASDU, data ...*iec103.ASDU) error {
	var gi iec103.GIInit
	if req != nil {
		var ok bool
		if gi, ok = req.Body.(iec103.GIInit); !ok {
			req = nil
		}
	}
	if req == nil {
		return fmt.Errorf("%w: AnswerGI needs a general interrogation (ASDU 7)", iec103.ErrInvalid)
	}
	scn := gi.SCN
	for _, a := range data {
		c := *a
		c.Cause = iec103.CauseGI
		switch b := c.Body.(type) {
		case iec103.TimeTagged:
			b.SIN = scn
			c.Body = b
		case iec103.RelTimeTagged:
			b.SIN = scn
			c.Body = b
		}
		if err := s.send(&c, Class1, giCycle); err != nil {
			return err
		}
	}
	if err := s.send(iec103.New(iec103.CauseGITermination, s.ca, iec103.FunGlobal, iec103.InfGIEnd, iec103.GITermination{SCN: scn}), Class1, giCycle); err != nil {
		return err
	}
	if s.cfg.Disturbances != nil {
		return s.sendList(giCycle)
	}
	return nil
}

// AckCommand acknowledges the general command req (clause 7.4.4): ASDU 1
// with the FUN and INF of the command, cause 20 (positive) or 21
// (negative), the DCO as DPI and the RII as SIN. A positive command is
// followed by the change of state with cause 12, which the application
// sends ([Server.Event]). A req whose body is not a general command
// ([iec103.GeneralCommand]), such as an ASDU 20 with a private cause that
// decodes as [iec103.Raw], returns an error wrapping [iec103.ErrInvalid]
// and acknowledges nothing.
func (s *Server) AckCommand(req *iec103.ASDU, positive bool) error {
	cmd, err := generalCommand(req)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.cmdPending = false
	s.mu.Unlock()
	return s.ackCommand(req, cmd, positive)
}

func generalCommand(req *iec103.ASDU) (iec103.GeneralCommand, error) {
	if req != nil {
		if cmd, ok := req.Body.(iec103.GeneralCommand); ok {
			return cmd, nil
		}
	}
	return iec103.GeneralCommand{}, fmt.Errorf("%w: AckCommand needs a general command (ASDU 20)", iec103.ErrInvalid)
}

func (s *Server) ack(req *iec103.ASDU, positive bool) error {
	cmd, err := generalCommand(req)
	if err != nil {
		return err
	}
	return s.ackCommand(req, cmd, positive)
}

func (s *Server) ackCommand(req *iec103.ASDU, cmd iec103.GeneralCommand, positive bool) error {
	cause := iec103.CauseCommandAck
	if !positive {
		cause = iec103.CauseCommandNack
	}
	return s.Send(iec103.New(cause, s.ca, req.Fun, req.Inf, iec103.TimeTagged{DPI: cmd.DCO, Time: s.Now(), SIN: cmd.RII}), Class1)
}

func riiOf(req *iec103.ASDU) uint8 {
	switch b := req.Body.(type) {
	case iec103.GenericCommand:
		return b.RII
	case iec103.GenericData:
		return b.RII
	}
	return 0
}

// GenericAnswer answers the generic command req (ASDU 21 or 10) with ASDU
// 10: the RII and INF of req, the given cause (42 or 43 for reads, 40, 41
// or 44 for writes, 9 for the general interrogation of generic data) and
// the data sets, split over several ASDUs when they need it.
func (s *Server) GenericAnswer(req *iec103.ASDU, cause iec103.Cause, sets ...iec103.DataSet) error {
	return s.generic(req.Inf, riiOf(req), cause, sets, noCycle)
}

func (s *Server) generic(inf iec103.InfoNumber, rii uint8, cause iec103.Cause, sets []iec103.DataSet, cy cycle) error {
	bodies, err := iec103.PackGenericData(rii, sets)
	if err != nil {
		return err
	}
	for _, b := range bodies {
		if err := s.send(iec103.New(cause, s.ca, iec103.FunGeneric, inf, b), Class1, cy); err != nil {
			return err
		}
	}
	return nil
}

// AnswerGenericGI answers the general interrogation of generic data req
// (ASDU 21, INF 245, clause 7.4.8.7): ASDU 10 with INF 244 and cause 9
// carrying the data sets, then the end (INF 245, cause 10, NGD 0). A new
// general interrogation of generic data that arrives before the cycle was
// polled drops what is left of it, end included (clause 7.4.8.7).
func (s *Server) AnswerGenericGI(req *iec103.ASDU, sets ...iec103.DataSet) error {
	rii := riiOf(req)
	if len(sets) > 0 {
		if err := s.generic(iec103.InfReadEntry, rii, iec103.CauseGI, sets, ggiCycle); err != nil {
			return err
		}
	}
	return s.generic(iec103.InfGenericGI, rii, iec103.CauseGITermination, nil, ggiCycle)
}

// GenericIdentAnswer answers a read of the directory of a single entry
// (INF 243) with ASDU 11.
func (s *Server) GenericIdentAnswer(req *iec103.ASDU, cause iec103.Cause, gin iec103.GIN, elems ...iec103.DescElement) error {
	bodies, err := iec103.PackGenericIdent(riiOf(req), gin, elems)
	if err != nil {
		return err
	}
	for _, b := range bodies {
		if err := s.Send(iec103.New(cause, s.ca, iec103.FunGeneric, req.Inf, b), Class1); err != nil {
			return err
		}
	}
	return nil
}

// receive takes user data from the link. While DFC is set (the queue to
// the handler is more than half full) user data is refused with NACK
// (clause 6.3: a command sent anyway is not processed and is answered
// with function 1); a broadcast is the exception and is taken while there
// is room.
func (s *Server) receive(data []byte, broadcast bool) bool {
	if !broadcast && s.busy() {
		return false
	}
	a, err := iec103.DecodeIn(data, s.cfg.Location)
	if err != nil {
		s.log.Warn("undecodable ASDU dropped", "error", err)
		return true // the link layer accepted the frame
	}
	select {
	case s.in <- inbound{a: a, broadcast: broadcast, at: time.Now()}:
		return true
	default:
		return false // busy: NACK with DFC (clause 6.3)
	}
}

func (s *Server) handle() {
	defer s.wg.Done()
	for {
		select {
		case m := <-s.in:
			s.dispatch(m)
		case <-s.ctx.Done():
			return
		}
	}
}

func (s *Server) dispatch(m inbound) {
	a := m.a
	if a.CommonAddr != s.ca && a.CommonAddr != iec103.CommonAddrGlobal {
		s.log.Debug("ASDU for another common address dropped", "asdu", a.String())
		return
	}
	switch b := a.Body.(type) {
	case iec103.TimeSync:
		s.timeSync(b.Time, m)
		return
	case iec103.GeneralCommand:
		if !iec103.CommandAllowed(a.Fun, a.Inf, b.DCO) {
			// Clause 7.2.6.4 and table 17: a DCO that is not used, or OFF
			// where only ON is defined, is acknowledged negatively.
			_ = s.ack(a, false)
			return
		}
		s.mu.Lock()
		pending := s.cmdPending
		s.cmdPending = true
		s.mu.Unlock()
		if pending {
			// Clause 7.4.4: a command before the previous one was
			// acknowledged is rejected.
			_ = s.ack(a, false)
			return
		}
		if s.cfg.Handler == nil {
			_ = s.AckCommand(a, false)
			return
		}
	case iec103.GIInit:
		s.purge(giCycle)
		if s.cfg.Handler == nil {
			_ = s.AnswerGI(a)
			return
		}
	case iec103.GenericCommand:
		if a.Inf == iec103.InfGenericGI && a.Cause == iec103.CauseGI {
			s.purge(ggiCycle)
		}
	case iec103.DisturbanceOrder:
		if s.cfg.Disturbances != nil && a.Cause == iec103.CauseDisturbanceData {
			s.disturbance(a, b)
			return
		}
	}
	if s.cfg.Handler != nil {
		s.cfg.Handler.HandleASDU(s, a)
	}
}

// The years a CP56Time2a carries: two digits, 70..99 for 1970..1999 and
// 0..69 for 2000..2069 (IEC 60870-5-4, as package cp reads it; clause 7.2.6
// of IEC 60870-5-103 uses CP56Time2a for the time synchronization).
const (
	cp56FirstYear = 1970
	cp56LastYear  = 2069
)

// maxOffset bounds the clock offset a synchronization sets: the 100 years
// (36525 days) of the CP56Time2a range, so that the corrected clock stays
// within reach of it whatever the local clock says.
const maxOffset = 36525 * 24 * time.Hour

// syncable reports whether a received synchronization time can be applied:
// not flagged invalid (which a CP56Time2a with a field out of range also
// decodes to, with the zero time), and within the CP56Time2a range.
func syncable(t cp.Timestamp) bool {
	y := t.Year()
	return !t.Invalid && !t.IsZero() && y >= cp56FirstYear && y <= cp56LastYear
}

// timeSync applies a time synchronization (clause 7.4.2) and answers an
// addressed one with ASDU 6 in the monitor direction carrying the
// corrected time; a broadcast one only with [Config.AnswerBroadcastSync]
// (figure 5: broadcast messages need no answer). A time that [syncable]
// refuses is not applied; the answer then carries the station's time with
// IV set. Clause 7.4.2 defines no negative answer to a time
// synchronization: the IV flag is how the ASDU 6 says the time is not
// valid (an interpretation, docs/iec103/INTEROPERABILITY.md).
func (s *Server) timeSync(ts cp.Timestamp, m inbound) {
	if !syncable(ts) {
		s.log.Warn("time synchronization not applied: time invalid or out of range",
			"time", ts.Time, "invalid", ts.Invalid, "broadcast", m.broadcast)
		if m.broadcast && !s.cfg.AnswerBroadcastSync {
			return
		}
		now := s.Now()
		now.Invalid = true
		_ = s.Send(iec103.New(iec103.CauseTimeSync, s.ca, iec103.FunGlobal, iec103.InfTimeSync, iec103.TimeSync{Time: now}), Class1)
		return
	}
	t := ts.Add(s.cfg.SyncDelay)
	offset := min(max(t.Sub(m.at), -maxOffset), maxOffset)
	s.mu.Lock()
	s.offset = offset
	s.synced = m.at
	s.mu.Unlock()
	if s.cfg.OnTimeSync != nil {
		s.cfg.OnTimeSync(t, m.broadcast)
	}
	if m.broadcast && !s.cfg.AnswerBroadcastSync {
		return
	}
	_ = s.Send(iec103.New(iec103.CauseTimeSync, s.ca, iec103.FunGlobal, iec103.InfTimeSync, iec103.TimeSync{Time: s.Now()}), Class1)
}

// identify queues an identification (ASDU 5) with the information number
// of table 8.
func (s *Server) identify(cause iec103.Cause, front bool) {
	inf := map[iec103.Cause]iec103.InfoNumber{
		iec103.CauseResetFCB: iec103.InfResetFCB, iec103.CauseResetCU: iec103.InfResetCU,
		iec103.CauseStartRestart: iec103.InfStartRestart, iec103.CausePowerOn: iec103.InfPowerOn,
	}[cause]
	raw, err := iec103.New(cause, s.ca, s.cfg.Fun, inf, s.cfg.Identification).AppendIn(nil, s.cfg.Location)
	if err == nil {
		err = s.queue(queued{raw: raw}, Class1, front)
	}
	if err != nil {
		s.log.Warn("identification not queued", "error", err)
	}
}

// reset runs the reset of the communication unit (clear true) or of the
// FCB (clause 7.4.1): the acknowledging identification goes first, ahead
// of what is queued; after the first reset since start the start
// identification follows. Both abort a disturbance data transfer.
func (s *Server) reset(clear bool) {
	s.mu.Lock()
	if clear {
		s.class = [2][]queued{}
	}
	first := !s.started
	s.started = true
	s.dist = nil
	s.cmdPending = false
	s.mu.Unlock()
	s.setState(iec103.StateUp, nil)
	if first {
		s.identify(s.cfg.StartCause, true)
	}
	if clear {
		s.identify(iec103.CauseResetCU, true)
	} else {
		s.identify(iec103.CauseResetFCB, true)
	}
}

// secondaryUser is the link user. It implements the optional hooks of
// package link for clause 6.3: reset FCB, DFC, and function code 15 for
// the functions clause 6.2.3 does not list.
type secondaryUser struct{ s *Server }

func (u secondaryUser) UserData(data []byte, broadcast bool) bool {
	return u.s.receive(data, broadcast)
}

func (u secondaryUser) pop(i int) []byte {
	u.s.mu.Lock()
	defer u.s.mu.Unlock()
	q := u.s.class[i]
	if len(q) == 0 {
		return nil
	}
	d := q[0]
	u.s.class[i] = q[1:]
	return d.raw
}

func (u secondaryUser) Class1() []byte { return u.pop(0) }
func (u secondaryUser) Class2() []byte { return u.pop(1) }

func (u secondaryUser) HasClass1() bool {
	u.s.mu.Lock()
	defer u.s.mu.Unlock()
	return len(u.s.class[0]) > 0
}

// Reset is reset of remote link, function 0: reset CU in IEC 60870-5-103.
// Function 1 never reaches it (Implemented).
func (u secondaryUser) Reset(bool) { u.s.reset(true) }

// ResetFCB is function 7 (clause 6.3).
func (u secondaryUser) ResetFCB() { u.s.reset(false) }

// Busy sets DFC while the handler queue is more than half full (clause
// 6.3).
func (u secondaryUser) Busy() bool { return u.s.busy() }

func (s *Server) busy() bool { return len(s.in) > cap(s.in)/2 }

// Implemented accepts the primary function codes of clause 6.2.3 and 6.3:
// 0, 3, 4, 7, 9, 10 and 11.
func (secondaryUser) Implemented(fn byte) bool {
	switch fn {
	case link.FuncResetLink, link.FuncUserDataConf, link.FuncUserDataNoRepl, link.FuncResetFCB,
		link.FuncRequestStatus, link.FuncRequestClass1, link.FuncRequestClass2:
		return true
	}
	return false
}
