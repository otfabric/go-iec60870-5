// SPDX-License-Identifier: MIT

// Package server is the controlled station of IEC 60870-5-101: it answers
// the link procedures of the controlling station, hands control-direction
// ASDUs to a [Handler] and transmits monitor-direction ASDUs: as class 1
// or class 2 data when polled (unbalanced transmission), or on its own
// initiative (balanced transmission).
//
// After the controlling station resets the link for the first time (and
// after each reset of user process) the server sends end of initialization
// (M_EI_NA_1, the station initialization procedure of IEC 60870-5-5)
// unless Config.NoEndOfInit is set.
package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	iec60870 "github.com/otfabric/go-iec60870-5"
	"github.com/otfabric/go-iec60870-5/asdu"
	"github.com/otfabric/go-iec60870-5/iec101"
	"github.com/otfabric/go-iec60870-5/internal/logx"
	"github.com/otfabric/go-iec60870-5/link"
	"github.com/otfabric/go-iec60870-5/telecontrol"
)

// Handler handles the control-direction ASDUs of the controlling station.
// Calls are made one at a time from a goroutine of the server; a handler
// answers through the Server (Confirm, Terminate, Send, ...), which only
// queues. It must not call Close: Close waits for the handler's goroutine.
// Call it in a new goroutine (go s.Close()); it returns once the handler
// has returned.
type Handler interface {
	HandleASDU(s *Server, a *asdu.ASDU)
}

// HandlerFunc adapts a function to [Handler].
type HandlerFunc func(s *Server, a *asdu.ASDU)

// HandleASDU calls f.
func (f HandlerFunc) HandleASDU(s *Server, a *asdu.ASDU) { f(s, a) }

// Defaults of [Config].
const (
	DefaultQueueSize     = 1024
	DefaultRetryInterval = 2 * time.Second
)

// Config configures a server.
type Config struct {
	iec101.Params

	// LinkAddr is this station's link address.
	LinkAddr uint16
	// Remote is the controlling station's link address in balanced mode.
	Remote uint16
	// CommonAddr is the station's common address, carried by the end of
	// initialization.
	CommonAddr asdu.CommonAddr

	// Handler handles control-direction ASDUs. Nil rejects every request
	// with cause "unknown type identification".
	Handler Handler

	// QueueSize bounds each of the class 1 and class 2 queues (and the
	// balanced send queue). Zero means [DefaultQueueSize]. Send returns
	// [ErrQueueFull] when a queue is full.
	QueueSize int
	// RetryInterval is the pause before a balanced station initializes its
	// direction of the link again. Zero means [DefaultRetryInterval].
	RetryInterval time.Duration

	// NoEndOfInit suppresses end of initialization after the first reset
	// of the link.
	NoEndOfInit bool
	// InitCause is the cause of initialization (COI) of that end of
	// initialization: [asdu.InitLocalPowerOn] by default.
	InitCause uint8

	// OnState is called with each change of the link state, from the
	// goroutine that changed it: the link's reader (unbalanced), the
	// server's supervisor (balanced), or Close. It must not call Close,
	// which waits for those goroutines: call it in a new goroutine.
	OnState func(s iec101.State, err error)
	// Logger receives the log entries; nil is silent.
	Logger iec60870.Logger
}

// ErrQueueFull reports that an ASDU could not be queued for transmission.
var ErrQueueFull = errors.New("iec101: send queue full")

// Class selects the data class of a monitor-direction ASDU in unbalanced
// transmission.
type Class = link.Class

// Data classes.
const (
	Class1 = link.Class1
	Class2 = link.Class2
)

// Server is a controlled station.
type Server struct {
	cfg Config
	log *logx.Logger

	sec *link.Secondary // unbalanced
	bal *link.Balanced  // balanced

	mu       sync.Mutex
	class    [2][][]byte // unbalanced queues
	out      chan []byte // balanced queue
	state    iec101.State
	initSent bool

	in     chan *asdu.ASDU // received requests for the handler goroutine
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// New starts a controlled station on rw. Close stops it and closes rw when
// it is an io.Closer; rw's Close must unblock its Read for Close to
// return.
func New(rw io.ReadWriter, cfg Config) (*Server, error) {
	p, err := cfg.Normalize()
	if err != nil {
		return nil, err
	}
	cfg.Params = p
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = DefaultQueueSize
	}
	if cfg.RetryInterval <= 0 {
		cfg.RetryInterval = DefaultRetryInterval
	}
	s := &Server{
		cfg: cfg,
		log: logx.New(cfg.Logger, "iec101.server", "link", cfg.LinkAddr),
		in:  make(chan *asdu.ASDU, 64),
	}
	s.ctx, s.cancel = context.WithCancel(context.Background())
	if p.Mode == iec101.ModeBalanced {
		s.out = make(chan []byte, cfg.QueueSize)
		s.bal, err = link.NewBalanced(rw, p.Link, link.BalancedConfig{Addr: cfg.LinkAddr, Remote: cfg.Remote}, balancedUser{s})
	} else {
		s.sec, err = link.NewSecondary(rw, p.Link, cfg.LinkAddr, secondaryUser{s})
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %w", iec101.ErrInvalidConfig, err)
	}
	s.wg.Add(1)
	go s.handle()
	if s.bal != nil {
		s.wg.Add(1)
		go s.runBalanced()
	}
	return s, nil
}

// Close stops the station and waits for its goroutines. Close is safe to
// call more than once and from several goroutines: the first call closes
// the transport and returns its error; later calls return nil. It must
// not be called from the Handler or Config.OnState (see there).
func (s *Server) Close() error {
	s.cancel()
	var err error
	if s.bal != nil {
		err = s.bal.Close()
	} else {
		err = s.sec.Close()
	}
	s.wg.Wait()
	s.setState(iec101.StateClosed, nil)
	return err
}

// Done is closed when the station stops: after Close, or when the
// transport fails (for example the TCP connection to a terminal server
// closes). Err tells which.
func (s *Server) Done() <-chan struct{} {
	if s.bal != nil {
		return s.bal.Done()
	}
	return s.sec.Done()
}

// Err returns the transport error that stopped the station, nil while it
// runs or after Close.
func (s *Server) Err() error {
	if s.bal != nil {
		return s.bal.Err()
	}
	return s.sec.Err()
}

// State returns the link state: up once the controlling station reset the
// link (unbalanced) or both directions are initialized (balanced).
func (s *Server) State() iec101.State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

func (s *Server) setState(st iec101.State, err error) {
	s.mu.Lock()
	if s.state == st || s.state == iec101.StateClosed {
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

// Params returns the normalized system parameters.
func (s *Server) Params() iec101.Params { return s.cfg.Params }

// Send queues a monitor-direction ASDU: as class 1 data in unbalanced
// transmission (events, confirmations), or for transmission in balanced
// transmission.
func (s *Server) Send(a *asdu.ASDU) error { return s.SendClass(a, Class1) }

// SendClass queues a monitor-direction ASDU as class 1 or class 2 data.
// In balanced transmission the class is ignored.
func (s *Server) SendClass(a *asdu.ASDU, c Class) error {
	if s.cfg.Strict {
		if err := iec101.Validate(a, s.cfg.ASDU); err != nil {
			return err
		}
	}
	raw, err := a.Encode(s.cfg.ASDU)
	if err != nil {
		return err
	}
	return s.queue(raw, c)
}

// queue queues an encoded ASDU as class c data.
func (s *Server) queue(raw []byte, c Class) error {
	if s.ctx.Err() != nil {
		return iec101.ErrClosed
	}
	if s.bal != nil {
		select {
		case s.out <- raw:
			return nil
		default:
			return ErrQueueFull
		}
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
	s.class[i] = append(s.class[i], raw)
	return nil
}

// Pending returns the number of queued ASDUs of class c (unbalanced) or
// in the send queue (balanced).
func (s *Server) Pending(c Class) int {
	if s.bal != nil {
		return len(s.out)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if c == Class2 {
		return len(s.class[1])
	}
	return len(s.class[0])
}

// Reply mirrors req with the given cause and P/N bit.
func (s *Server) Reply(req *asdu.ASDU, cause asdu.Cause, negative bool) error {
	return s.Send(req.Reply(cause, negative))
}

// Confirm sends the positive (de)activation confirmation of req.
func (s *Server) Confirm(req *asdu.ASDU) error {
	return s.Reply(req, telecontrol.Confirmation(req), false)
}

// Negative sends the negative (de)activation confirmation of req.
func (s *Server) Negative(req *asdu.ASDU) error {
	return s.Reply(req, telecontrol.Confirmation(req), true)
}

// Terminate sends the activation termination of req.
func (s *Server) Terminate(req *asdu.ASDU) error {
	return s.Reply(req, asdu.CauseActivationTerm, false)
}

// Reject mirrors req negatively with one of the "unknown ..." causes.
func (s *Server) Reject(req *asdu.ASDU, cause asdu.Cause) error {
	return s.Reply(req, cause, true)
}

func (s *Server) receive(data []byte) bool {
	a, err := asdu.Decode(data, s.cfg.ASDU)
	if err != nil {
		s.log.Warn("undecodable ASDU dropped", "error", err)
		return true // the link layer accepted the frame
	}
	select {
	case s.in <- a:
		return true
	default:
		return false // busy: NACK with DFC
	}
}

func (s *Server) handle() {
	defer s.wg.Done()
	for {
		select {
		case a := <-s.in:
			if s.cfg.Strict && !s.admit(a) {
				continue
			}
			if s.cfg.Handler == nil {
				_ = s.Reject(a, asdu.CauseUnknownType)
				continue
			}
			s.cfg.Handler.HandleASDU(s, a)
		case <-s.ctx.Done():
			return
		}
	}
}

// admit applies the profile checks of Params.Strict to a received ASDU: one
// that fails is mirrored negatively with the "unknown ..." cause that fits
// (IEC 60870-5-101:2003 7.2.3), or dropped.
func (s *Server) admit(a *asdu.ASDU) bool {
	err := iec101.Validate(a, s.cfg.ASDU)
	if err == nil {
		return true
	}
	s.log.Warn("ASDU refused by the IEC 60870-5-101 profile", "asdu", a.String(), "error", err)
	var pe *iec101.ProfileError
	if errors.As(err, &pe) && pe.Cause != 0 && a.Type.InControlDirection() {
		// The mirror itself would fail the checks (reserved type, cause 0,
		// common address 0): sent without them.
		r := a.Reply(pe.Cause, true)
		if raw, err := r.Encode(s.cfg.ASDU); err == nil {
			_ = s.queue(raw, Class1)
		}
	}
	return false
}

func (s *Server) endOfInit(coi uint8) {
	if s.cfg.NoEndOfInit {
		return
	}
	if coi == asdu.InitLocalPowerOn {
		coi = s.cfg.InitCause
	}
	_ = s.Send(asdu.New(asdu.CauseInitialized, s.cfg.CommonAddr, asdu.EndOfInitialization{Cause: coi}))
}

type secondaryUser struct{ s *Server }

func (u secondaryUser) UserData(data []byte, _ bool) bool { return u.s.receive(data) }

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

func (u secondaryUser) Reset(user bool) {
	s := u.s
	if user {
		// Reset of user process: discard what was queued (IEC 60870-5-2
		// 5.1, function code 1).
		s.mu.Lock()
		s.class = [2][][]byte{}
		s.mu.Unlock()
		s.endOfInit(asdu.InitRemoteReset)
		return
	}
	s.setState(iec101.StateUp, nil)
	s.mu.Lock()
	first := !s.initSent
	s.initSent = true
	s.mu.Unlock()
	if first {
		s.endOfInit(asdu.InitLocalPowerOn)
	}
}

// sendBalanced sends one ASDU, again while the controlling station refuses
// it as busy: NACK, or DFC that stays set (IEC 60870-5-101:2003 figure 8,
// "secondary link busy").
func (s *Server) sendBalanced(raw []byte) error {
	for {
		err := s.bal.Send(s.ctx, raw)
		if !errors.Is(err, link.ErrNack) && !errors.Is(err, link.ErrDataFlow) {
			return err
		}
		s.log.Debug("controlling station busy, sending again", "error", err)
		t := time.NewTimer(s.cfg.Link.Timeout)
		select {
		case <-t.C:
		case <-s.ctx.Done():
			t.Stop()
			return err
		}
	}
}

// busy reports a handler queue more than half full: the link sets DFC and
// refuses further user data (IEC 60870-5-101:2003 figures 6 and 9).
func (s *Server) busy() bool { return len(s.in) > cap(s.in)/2 }

func (u secondaryUser) Busy() bool { return u.s.busy() }

type balancedUser struct{ s *Server }

func (u balancedUser) Busy() bool { return u.s.busy() }

func (u balancedUser) UserData(data []byte) bool { return u.s.receive(data) }

func (u balancedUser) Reset(user bool) {
	if user {
		u.s.endOfInit(asdu.InitRemoteReset)
	}
}

func (s *Server) runBalanced() {
	defer s.wg.Done()
	for s.ctx.Err() == nil {
		if err := s.bal.Init(s.ctx); err != nil {
			s.log.Debug("link init failed", "error", err)
			t := time.NewTimer(s.cfg.RetryInterval)
			select {
			case <-t.C:
			case <-s.ctx.Done():
				t.Stop()
				return
			}
			continue
		}
		s.setState(iec101.StateUp, nil)
		s.mu.Lock()
		first := !s.initSent
		s.initSent = true
		s.mu.Unlock()
		if first {
			s.endOfInit(asdu.InitLocalPowerOn)
		}
		for s.ctx.Err() == nil {
			var raw []byte
			select {
			case raw = <-s.out:
			case <-s.ctx.Done():
				return
			}
			if err := s.sendBalanced(raw); err != nil {
				s.log.Warn("send failed", "error", err)
				if errors.Is(err, link.ErrTimeout) {
					s.setState(iec101.StateDown, err)
					break
				}
			}
		}
	}
}
