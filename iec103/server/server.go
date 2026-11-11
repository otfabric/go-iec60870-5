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
//     synchronization and when none came for [Config.SyncTimeout];
//   - the disturbance data transfer of clause 7.4.7 from a
//     [DisturbanceSource], when one is configured.
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
// goroutine of the server; the handler answers through the Server.
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
)

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
	// identification messages carry (table 8, note 2).
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
	// transmission time of the ASDU 6, length times bit time (clause
	// 7.4.2), which the line speed determines.
	SyncDelay time.Duration
	// OnTimeSync is called with each received synchronization time.
	OnTimeSync func(t time.Time, broadcast bool)

	// OnState is called with each change of the link state.
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
	class      [2][][]byte
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
}

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
		log: logx.New(cfg.Logger, "iec103-server", "link", cfg.LinkAddr),
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

// Close stops the station.
func (s *Server) Close() error {
	s.cancel()
	err := s.sec.Close()
	s.wg.Wait()
	s.setState(iec103.StateClosed, nil)
	return err
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
	s.log.Info("link state", "state", st.String(), "err", err)
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
func (s *Server) Send(a *iec103.ASDU, c Class) error {
	raw, err := a.AppendIn(nil, s.cfg.Location)
	if err != nil {
		return err
	}
	return s.queue(raw, c, false)
}

func (s *Server) queue(raw []byte, c Class, front bool) error {
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
		s.class[i] = append([][]byte{raw}, s.class[i]...)
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
// cause 31, clause 7.4.7).
func (s *Server) AnswerGI(req *iec103.ASDU, data ...*iec103.ASDU) error {
	scn := req.Body.(iec103.GIInit).SCN
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
		if err := s.Send(&c, Class1); err != nil {
			return err
		}
	}
	if err := s.Send(iec103.New(iec103.CauseGITermination, s.ca, iec103.FunGlobal, iec103.InfGIEnd, iec103.GITermination{SCN: scn}), Class1); err != nil {
		return err
	}
	if s.cfg.Disturbances != nil {
		return s.sendList(req.Fun)
	}
	return nil
}

// AckCommand acknowledges the general command req (clause 7.4.4): ASDU 1
// with the FUN and INF of the command, cause 20 (positive) or 21
// (negative), the DCO as DPI and the RII as SIN. A positive command is
// followed by the change of state with cause 12, which the application
// sends ([Server.Event]).
func (s *Server) AckCommand(req *iec103.ASDU, positive bool) error {
	cmd := req.Body.(iec103.GeneralCommand)
	s.mu.Lock()
	s.cmdPending = false
	s.mu.Unlock()
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
	return s.generic(req.Inf, riiOf(req), cause, sets)
}

func (s *Server) generic(inf iec103.InfoNumber, rii uint8, cause iec103.Cause, sets []iec103.DataSet) error {
	bodies, err := iec103.PackGenericData(rii, sets)
	if err != nil {
		return err
	}
	for _, b := range bodies {
		if err := s.Send(iec103.New(cause, s.ca, iec103.FunGeneric, inf, b), Class1); err != nil {
			return err
		}
	}
	return nil
}

// AnswerGenericGI answers the general interrogation of generic data req
// (ASDU 21, INF 245, clause 7.4.8.7): ASDU 10 with INF 244 and cause 9
// carrying the data sets, then the end (INF 245, cause 10, NGD 0).
func (s *Server) AnswerGenericGI(req *iec103.ASDU, sets ...iec103.DataSet) error {
	rii := riiOf(req)
	if len(sets) > 0 {
		if err := s.generic(iec103.InfReadEntry, rii, iec103.CauseGI, sets); err != nil {
			return err
		}
	}
	return s.generic(iec103.InfGenericGI, rii, iec103.CauseGITermination, nil)
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

func (s *Server) receive(data []byte, broadcast bool) bool {
	a, err := iec103.DecodeIn(data, s.cfg.Location)
	if err != nil {
		s.log.Warn("undecodable ASDU dropped", "err", err)
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
		s.timeSync(b.Time.Time, m)
		return
	case iec103.GeneralCommand:
		s.mu.Lock()
		pending := s.cmdPending
		s.cmdPending = true
		s.mu.Unlock()
		if pending {
			// Clause 7.4.4: a command before the previous one was
			// acknowledged is rejected.
			_ = s.AckCommand(a, false)
			s.mu.Lock()
			s.cmdPending = true
			s.mu.Unlock()
			return
		}
		if s.cfg.Handler == nil {
			_ = s.AckCommand(a, false)
			return
		}
	case iec103.GIInit:
		if s.cfg.Handler == nil {
			_ = s.AnswerGI(a)
			return
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

// timeSync applies a time synchronization (clause 7.4.2) and answers with
// ASDU 6 in the monitor direction carrying the corrected time. We answer a
// broadcast synchronization as well: figure 5 shows the monitor-direction
// ASDU 6 after both forms.
func (s *Server) timeSync(t time.Time, m inbound) {
	t = t.Add(s.cfg.SyncDelay)
	s.mu.Lock()
	s.offset = t.Sub(m.at)
	s.synced = m.at
	s.mu.Unlock()
	if s.cfg.OnTimeSync != nil {
		s.cfg.OnTimeSync(t, m.broadcast)
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
		err = s.queue(raw, Class1, front)
	}
	if err != nil {
		s.log.Warn("identification not queued", "err", err)
	}
}

// reset runs the reset of the communication unit (clear true) or of the
// FCB (clause 7.4.1): the acknowledging identification goes first, ahead
// of what is queued; after the first reset since start the start
// identification follows. Both abort a disturbance data transfer.
func (s *Server) reset(clear bool) {
	s.mu.Lock()
	if clear {
		s.class = [2][][]byte{}
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
	return d
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
func (u secondaryUser) Busy() bool { return len(u.s.in) > cap(u.s.in)/2 }

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
