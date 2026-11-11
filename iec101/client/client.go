// SPDX-License-Identifier: MIT

// Package client is the controlling station of IEC 60870-5-101: it brings
// up the link, polls the controlled station (unbalanced transmission) or
// exchanges messages with it (balanced transmission), runs requests
// (interrogation, counter interrogation, read, commands, clock
// synchronization, test, reset process, delay acquisition) and delivers
// monitor-direction ASDUs to a handler.
//
// The link is initialized in the background and re-initialized when the
// controlled station stops answering; [Client.WaitUp] waits for it.
// Requests fail with [iec101.ErrLinkDown] while the link is down.
package client

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

// Defaults of [Config].
const (
	DefaultPollInterval   = 100 * time.Millisecond
	DefaultRequestTimeout = 10 * time.Second
	DefaultRetryInterval  = 2 * time.Second
	DefaultTestInterval   = 10 * time.Second
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

	// Handler receives every monitor-direction ASDU that is not a mirror of
	// a request: spontaneous and cyclic data, and the data that answers an
	// interrogation or read (also returned by those calls). It runs on a
	// goroutine of its own, in order; it may issue requests.
	Handler func(a *asdu.ASDU)
	// OnState is called with each change of the link state.
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

	sends   chan sendReq // unbalanced: user data for the poller
	handled chan *asdu.ASDU

	mu    sync.Mutex
	state iec101.State
	upCh  chan struct{} // closed while the link is up
	kick  chan struct{} // the link went down: re-initialize now

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

type sendReq struct {
	data []byte
	done chan error
}

// New starts a controlling station on rw. Close stops it and closes rw
// when it is an io.Closer.
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
	c := &Client{
		cfg:     cfg,
		log:     logx.New(cfg.Logger, "iec101-client", "link", cfg.LinkAddr),
		sends:   make(chan sendReq),
		handled: make(chan *asdu.ASDU, 256),
		upCh:    make(chan struct{}),
		kick:    make(chan struct{}, 1),
	}
	c.req = telecontrol.Requester{Params: p.ASDU, Send: c.send, Timeout: cfg.RequestTimeout}
	c.ctx, c.cancel = context.WithCancel(context.Background())
	if p.Mode == iec101.ModeBalanced {
		c.bal, err = link.NewBalanced(rw, p.Link,
			link.BalancedConfig{Addr: cfg.OwnAddr, Remote: cfg.LinkAddr, DIR: true}, balancedUser{c})
		if err != nil {
			return nil, fmt.Errorf("%w: %w", iec101.ErrInvalidConfig, err)
		}
	} else {
		c.pri, err = link.NewPrimary(rw, p.Link)
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

// Err returns the transport error that stopped the station, nil while it
// runs or after Close.
func (c *Client) Err() error {
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

// WaitUp waits until the link is initialized.
func (c *Client) WaitUp(ctx context.Context) error {
	for {
		c.mu.Lock()
		st, ch := c.state, c.upCh
		c.mu.Unlock()
		switch st {
		case iec101.StateUp:
			return nil
		case iec101.StateClosed:
			return iec101.ErrClosed
		case iec101.StateDown:
		}
		select {
		case <-ch:
		case <-ctx.Done():
			return fmt.Errorf("iec101: waiting for link: %w", ctx.Err())
		case <-c.ctx.Done():
			return iec101.ErrClosed
		}
	}
}

// Close stops the station. Outstanding requests fail with
// [iec101.ErrClosed].
func (c *Client) Close() error {
	c.mu.Lock()
	if c.state == iec101.StateClosed {
		c.mu.Unlock()
		return nil
	}
	c.mu.Unlock()
	c.cancel()
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

func (c *Client) setState(s iec101.State, err error) {
	c.mu.Lock()
	if c.state == s || c.state == iec101.StateClosed {
		c.mu.Unlock()
		return
	}
	c.state = s
	if s == iec101.StateUp {
		close(c.upCh)
	} else {
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
	if s == iec101.StateDown {
		c.req.FailAll(fmt.Errorf("%w: %w", iec101.ErrLinkDown, err))
	}
	c.log.Info("link state", "state", s.String(), "err", err)
	if c.cfg.OnState != nil {
		c.cfg.OnState(s, err)
	}
}

// receive decodes user data from the controlled station and routes it.
func (c *Client) receive(data []byte) {
	a, err := asdu.Decode(data, c.cfg.ASDU)
	if err != nil {
		c.log.Warn("undecodable ASDU dropped", "err", err)
		return
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
	for {
		select {
		case a := <-c.handled:
			if c.cfg.Handler != nil {
				c.cfg.Handler(a)
			}
		case <-c.ctx.Done():
			return
		}
	}
}

// send transmits a control-direction ASDU and returns when the link layer
// confirmed it.
func (c *Client) send(ctx context.Context, a *asdu.ASDU) error {
	if c.State() != iec101.StateUp {
		return iec101.ErrLinkDown
	}
	raw, err := a.Encode(c.cfg.ASDU)
	if err != nil {
		return err
	}
	if c.bal != nil {
		if err := c.bal.Send(ctx, raw); err != nil {
			c.linkFailed(err)
			return err
		}
		return nil
	}
	r := sendReq{data: raw, done: make(chan error, 1)}
	select {
	case c.sends <- r:
	case <-ctx.Done():
		return ctx.Err()
	case <-c.ctx.Done():
		return iec101.ErrClosed
	}
	select {
	case err := <-r.done:
		return err
	case <-c.ctx.Done():
		return iec101.ErrClosed
	}
}

// Send transmits a in the control direction without waiting for an
// application answer. Answers reach the Handler.
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
	addr := c.cfg.LinkAddr
	var acd, dfc bool
	for c.ctx.Err() == nil {
		if c.State() != iec101.StateUp {
			st, err := c.pri.Init(c.ctx, addr)
			if err != nil {
				c.log.Debug("link init failed", "err", err)
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

func (c *Client) runBalanced() {
	defer c.wg.Done()
	for c.ctx.Err() == nil {
		if c.State() != iec101.StateUp {
			if err := c.bal.Init(c.ctx); err != nil {
				c.log.Debug("link init failed", "err", err)
				if !c.sleep(c.cfg.RetryInterval) {
					return
				}
				continue
			}
			c.setState(iec101.StateUp, nil)
		}
		if c.cfg.TestInterval < 0 {
			if !c.sleep(c.cfg.RetryInterval) {
				return
			}
			continue
		}
		if !c.sleep(c.cfg.TestInterval) {
			return
		}
		if err := c.bal.Test(c.ctx); err != nil {
			c.linkFailed(err)
		}
	}
}

type balancedUser struct{ c *Client }

func (u balancedUser) UserData(data []byte) bool {
	u.c.receive(data)
	return true
}

func (u balancedUser) Reset(bool) {}

// Interrogate runs an interrogation (C_IC_NA_1) with qualifier qoi
// ([asdu.QOIStation] or [asdu.QOIGroup]) and returns the data the station
// sent with the matching cause until the activation termination.
func (c *Client) Interrogate(ctx context.Context, ca asdu.CommonAddr, qoi asdu.QOI) ([]*asdu.ASDU, error) {
	return c.req.Interrogate(ctx, ca, qoi)
}

// CounterInterrogate runs a counter interrogation (C_CI_NA_1).
func (c *Client) CounterInterrogate(ctx context.Context, ca asdu.CommonAddr, request, freeze uint8) ([]*asdu.ASDU, error) {
	return c.req.CounterInterrogate(ctx, ca, request, freeze)
}

// Read reads one information object (C_RD_NA_1).
func (c *Client) Read(ctx context.Context, ca asdu.CommonAddr, ioa asdu.IOA) (*asdu.ASDU, error) {
	return c.req.Read(ctx, ca, ioa)
}

// Command sends a process command and waits for its activation
// confirmation. For select-before-operate call it twice, first with the
// object's Select set. A refusal is a [*telecontrol.NegativeError].
func (c *Client) Command(ctx context.Context, ca asdu.CommonAddr, obj asdu.InformationObject) error {
	return c.req.Command(ctx, ca, obj, false)
}

// Deactivate cancels a selected command (cause deactivation).
func (c *Client) Deactivate(ctx context.Context, ca asdu.CommonAddr, obj asdu.InformationObject) error {
	return c.req.Command(ctx, ca, obj, true)
}

// ClockSync synchronizes the station's clock (C_CS_NA_1).
func (c *Client) ClockSync(ctx context.Context, ca asdu.CommonAddr, t time.Time) error {
	return c.req.ClockSync(ctx, ca, t)
}

// TestCommand sends C_TS_NA_1 with the fixed test bit pattern.
func (c *Client) TestCommand(ctx context.Context, ca asdu.CommonAddr) error {
	return c.req.TestCommand(ctx, ca, false)
}

// ResetProcess sends a reset process command (C_RP_NA_1).
func (c *Client) ResetProcess(ctx context.Context, ca asdu.CommonAddr, qualifier uint8) error {
	return c.req.ResetProcess(ctx, ca, qualifier)
}

// DelayAcquisition sends a delay acquisition command (C_CD_NA_1).
func (c *Client) DelayAcquisition(ctx context.Context, ca asdu.CommonAddr, d time.Duration) error {
	return c.req.DelayAcquisition(ctx, ca, d)
}
