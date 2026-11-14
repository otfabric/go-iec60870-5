// SPDX-License-Identifier: MIT

// Package server is the controlled station of IEC 60870-5-102, the
// integrated total station (a meter or data concentrator): the secondary
// station of unbalanced transmission. It answers the link procedures of
// the controlling station and serves the standard read commands from a
// [Store] of records:
//
//   - C_RD_NA_2 with P_MP_NA_2 (cause 5), from [Config.ProductSpec];
//   - C_TI_NA_2 with M_TI_TA_2 (cause 5), from [Config.Clock];
//   - C_CI_NA_2 .. C_CI_NU_2 with the activation confirmation (cause 7),
//     the selected integration periods as M_IT_TA_2 .. M_IT_TM_2 (cause 5)
//     and the activation termination (cause 10);
//   - C_SP_NA_2 and C_SP_NB_2 the same way with M_SP_TA_2.
//
// A request it cannot serve is mirrored with a cause of refusal: 16 for a
// record address the store does not hold, 14 for a record without
// integrated totals of the requested family (or an unknown type without a
// [Handler]), 18 when no integration period matches, 17 when the address
// range selects no total. These procedures and the choice of causes are
// interpretations of the Beckhoff TS650x manual (section 9.1.11 names the
// causes) and of the recorded traffic; see docs/iec102/INTEROPERABILITY.md.
//
// Every other ASDU (the REE profile's session, clock setting and tariff
// requests, private types) goes to the [Handler], which answers through
// [Server.Send] and [Server.Mirror]. Answers are class 2 data: the
// controlling stations we compare with (gisce/iec870ree,
// daiiniel/IEC60870-5-102) poll class 2 only.
package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"

	iec60870 "github.com/otfabric/go-iec60870-5"
	"github.com/otfabric/go-iec60870-5/iec102"
	"github.com/otfabric/go-iec60870-5/internal/callback"
	"github.com/otfabric/go-iec60870-5/internal/logx"
	"github.com/otfabric/go-iec60870-5/link"
)

// Handler handles the control-direction ASDUs the server does not answer
// itself. Calls are made one at a time from a goroutine of the server; the
// handler answers through the Server, whose methods it may call. Close
// from the handler does not wait for the handler's goroutine: it returns
// at once, and the close completes when the handler has returned. A
// handler that does not return normally is logged with its stack and
// closes the server; [Server.Err] reports it.
//
// The ASDU is the handler's own: a new one is decoded for each call and the
// server keeps no reference to it, so the handler may keep or modify it.
type Handler interface {
	HandleASDU(s *Server, a *iec102.ASDU)
}

// HandlerFunc adapts a function to [Handler].
type HandlerFunc func(s *Server, a *iec102.ASDU)

// HandleASDU calls f.
func (f HandlerFunc) HandleASDU(s *Server, a *iec102.ASDU) { f(s, a) }

// DefaultQueueSize bounds the class 2 queue when [Config.QueueSize] is
// zero.
const DefaultQueueSize = 4096

// ErrQueueFull reports that an ASDU could not be queued for transmission.
var ErrQueueFull = errors.New("iec102: send queue full")

// Config configures a server.
type Config struct {
	// Link holds the link options; Link.FT12 is set from LinkAddrSize.
	Link link.Config
	// LinkAddrSize is the size of the link address field: 1 or 2 octets,
	// [iec102.NoLinkAddr] for none. Zero means
	// [iec102.DefaultLinkAddrSize].
	LinkAddrSize int
	// LinkAddr is this station's link address.
	LinkAddr uint16
	// CommonAddr is the common address of ASDU (measuring point address)
	// this station serves; requests to another are refused with cause 16.
	CommonAddr uint16
	// Params are the codec parameters.
	Params iec102.Params

	// ProductSpec answers C_RD_NA_2.
	ProductSpec iec102.ProductSpec
	// Store holds the records the server serves. Nil serves none.
	Store *Store
	// Clock returns the station's time for C_TI_NA_2. Nil means time.Now.
	// A Clock that does not return normally closes the server, as for the
	// Handler.
	Clock func() time.Time

	// Handler handles the other control-direction ASDUs. Nil refuses them
	// with cause 14.
	Handler Handler
	// QueueSize bounds the class 2 queue. Zero means [DefaultQueueSize].
	QueueSize int
	// OnState is called with each change of the link state: from the
	// station's link goroutine on a reset of remote link, and from Close
	// for StateClosed. It must return quickly. It may call Close, which
	// then does not wait for the link goroutine. An OnState that does not
	// return normally is logged and closes the server, as for the Handler.
	OnState func(s iec102.State, err error)
	// Logger receives the log entries; nil is silent.
	Logger iec60870.Logger
}

// Server is an integrated total station.
type Server struct {
	cfg    Config
	log    *logx.Logger
	sec    *link.Secondary
	budget int // largest ASDU one frame carries

	mu    sync.Mutex
	queue [][]byte
	state iec102.State

	in     chan []byte
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	closeOnce sync.Once
	closeErr  error

	workID   atomic.Uint64 // the goroutine of work
	onReader atomic.Uint64 // the link goroutine, while it calls OnState
	failMu   sync.Mutex
	failed   error // why a callback stopped the server
}

// New starts a server on rw. Close stops it and closes rw when it is an
// io.Closer.
func New(rw io.ReadWriter, cfg Config) (*Server, error) {
	fp, err := iec102.LinkParams(cfg.LinkAddrSize)
	if err != nil {
		return nil, err
	}
	if err := cfg.Params.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %w", iec102.ErrInvalidConfig, err)
	}
	cfg.Link.FT12 = fp
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = DefaultQueueSize
	}
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}
	if cfg.Store == nil {
		cfg.Store = NewStore()
	}
	s := &Server{
		cfg:    cfg,
		log:    logx.New(cfg.Logger, "iec102.server", "link", cfg.LinkAddr),
		budget: min(fp.MaxData(), iec102.MaxSize),
		in:     make(chan []byte, 16),
	}
	s.ctx, s.cancel = context.WithCancel(context.Background())
	lc := cfg.Link
	if lc.Logger == nil {
		lc.Logger = cfg.Logger
	}
	sec, err := link.NewSecondary(rw, lc, cfg.LinkAddr, (*linkUser)(s))
	if err != nil {
		s.cancel()
		return nil, fmt.Errorf("%w: %w", iec102.ErrInvalidConfig, err)
	}
	s.sec = sec
	s.wg.Add(1)
	go s.work()
	return s, nil
}

// Close stops the station. Close is safe to call more than once and from
// several goroutines: the first call closes the transport and returns its
// error; later calls return nil.
//
// Called from the Handler, the Clock or OnState, Close cannot wait for the
// goroutine it runs on: it stops the server and returns nil at once, and
// the close completes, StateClosed reported included, when that callback
// has returned.
func (s *Server) Close() error {
	if id := callback.Goroutine(); id == s.workID.Load() || id == s.onReader.Load() {
		s.closeAsync()
		return nil
	}
	return s.close()
}

func (s *Server) closeAsync() {
	s.cancel()
	go func() { _ = s.close() }()
}

// stop ends the server after a callback did not return normally: Err
// reports err from now on, unless the server was closing already.
func (s *Server) stop(err error) {
	s.failMu.Lock()
	if s.failed == nil && s.ctx.Err() == nil {
		s.failed = fmt.Errorf("%w: %w", iec102.ErrClosed, err)
	}
	s.failMu.Unlock()
	s.closeAsync()
}

func (s *Server) close() error {
	first := false
	s.closeOnce.Do(func() {
		first = true
		s.cancel()
		s.closeErr = s.sec.Close()
		s.wg.Wait()
	})
	if !first {
		return nil
	}
	// Outside the Once: OnState(StateClosed) may call Close.
	s.setState(iec102.StateClosed, nil)
	return s.closeErr
}

// Done is closed when the station stops: after Close, a transport
// failure, or a callback that did not return normally. Err tells which.
func (s *Server) Done() <-chan struct{} { return s.sec.Done() }

// Err returns the error that stopped the station: the transport's, or that
// of a callback that did not return normally (wrapping
// [iec102.ErrClosed]); nil while it runs or after Close.
func (s *Server) Err() error {
	s.failMu.Lock()
	failed := s.failed
	s.failMu.Unlock()
	if failed != nil {
		return failed
	}
	return s.sec.Err()
}

// State returns the link state: up after the first reset of remote link.
func (s *Server) State() iec102.State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

// CommonAddr returns the common address of ASDU the server serves.
func (s *Server) CommonAddr() uint16 { return s.cfg.CommonAddr }

// Params returns the codec parameters.
func (s *Server) Params() iec102.Params { return s.cfg.Params }

// Now returns the station's time.
func (s *Server) Now() time.Time { return s.cfg.Clock() }

// Store returns the store of records.
func (s *Server) Store() *Store { return s.cfg.Store }

func (s *Server) setState(st iec102.State, err error) {
	s.mu.Lock()
	if s.state == st || s.state == iec102.StateClosed {
		s.mu.Unlock()
		return
	}
	s.state = st
	s.mu.Unlock()
	s.log.Info("link state", "state", st.String(), "error", err)
	if f := s.cfg.OnState; f != nil {
		if cerr := callback.Call(s.log, "OnState", func() { f(st, err) }); cerr != nil {
			s.stop(cerr)
		}
	}
}

// Send queues a monitor-direction ASDU as class 2 data.
func (s *Server) Send(a *iec102.ASDU) error {
	raw, err := a.Encode(s.cfg.Params)
	if err != nil {
		return err
	}
	if len(raw) > s.budget {
		return fmt.Errorf("%w: %d octets, the link carries %d", iec102.ErrTooLong, len(raw), s.budget)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state == iec102.StateClosed {
		return iec102.ErrClosed
	}
	if len(s.queue) >= s.cfg.QueueSize {
		return ErrQueueFull
	}
	s.queue = append(s.queue, raw)
	return nil
}

// Mirror queues the request req with cause c: a confirmation (7), a
// termination (10) or a refusal (13..18, or with the P/N bit).
func (s *Server) Mirror(req *iec102.ASDU, c iec102.Cause) error {
	m := *req
	m.Cause = c
	return s.Send(&m)
}

// linkUser is the server as the link's SecondaryHandler.
type linkUser Server

func (u *linkUser) UserData(data []byte, _ bool) bool {
	select {
	case u.in <- data:
		return true
	default:
		return false
	}
}

func (u *linkUser) Class1() []byte  { return nil }
func (u *linkUser) HasClass1() bool { return false }

func (u *linkUser) Class2() []byte {
	u.mu.Lock()
	defer u.mu.Unlock()
	if len(u.queue) == 0 {
		return nil
	}
	b := u.queue[0]
	u.queue = u.queue[1:]
	return b
}

func (u *linkUser) Reset(user bool) {
	s := (*Server)(u)
	if !user {
		s.mu.Lock()
		s.queue = nil
		s.mu.Unlock()
		s.onReader.Store(callback.Goroutine())
		s.setState(iec102.StateUp, nil)
		s.onReader.Store(0)
	}
}

func (s *Server) work() {
	defer s.wg.Done()
	s.workID.Store(callback.Goroutine())
	for {
		select {
		case data := <-s.in:
			if s.ctx.Err() != nil {
				return // Close from a callback came first
			}
			a, err := iec102.Decode(data, s.cfg.Params)
			if err != nil {
				s.log.Warn("undecodable ASDU dropped", "error", err)
				continue
			}
			// serve calls the application's Handler or Clock.
			name := "Handler"
			if r, ok := a.Body.(iec102.Read); ok && r.T == iec102.C_TI_NA_2 {
				name = "Clock"
			}
			var serr error
			if cerr := callback.Call(s.log, name, func() { serr = s.serve(a) }); cerr != nil {
				s.stop(cerr)
				return
			}
			if serr != nil {
				s.log.Warn("answer not queued", "asdu", a.String(), "error", serr)
			}
		case <-s.ctx.Done():
			return
		}
	}
}

func (s *Server) serve(a *iec102.ASDU) error {
	if a.CommonAddr != s.cfg.CommonAddr {
		return s.Mirror(a, iec102.CauseUnknownRecordAddr)
	}
	switch b := a.Body.(type) {
	case iec102.Read:
		switch b.T {
		case iec102.C_RD_NA_2:
			return s.Send(iec102.New(iec102.CauseRequest, a.CommonAddr, a.Record, s.cfg.ProductSpec))
		case iec102.C_TI_NA_2:
			t := iec102.TimeBOf(s.cfg.Clock(), s.cfg.Params.Location)
			return s.Send(iec102.New(iec102.CauseRequest, a.CommonAddr, a.Record, iec102.SystemTime{Time: t}))
		case iec102.C_SP_NA_2, iec102.C_SP_NB_2:
			return s.serveSinglePoints(a, b)
		default:
			return s.serveTotals(a, b)
		}
	}
	if s.cfg.Handler == nil {
		return s.Mirror(a, iec102.CauseUnknownType)
	}
	s.cfg.Handler.HandleASDU(s, a)
	return nil
}

func (s *Server) serveTotals(a *iec102.ASDU, q iec102.Read) error {
	periods, cause := s.cfg.Store.selectTotals(a.Record, q)
	if cause != 0 {
		return s.Mirror(a, cause)
	}
	if err := s.Mirror(a, iec102.CauseActivationCon); err != nil {
		return err
	}
	for _, p := range periods {
		for _, part := range s.split(p) {
			if err := s.Send(iec102.New(iec102.CauseRequest, a.CommonAddr, a.Record, part)); err != nil {
				return err
			}
		}
	}
	return s.Mirror(a, iec102.CauseActivationTerm)
}

// split cuts a period into bodies that fit one frame.
func (s *Server) split(p iec102.IntegratedTotals) []iec102.IntegratedTotals {
	w, _ := p.T.IntegratedTotalsType()
	per := w + 2
	if p.Signed {
		per++
	}
	n := min(max((s.budget-s.cfg.Params.HeaderSize()-iec102.SizeTimeA)/per, 1), 127)
	if len(p.Totals) <= n {
		return []iec102.IntegratedTotals{p}
	}
	var out []iec102.IntegratedTotals
	for i := 0; i < len(p.Totals); i += n {
		q := p
		q.Totals = p.Totals[i:min(i+n, len(p.Totals))]
		out = append(out, q)
	}
	return out
}

func (s *Server) serveSinglePoints(a *iec102.ASDU, q iec102.Read) error {
	pts, ok := s.cfg.Store.selectPoints(a.Record, q)
	if !ok {
		return s.Mirror(a, iec102.CauseUnknownRecordAddr)
	}
	if err := s.Mirror(a, iec102.CauseActivationCon); err != nil {
		return err
	}
	n := min((s.budget-s.cfg.Params.HeaderSize())/(2+iec102.SizeTimeB), 127)
	for i := 0; i < len(pts); i += n {
		body := iec102.SinglePoints{Points: pts[i:min(i+n, len(pts))]}
		if err := s.Send(iec102.New(iec102.CauseRequest, a.CommonAddr, a.Record, body)); err != nil {
			return err
		}
	}
	return s.Mirror(a, iec102.CauseActivationTerm)
}
