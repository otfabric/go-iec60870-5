// SPDX-License-Identifier: MIT

// Package client is the controlling station of IEC 60870-5-101: it brings
// up the link, polls the controlled station (unbalanced transmission) or
// exchanges messages with it (balanced transmission), runs requests
// (interrogation, counter interrogation, read, commands, clock
// synchronization, test, reset process, delay acquisition) and delivers
// monitor-direction ASDUs to a handler.
//
// The link is initialized in the background and re-initialized when the
// controlled station stops answering, or answers only with frames that
// cannot answer the message until the last time-out (link.ErrUnexpected
// wrapping link.ErrTimeout); [Client.WaitUp] waits for it and returns only
// after Config.OnState for the change to StateUp has returned. Requests
// fail with [iec101.ErrLinkDown] while the link is down, and return at
// their context deadline even while the link layer is still repeating
// their user data. After a failed poll the client waits PollInterval
// before it polls again.
package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"

	iec60870 "github.com/otfabric/go-iec60870-5"
	"github.com/otfabric/go-iec60870-5/asdu"
	"github.com/otfabric/go-iec60870-5/iec101"
	"github.com/otfabric/go-iec60870-5/internal/callback"
	"github.com/otfabric/go-iec60870-5/internal/logx"
	"github.com/otfabric/go-iec60870-5/link"
	"github.com/otfabric/go-iec60870-5/telecontrol"
)

// Defaults of [Config].
const (
	DefaultPollInterval   = 100 * time.Millisecond
	DefaultRequestTimeout = 10 * time.Second
	DefaultRetryInterval  = 2 * time.Second
	DefaultTestInterval   = 10 * time.Second
	// DefaultMaxResult is the default of Config.MaxResult: an
	// interrogation of 65536 ASDUs, each of one or more objects, is far
	// beyond what a controlled station holds.
	DefaultMaxResult = 65536
)

// Config configures a client.
type Config struct {
	iec101.Params

	// LinkAddr is the link address of the controlled station. In balanced
	// mode it is the address carried by this station's primary messages.
	LinkAddr uint16
	// OwnAddr is this station's link address in balanced mode.
	OwnAddr uint16

	// PollInterval is the pause between class 2 polls when the controlled
	// station has nothing to send (unbalanced). Zero means
	// [DefaultPollInterval].
	PollInterval time.Duration
	// RequestTimeout bounds every request whose context has no earlier
	// deadline. Zero means [DefaultRequestTimeout].
	RequestTimeout time.Duration
	// RetryInterval is the pause before the link is initialized again
	// after a failure. Zero means [DefaultRetryInterval].
	RetryInterval time.Duration
	// TestInterval is how often an idle balanced link is tested with the
	// test function for link. Zero means [DefaultTestInterval]; negative
	// disables the test.
	TestInterval time.Duration
	// MaxResult bounds the ASDUs one interrogation or counter
	// interrogation collects: the request that would collect more ends
	// with an error wrapping [telecontrol.ErrResultLimit], with the ASDUs
	// so far, instead of growing until its deadline. Zero means
	// [DefaultMaxResult]; negative means no bound, as before v0.6.0.
	MaxResult int

	// Handler receives every monitor-direction ASDU that is not a mirror of
	// a request: spontaneous and cyclic data, and the data that answers an
	// interrogation or read (also returned by those calls). It runs on a
	// goroutine of its own, in order, and the ASDUs wait for it in a queue
	// of 256 (in balanced transmission the link refuses user data, DFC
	// set, while more than half of them wait).
	//
	// The Handler must not wait for the station: while it runs nothing
	// empties the queue, and the answers a request waits for go through
	// it. A request method that waits for an answer (Interrogate,
	// CounterInterrogate, Read, Command, Deactivate, ClockSync,
	// TestCommand, ResetProcess, MeasureDelay) called from the Handler
	// returns [iec101.ErrInHandler] at once. Send and SetDelay wait only
	// for the link layer; in unbalanced transmission that is the poller,
	// which waits for room in the queue, so they too are best made from
	// another goroutine. The Handler may call Close, which then does not
	// wait for the Handler's goroutine: it returns at once, the Handler is
	// not called again, and the client completes the close when the
	// Handler has returned. A Handler that does not return normally is
	// logged with its stack and closes the client; Err reports it.
	Handler func(a *asdu.ASDU)
	// OnState is called with each change of the link state, from the
	// goroutine that changed it: one of the client's own goroutines, or
	// Close. WaitUp returns only after OnState for the change to StateUp
	// has returned, so what the callback sets up is in place when WaitUp
	// returns. State reports a change before OnState for it is called.
	// Called on one of the client's goroutines, OnState cannot have a
	// request answered: the request methods, Send and SetDelay return
	// [iec101.ErrInHandler]; start them in a new goroutine. It may call
	// Close, as the Handler may. An OnState that does not return normally
	// is logged and closes the client, as for the Handler.
	OnState func(s iec101.State, err error)
	// Logger receives the log entries; nil is silent.
	Logger iec60870.Logger
}

// Client is a controlling station.
type Client struct {
	cfg Config
	req telecontrol.Requester
	log *logx.Logger

	pri *link.Primary  // unbalanced
	bal *link.Balanced // balanced

	sends   chan sendReq // user data for the poller or the supervisor
	handled chan *asdu.ASDU

	mu     sync.Mutex
	state  iec101.State
	upCh   chan struct{} // closed while the link is up and OnState was told
	upTold bool          // OnState(StateUp) returned for the current up period
	kick   chan struct{} // the link went down: re-initialize now

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// handlerID and superID are the goroutines of deliver and of the
	// supervisor (poller), on which the callbacks run.
	handlerID, superID atomic.Uint64
	failMu             sync.Mutex
	failed             error // why a callback stopped the client
}

type sendReq struct {
	data []byte
	done chan error
}

// New starts a controlling station on rw. Close stops it and closes rw
// when it is an io.Closer; rw's Close must unblock its Read for Close to
// return.
func New(rw io.ReadWriter, cfg Config) (*Client, error) {
	p, err := cfg.Normalize()
	if err != nil {
		return nil, err
	}
	cfg.Params = p
	if cfg.LinkAddr > p.Link.FT12.MaxAddr() || cfg.OwnAddr > p.Link.FT12.MaxAddr() {
		return nil, fmt.Errorf("%w: link address does not fit %d octet(s)", iec101.ErrInvalidConfig, p.Link.FT12.AddrSize)
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = DefaultPollInterval
	}
	if cfg.RequestTimeout <= 0 {
		cfg.RequestTimeout = DefaultRequestTimeout
	}
	if cfg.RetryInterval <= 0 {
		cfg.RetryInterval = DefaultRetryInterval
	}
	if cfg.TestInterval == 0 {
		cfg.TestInterval = DefaultTestInterval
	}
	switch {
	case cfg.MaxResult == 0:
		cfg.MaxResult = DefaultMaxResult
	case cfg.MaxResult < 0:
		cfg.MaxResult = 0 // no bound
	}
	c := &Client{
		cfg:     cfg,
		log:     logx.New(cfg.Logger, "iec101.client", "link", cfg.LinkAddr),
		sends:   make(chan sendReq),
		handled: make(chan *asdu.ASDU, 256),
		upCh:    make(chan struct{}),
		kick:    make(chan struct{}, 1),
	}
	c.req = telecontrol.Requester{Params: p.ASDU, Send: c.send, Timeout: cfg.RequestTimeout, MaxResult: cfg.MaxResult}
	c.ctx, c.cancel = context.WithCancel(context.Background())
	lc := p.Link
	if lc.Logger == nil {
		lc.Logger = cfg.Logger
	}
	if p.Mode == iec101.ModeBalanced {
		c.bal, err = link.NewBalanced(rw, lc,
			link.BalancedConfig{Addr: cfg.OwnAddr, Remote: cfg.LinkAddr, DIR: true}, balancedUser{c})
		if err != nil {
			return nil, fmt.Errorf("%w: %w", iec101.ErrInvalidConfig, err)
		}
	} else {
		c.pri, err = link.NewPrimary(rw, lc)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", iec101.ErrInvalidConfig, err)
		}
	}
	c.wg.Add(2)
	go c.deliver()
	if c.bal != nil {
		go c.runBalanced()
	} else {
		go c.runUnbalanced()
	}
	return c, nil
}

// Done is closed when the link station stops: after Close, or when the
// transport fails. Err tells which.
func (c *Client) Done() <-chan struct{} {
	if c.bal != nil {
		return c.bal.Done()
	}
	return c.pri.Done()
}

// Err returns the error that stopped the station: the transport's, or that
// of a callback that did not return normally (wrapping
// [iec101.ErrClosed]); nil while it runs or after Close.
func (c *Client) Err() error {
	c.failMu.Lock()
	failed := c.failed
	c.failMu.Unlock()
	if failed != nil {
		return failed
	}
	if c.bal != nil {
		return c.bal.Err()
	}
	return c.pri.Err()
}

// State returns the link state.
func (c *Client) State() iec101.State {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state
}

// WaitUp waits until the link is initialized and OnState has been told
// (Config.OnState). It returns the transport error when the transport fails
// first (closed by the peer, say): the client does not reopen a transport,
// so the link cannot come up on it.
func (c *Client) WaitUp(ctx context.Context) error {
	for {
		c.mu.Lock()
		st, ch, told := c.state, c.upCh, c.upTold
		c.mu.Unlock()
		switch {
		case st == iec101.StateUp && told:
			return nil
		case st == iec101.StateClosed:
			return iec101.ErrClosed
		}
		select {
		case <-ch:
		case <-ctx.Done():
			return fmt.Errorf("iec101: waiting for link: %w", ctx.Err())
		case <-c.ctx.Done():
			return iec101.ErrClosed
		case <-c.Done():
			// The transport failed: the link cannot come up again on it.
			if err := c.Err(); err != nil && c.ctx.Err() == nil {
				return fmt.Errorf("iec101: waiting for link: %w", err)
			}
			return iec101.ErrClosed
		}
	}
}

// Close stops the station and waits for its goroutines. Outstanding
// requests, and requests made afterwards, fail with [iec101.ErrClosed].
// Close is safe to call more than once and from several goroutines: the
// first call closes the transport and returns its error; later calls
// return nil.
//
// Called from Config.Handler or Config.OnState on one of the client's
// goroutines, Close cannot wait for that goroutine: it stops the client
// and returns nil at once, and the close completes, StateClosed reported
// included, when the callback has returned.
func (c *Client) Close() error {
	if c.own() {
		c.closeAsync()
		return nil
	}
	return c.close()
}

func (c *Client) close() error {
	c.mu.Lock()
	if c.state == iec101.StateClosed {
		c.mu.Unlock()
		return nil
	}
	c.mu.Unlock()
	c.cancel()
	// Before the goroutines are joined: a request waiting for an answer
	// learns at once that none will come.
	c.req.FailAll(iec101.ErrClosed)
	var err error
	if c.bal != nil {
		err = c.bal.Close()
	} else {
		err = c.pri.Close()
	}
	c.wg.Wait()
	c.setState(iec101.StateClosed, nil)
	c.req.FailAll(iec101.ErrClosed)
	return err
}

// closeAsync stops the client from one of its own goroutines and completes
// the close on a new one.
func (c *Client) closeAsync() {
	c.cancel()
	c.req.FailAll(iec101.ErrClosed)
	go func() { _ = c.close() }()
}

// own reports whether the caller runs on one of the client's goroutines:
// the Handler's or the supervisor's.
func (c *Client) own() bool {
	id := callback.Goroutine()
	return id == c.handlerID.Load() || id == c.superID.Load()
}

// stop ends the client after a callback did not return normally: Err
// reports err from now on, unless the client was closing already.
func (c *Client) stop(err error) {
	c.failMu.Lock()
	if c.failed == nil && c.ctx.Err() == nil {
		c.failed = fmt.Errorf("%w: %w", iec101.ErrClosed, err)
	}
	c.failMu.Unlock()
	c.closeAsync()
}

// inHandler returns [iec101.ErrInHandler] for a request made on one of the
// client's own goroutines.
func (c *Client) inHandler(what string) error {
	if c.own() {
		return fmt.Errorf("%w: %s", iec101.ErrInHandler, what)
	}
	return nil
}

func (c *Client) setState(s iec101.State, err error) {
	c.mu.Lock()
	if c.state == s || c.state == iec101.StateClosed {
		c.mu.Unlock()
		return
	}
	c.state = s
	up := c.upCh
	if s != iec101.StateUp {
		c.upTold = false
		select {
		case <-c.upCh:
			c.upCh = make(chan struct{})
		default:
		}
		select {
		case c.kick <- struct{}{}:
		default:
		}
	}
	c.mu.Unlock()
	if s == iec101.StateDown && c.ctx.Err() == nil {
		c.req.FailAll(fmt.Errorf("%w: %w", iec101.ErrLinkDown, err))
	}
	c.log.Info("link state", "state", s.String(), "error", err)
	if f := c.cfg.OnState; f != nil {
		if cerr := callback.Call(c.log, "OnState", func() { f(s, err) }); cerr != nil {
			c.stop(cerr)
			return
		}
	}
	if s == iec101.StateUp {
		// WaitUp returns only now, after OnState(StateUp) returned, and
		// only if the link is still in this up period.
		c.mu.Lock()
		if c.state == iec101.StateUp && c.upCh == up && !c.upTold {
			c.upTold = true
			close(up)
		}
		c.mu.Unlock()
	}
}

// receive decodes user data from the controlled station and routes it.
func (c *Client) receive(data []byte) {
	a, err := asdu.Decode(data, c.cfg.ASDU)
	if err != nil {
		c.log.Warn("undecodable ASDU dropped", "error", err)
		return
	}
	if c.cfg.Strict {
		if err := iec101.Validate(a, c.cfg.ASDU); err != nil {
			// IEC 60870-5-101:2003 7.2.3: the controlling station may log
			// such ASDUs; they do not affect the following messages.
			c.log.Warn("ASDU dropped by the IEC 60870-5-101 profile", "asdu", a.String(), "error", err)
			return
		}
	}
	if c.req.Dispatch(a) {
		return
	}
	select {
	case c.handled <- a:
	case <-c.ctx.Done():
	}
}

func (c *Client) deliver() {
	defer c.wg.Done()
	c.handlerID.Store(callback.Goroutine())
	for {
		select {
		case a := <-c.handled:
			// Not after Close from a callback, which may have come first.
			if h := c.cfg.Handler; h != nil && c.ctx.Err() == nil {
				if err := callback.Call(c.log, "Handler", func() { h(a) }); err != nil {
					c.stop(err)
					return
				}
			}
		case <-c.ctx.Done():
			return
		}
	}
}

// send transmits a control-direction ASDU and returns when the link layer
// confirmed it.
func (c *Client) send(ctx context.Context, a *asdu.ASDU) error {
	if c.ctx.Err() != nil {
		return iec101.ErrClosed
	}
	if c.superID.Load() == callback.Goroutine() {
		// The supervisor is the goroutine that would send it.
		return fmt.Errorf("%w: %s", iec101.ErrInHandler, a.Type)
	}
	if c.State() != iec101.StateUp {
		return iec101.ErrLinkDown
	}
	if c.cfg.Strict {
		if err := iec101.Validate(a, c.cfg.ASDU); err != nil {
			return err
		}
	}
	raw, err := a.Encode(c.cfg.ASDU)
	if err != nil {
		return err
	}
	r := sendReq{data: raw, done: make(chan error, 1)}
	select {
	case c.sends <- r:
	case <-ctx.Done():
		return ctx.Err()
	case <-c.ctx.Done():
		return iec101.ErrClosed
	}
	// The poller (unbalanced) or the supervisor (balanced) finishes the
	// exchange it started (a primary repeats a message until it is
	// answered or Config.Retries ran out; giving up half way would leave
	// the FCB in doubt), but the request ends at its deadline.
	select {
	case err := <-r.done:
		if err != nil && c.ctx.Err() != nil {
			return iec101.ErrClosed // the link failed because of Close
		}
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-c.ctx.Done():
		return iec101.ErrClosed
	}
}

// Send transmits a in the control direction without waiting for an
// application answer. It returns when the link layer confirmed it, or at
// the deadline of ctx: the client's own goroutine (the poller in
// unbalanced transmission, the supervisor in balanced) then still
// finishes the exchange, repeating the frame until it is answered or
// Link.Retries ran out. Answers reach the Handler.
func (c *Client) Send(ctx context.Context, a *asdu.ASDU) error { return c.send(ctx, a) }

func (c *Client) linkFailed(err error) {
	if errors.Is(err, link.ErrTimeout) || errors.Is(err, link.ErrClosed) {
		c.setState(iec101.StateDown, err)
	}
}

// sleep waits for d, or less when the link goes down; it reports false
// when the client is closed.
func (c *Client) sleep(d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-c.kick:
		return true
	case <-c.ctx.Done():
		return false
	}
}

func (c *Client) runUnbalanced() {
	defer c.wg.Done()
	c.superID.Store(callback.Goroutine())
	addr := c.cfg.LinkAddr
	var acd, dfc bool
	for c.ctx.Err() == nil {
		if c.State() != iec101.StateUp {
			st, err := c.pri.Init(c.ctx, addr)
			if err != nil {
				c.log.Debug("link init failed", "error", err)
				select {
				case <-c.Done():
					return // the transport failed: nothing to initialize
				default:
				}
				if !c.sleep(c.cfg.RetryInterval) {
					return
				}
				continue
			}
			acd, dfc = st.ACD, st.DFC
			c.setState(iec101.StateUp, nil)
		}
		// User data first, unless the station asked to hold it back.
		if !dfc {
			select {
			case r := <-c.sends:
				st, err := c.pri.SendConfirm(c.ctx, addr, r.data)
				r.done <- err
				if err != nil {
					c.linkFailed(err)
					continue
				}
				acd, dfc = st.ACD, st.DFC
				continue
			default:
			}
		}
		class := link.Class2
		if acd {
			class = link.Class1
		}
		data, st, err := c.pri.RequestData(c.ctx, addr, class)
		if err != nil {
			c.linkFailed(err)
			// A station that answers every poll with an error (function
			// 14 or 15, say) is polled at the poll interval, not as fast
			// as it answers.
			if !c.sleep(c.cfg.PollInterval) {
				return
			}
			continue
		}
		acd, dfc = st.ACD, st.DFC
		if data != nil {
			c.receive(data)
			continue
		}
		if acd {
			continue
		}
		// Nothing to fetch: wait for the next poll or user data.
		t := time.NewTimer(c.cfg.PollInterval)
		select {
		case r := <-c.sends:
			t.Stop()
			if dfc {
				r.done <- link.ErrDataFlow
				continue
			}
			st, err := c.pri.SendConfirm(c.ctx, addr, r.data)
			r.done <- err
			if err != nil {
				c.linkFailed(err)
				continue
			}
			acd, dfc = st.ACD, st.DFC
		case <-t.C:
		case <-c.ctx.Done():
			t.Stop()
			return
		}
	}
}

// runBalanced is the supervisor of a balanced link: it initializes the
// link, sends the user data of the requests and tests the link every
// TestInterval. Every primary exchange runs here, under the client's own
// context, so a request whose context ends does not abandon an exchange
// half way.
func (c *Client) runBalanced() {
	defer c.wg.Done()
	c.superID.Store(callback.Goroutine())
	var test *time.Timer
	var testC <-chan time.Time
	if c.cfg.TestInterval > 0 {
		test = time.NewTimer(c.cfg.TestInterval)
		defer test.Stop()
		testC = test.C
	}
	for c.ctx.Err() == nil {
		if c.State() != iec101.StateUp {
			if err := c.bal.Init(c.ctx); err != nil {
				c.log.Debug("link init failed", "error", err)
				select {
				case <-c.Done():
					return // the transport failed: nothing to initialize
				default:
				}
				if !c.sleep(c.cfg.RetryInterval) {
					return
				}
				continue
			}
			c.setState(iec101.StateUp, nil)
			if test != nil {
				test.Reset(c.cfg.TestInterval) // the first test one interval after the link came up
			}
		}
		select {
		case r := <-c.sends:
			err := c.bal.Send(c.ctx, r.data)
			r.done <- err
			if err != nil {
				c.linkFailed(err)
			}
		case <-testC:
			if err := c.bal.Test(c.ctx); err != nil {
				c.linkFailed(err)
			}
			test.Reset(c.cfg.TestInterval)
		case <-c.kick:
		case <-c.ctx.Done():
			return
		}
	}
}

type balancedUser struct{ c *Client }

func (u balancedUser) UserData(data []byte) bool {
	u.c.receive(data)
	return true
}

func (u balancedUser) Reset(bool) {}

// Busy sets DFC, and refuses user data, while the handler queue is more
// than half full (IEC 60870-5-101:2003 figure 9, "secondary link busy").
func (u balancedUser) Busy() bool { return len(u.c.handled) > cap(u.c.handled)/2 }

// Interrogate runs an interrogation (C_IC_NA_1) with qualifier qoi
// ([asdu.QOIStation] or [asdu.QOIGroup]) and returns the data the station
// sent with the matching cause until the activation termination.
//
// More ASDUs than Config.MaxResult end it with an error wrapping
// [telecontrol.ErrResultLimit]. Called from the Handler or OnState it
// returns [iec101.ErrInHandler].
func (c *Client) Interrogate(ctx context.Context, ca asdu.CommonAddr, qoi asdu.QOI) ([]*asdu.ASDU, error) {
	if err := c.inHandler("interrogation"); err != nil {
		return nil, err
	}
	return c.req.Interrogate(ctx, ca, qoi)
}

// CounterInterrogate runs a counter interrogation (C_CI_NA_1), bounded by
// Config.MaxResult as Interrogate is.
func (c *Client) CounterInterrogate(ctx context.Context, ca asdu.CommonAddr, request, freeze uint8) ([]*asdu.ASDU, error) {
	if err := c.inHandler("counter interrogation"); err != nil {
		return nil, err
	}
	return c.req.CounterInterrogate(ctx, ca, request, freeze)
}

// Read reads one information object (C_RD_NA_1).
func (c *Client) Read(ctx context.Context, ca asdu.CommonAddr, ioa asdu.IOA) (*asdu.ASDU, error) {
	if err := c.inHandler("read"); err != nil {
		return nil, err
	}
	return c.req.Read(ctx, ca, ioa)
}

// Command sends a process command and waits for its activation
// confirmation. For select-before-operate call it twice, first with the
// object's Select set. A refusal is a [*telecontrol.NegativeError].
func (c *Client) Command(ctx context.Context, ca asdu.CommonAddr, obj asdu.InformationObject) error {
	if err := c.inHandler("command"); err != nil {
		return err
	}
	return c.req.Command(ctx, ca, obj, false)
}

// Deactivate cancels a selected command (cause deactivation).
func (c *Client) Deactivate(ctx context.Context, ca asdu.CommonAddr, obj asdu.InformationObject) error {
	if err := c.inHandler("deactivation"); err != nil {
		return err
	}
	return c.req.Command(ctx, ca, obj, true)
}

// ClockSync synchronizes the station's clock (C_CS_NA_1).
func (c *Client) ClockSync(ctx context.Context, ca asdu.CommonAddr, t time.Time) error {
	if err := c.inHandler("clock synchronization"); err != nil {
		return err
	}
	return c.req.ClockSync(ctx, ca, t)
}

// TestCommand sends C_TS_NA_1 with the fixed test bit pattern.
func (c *Client) TestCommand(ctx context.Context, ca asdu.CommonAddr) error {
	if err := c.inHandler("test command"); err != nil {
		return err
	}
	return c.req.TestCommand(ctx, ca, false)
}

// ResetProcess sends a reset process command (C_RP_NA_1).
func (c *Client) ResetProcess(ctx context.Context, ca asdu.CommonAddr, qualifier uint8) error {
	if err := c.inHandler("reset process"); err != nil {
		return err
	}
	return c.req.ResetProcess(ctx, ca, qualifier)
}

// SetDelay sends the transmission delay to the station (C_CD_NA_1,
// cause spontaneous), which corrects following clock synchronizations by
// it. See [telecontrol.Requester.SetDelay].
func (c *Client) SetDelay(ctx context.Context, ca asdu.CommonAddr, d time.Duration) error {
	return c.req.SetDelay(ctx, ca, d)
}

// MeasureDelay measures the transmission delay with the delay acquisition
// procedure (C_CD_NA_1, cause activation). See
// [telecontrol.Requester.MeasureDelay].
func (c *Client) MeasureDelay(ctx context.Context, ca asdu.CommonAddr) (time.Duration, error) {
	if err := c.inHandler("delay acquisition"); err != nil {
		return 0, err
	}
	return c.req.MeasureDelay(ctx, ca)
}
