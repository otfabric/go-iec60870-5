// SPDX-License-Identifier: MIT

// Package client is the controlling system of IEC 60870-5-103: the primary
// station of unbalanced transmission (clause 6.2.3) that polls one item of
// protection equipment, runs the application functions of clause 7.4
// (station initialization, time synchronization, general interrogation,
// general commands, generic services, disturbance data transfer) and
// delivers every monitor-direction ASDU to a handler.
//
// The link is initialized in the background (request status of link,
// then reset CU or reset FCB, clause 7.4.1) and again when the protection
// equipment stops answering; [Client.WaitUp] waits for it. Requests fail
// with [iec103.ErrLinkDown] while the link is down.
//
// One client serves one item of protection equipment on a transport of
// its own; one primary polling several items on a shared line is not
// supported yet.
package client

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

// Defaults of [Config].
const (
	DefaultPollInterval   = 100 * time.Millisecond
	DefaultRequestTimeout = 10 * time.Second
	DefaultRetryInterval  = 2 * time.Second
)

// BroadcastAddr is the link address of send/no reply to all stations
// (clause 6.2.3).
const BroadcastAddr = 255

// Config configures a client.
type Config struct {
	// Link holds the link timing. The address field is always one octet
	// (clause 6.2.3); Link.FT12 is set accordingly.
	Link link.Config

	// LinkAddr is the link address of the protection equipment, 0..254.
	LinkAddr uint8
	// CommonAddr is the common address of ASDU of the protection
	// equipment. Zero means LinkAddr, which clause 7.2.4 makes the normal
	// case and the system functions require.
	CommonAddr uint8

	// ResetFCB initializes the link with reset FCB (function 7) instead of
	// reset CU (function 0); clause 7.4.1 leaves the choice to the control
	// system. Reset CU also clears the transmit buffer of the equipment.
	ResetFCB bool
	// NoInitTimeSync and NoInitGI leave out the time synchronization and
	// the general interrogation that follow the initialization (clause
	// 7.4.1, figure 3; clause 7.4.3).
	NoInitTimeSync bool
	NoInitGI       bool

	// PollInterval is the pause between class 2 polls when the equipment
	// has nothing to send. Zero means [DefaultPollInterval].
	PollInterval time.Duration
	// RequestTimeout bounds every request whose context has no deadline.
	// Zero means [DefaultRequestTimeout]. A disturbance data transfer
	// usually needs a longer context.
	RequestTimeout time.Duration
	// RetryInterval is the pause before the link is initialized again
	// after a failure. Zero means [DefaultRetryInterval].
	RetryInterval time.Duration

	// Location is the time zone of the time tags; nil means UTC.
	Location *time.Location

	// Handler receives every monitor-direction ASDU, the answers to
	// requests included (they are also returned by the request). It runs
	// on a goroutine of its own, in order; it may issue requests.
	Handler func(a *iec103.ASDU)
	// OnState is called with each change of the link state.
	OnState func(s iec103.State, err error)
	// Logger receives the log entries; nil is silent.
	Logger iec60870.Logger
}

// Client is a controlling system.
type Client struct {
	cfg Config
	ca  uint8
	log *logx.Logger
	pri *link.Primary

	sends   chan sendReq
	bcasts  chan sendReq // broadcasts: sent whatever DFC says (clause 6.3)
	handled chan *iec103.ASDU

	mu      sync.Mutex
	state   iec103.State
	upCh    chan struct{} // closed while the link is up
	downCh  chan struct{} // closed when the link goes down after being up
	kick    chan struct{}
	ident   *iec103.ASDU
	waiters map[*waiter]struct{}
	counter uint8 // RII and SCN

	cmdMu, giMu, readMu, writeMu, ggiMu, distMu sync.Mutex

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

type sendReq struct {
	data []byte
	// stamp, when set, encodes the ASDU right before it is transmitted:
	// the time synchronization carries the time of transmission (clause
	// 7.4.2).
	stamp func() []byte
	done  chan error
}

// New starts a controlling system on rw. Close stops it and closes rw when
// it is an io.Closer.
func New(rw io.ReadWriter, cfg Config) (*Client, error) {
	if cfg.LinkAddr == BroadcastAddr {
		return nil, fmt.Errorf("%w: link address 255 is the broadcast address", iec103.ErrInvalidConfig)
	}
	cfg.Link.FT12 = ft12.Params{AddrSize: 1}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = DefaultPollInterval
	}
	if cfg.RequestTimeout <= 0 {
		cfg.RequestTimeout = DefaultRequestTimeout
	}
	if cfg.RetryInterval <= 0 {
		cfg.RetryInterval = DefaultRetryInterval
	}
	pri, err := link.NewPrimary(rw, cfg.Link)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", iec103.ErrInvalidConfig, err)
	}
	c := &Client{
		cfg:     cfg,
		ca:      cfg.CommonAddr,
		log:     logx.New(cfg.Logger, "iec103-client", "link", cfg.LinkAddr),
		pri:     pri,
		sends:   make(chan sendReq),
		bcasts:  make(chan sendReq),
		handled: make(chan *iec103.ASDU, 256),
		upCh:    make(chan struct{}),
		downCh:  make(chan struct{}),
		kick:    make(chan struct{}, 1),
		waiters: map[*waiter]struct{}{},
	}
	if c.ca == 0 {
		c.ca = cfg.LinkAddr
	}
	c.ctx, c.cancel = context.WithCancel(context.Background())
	c.wg.Add(2)
	go c.deliver()
	go c.run()
	return c, nil
}

// State returns the link state.
func (c *Client) State() iec103.State {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state
}

// CommonAddr returns the common address of ASDU the client uses.
func (c *Client) CommonAddr() uint8 { return c.ca }

// WaitUp waits until the link is initialized.
func (c *Client) WaitUp(ctx context.Context) error {
	for {
		c.mu.Lock()
		st, ch := c.state, c.upCh
		c.mu.Unlock()
		switch st {
		case iec103.StateUp:
			return nil
		case iec103.StateClosed:
			return iec103.ErrClosed
		case iec103.StateDown:
		}
		select {
		case <-ch:
		case <-ctx.Done():
			return fmt.Errorf("iec103: waiting for link: %w", ctx.Err())
		case <-c.ctx.Done():
			return iec103.ErrClosed
		}
	}
}

// Identification returns the last identification (ASDU 5) the protection
// equipment sent, after a reset or a start/restart (clause 7.4.1), or nil.
func (c *Client) Identification() *iec103.ASDU {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ident
}

// Close stops the station. Outstanding requests fail with
// [iec103.ErrClosed].
func (c *Client) Close() error {
	c.mu.Lock()
	if c.state == iec103.StateClosed {
		c.mu.Unlock()
		return nil
	}
	c.mu.Unlock()
	c.cancel()
	err := c.pri.Close()
	c.wg.Wait()
	c.setState(iec103.StateClosed, nil)
	return err
}

func (c *Client) setState(s iec103.State, err error) {
	c.mu.Lock()
	if c.state == s || c.state == iec103.StateClosed {
		c.mu.Unlock()
		return
	}
	prev := c.state
	c.state = s
	if s == iec103.StateUp {
		close(c.upCh)
		c.downCh = make(chan struct{})
	} else {
		select {
		case <-c.upCh:
			c.upCh = make(chan struct{})
		default:
		}
		if prev == iec103.StateUp {
			close(c.downCh)
		}
		select {
		case c.kick <- struct{}{}:
		default:
		}
	}
	c.mu.Unlock()
	c.log.Info("link state", "state", s.String(), "err", err)
	if c.cfg.OnState != nil {
		c.cfg.OnState(s, err)
	}
}

func (c *Client) next() uint8 {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.counter++
	return c.counter
}

// receive decodes user data from the protection equipment and routes it.
func (c *Client) receive(data []byte) {
	a, err := iec103.DecodeIn(data, c.cfg.Location)
	if err != nil {
		c.log.Warn("undecodable ASDU dropped", "err", err)
		return
	}
	c.mu.Lock()
	if a.Type == iec103.TypeIdentification {
		c.ident = a
	}
	for w := range c.waiters {
		if w.match(a) {
			select {
			case w.ch <- a:
			default:
				w.overflow = true
			}
		}
	}
	c.mu.Unlock()
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

// Send transmits a control-direction ASDU with send/confirm and returns
// when the link layer confirmed it. Answers reach the Handler.
func (c *Client) Send(ctx context.Context, a *iec103.ASDU) error {
	raw, err := a.AppendIn(nil, c.cfg.Location)
	if err != nil {
		return err
	}
	return c.send(ctx, raw)
}

// Broadcast transmits a control-direction ASDU with send/no reply to link
// address 255 (clause 6.2.3), between two exchanges of the link, also
// while the equipment sets DFC (clause 6.3). Nothing is confirmed.
func (c *Client) Broadcast(a *iec103.ASDU) error {
	raw, err := a.AppendIn(nil, c.cfg.Location)
	if err != nil {
		return err
	}
	return c.broadcast(sendReq{data: raw})
}

// up returns the channel closed when the link goes down, or ErrLinkDown
// (ErrClosed) when it is not up.
func (c *Client) up() (<-chan struct{}, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch c.state {
	case iec103.StateUp:
		return c.downCh, nil
	case iec103.StateClosed:
		return nil, iec103.ErrClosed
	}
	return nil, iec103.ErrLinkDown
}

func (c *Client) broadcast(r sendReq) error {
	down, err := c.up()
	if err != nil {
		return err
	}
	r.done = make(chan error, 1)
	// The link loop takes broadcasts only while the link is up: one that
	// goes down before it took this one ends the call, which has no
	// context to bound it.
	select {
	case c.bcasts <- r:
	case <-down:
		return iec103.ErrLinkDown
	case <-c.ctx.Done():
		return iec103.ErrClosed
	}
	select {
	case err := <-r.done:
		return err
	case <-c.ctx.Done():
		return iec103.ErrClosed
	}
}

func (c *Client) send(ctx context.Context, raw []byte) error {
	return c.submit(ctx, sendReq{data: raw})
}

// submit hands r to the link loop and waits for the confirmation.
func (c *Client) submit(ctx context.Context, r sendReq) error {
	down, err := c.up()
	if err != nil {
		return err
	}
	r.done = make(chan error, 1)
	select {
	case c.sends <- r:
	case <-down:
		return iec103.ErrLinkDown
	case <-ctx.Done():
		return ctx.Err()
	case <-c.ctx.Done():
		return iec103.ErrClosed
	}
	// The link loop finishes the exchange it started (a primary repeats a
	// message until it is answered or Link.Retries ran out), but the
	// request ends at its deadline.
	select {
	case err := <-r.done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-c.ctx.Done():
		return iec103.ErrClosed
	}
}

// transmit sends r with send/confirm, or with send/no reply to the
// broadcast address when broadcast is set, encoding a stamped ASDU just
// before.
func (c *Client) transmit(r sendReq, broadcast bool) (link.Status, error) {
	if r.stamp != nil {
		r.data = r.stamp()
	}
	if broadcast {
		err := c.pri.SendNoReply(BroadcastAddr, r.data)
		r.done <- err
		return link.Status{}, err
	}
	st, err := c.pri.SendConfirm(c.ctx, uint16(c.cfg.LinkAddr), r.data)
	r.done <- err
	return st, err
}

func (c *Client) linkFailed(err error) {
	if errors.Is(err, link.ErrTimeout) || errors.Is(err, link.ErrClosed) {
		c.setState(iec103.StateDown, err)
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

// initLink runs the link part of clause 7.4.1: request status of link,
// then reset CU or reset FCB.
func (c *Client) initLink() (link.Status, error) {
	addr := uint16(c.cfg.LinkAddr)
	if _, err := c.pri.RequestStatus(c.ctx, addr); err != nil {
		return link.Status{}, err
	}
	if c.cfg.ResetFCB {
		return c.pri.ResetFCB(c.ctx, addr)
	}
	return c.pri.ResetLink(c.ctx, addr)
}

func (c *Client) run() {
	defer c.wg.Done()
	addr := uint16(c.cfg.LinkAddr)
	var acd, dfc bool
	for c.ctx.Err() == nil {
		if c.State() != iec103.StateUp {
			st, err := c.initLink()
			if err != nil {
				c.log.Debug("link init failed", "err", err)
				if !c.sleep(c.cfg.RetryInterval) {
					return
				}
				continue
			}
			acd, dfc = st.ACD, st.DFC
			c.setState(iec103.StateUp, nil)
			c.wg.Add(1)
			go c.afterInit()
		}
		// Broadcasts go out whatever DFC says (clause 6.3).
		select {
		case r := <-c.bcasts:
			_, _ = c.transmit(r, true)
			continue
		default:
		}
		// User data first, unless the equipment holds it back with DFC
		// (clause 6.3): it stays queued until DFC clears.
		if !dfc {
			select {
			case r := <-c.sends:
				st, err := c.transmit(r, false)
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
		sends := c.sends
		if dfc {
			sends = nil
		}
		t := time.NewTimer(c.cfg.PollInterval)
		select {
		case r := <-c.bcasts:
			t.Stop()
			_, _ = c.transmit(r, true)
		case r := <-sends:
			t.Stop()
			st, err := c.transmit(r, false)
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

// afterInit runs the application part of clause 7.4.1: time
// synchronization and general interrogation.
func (c *Client) afterInit() {
	defer c.wg.Done()
	if !c.cfg.NoInitTimeSync {
		if _, err := c.ClockSync(c.ctx, time.Now()); err != nil {
			c.log.Warn("time synchronization after initialization failed", "err", err)
		}
	}
	if !c.cfg.NoInitGI {
		if _, err := c.Interrogate(c.ctx); err != nil {
			c.log.Warn("general interrogation after initialization failed", "err", err)
		}
	}
}

// waiter collects the ASDUs a request is waiting for.
type waiter struct {
	match    func(*iec103.ASDU) bool
	ch       chan *iec103.ASDU
	down     <-chan struct{}
	overflow bool
}

// errOverflow reports a waiter that missed answers.
var errOverflow = errors.New("iec103: answers arrived faster than they were taken")

func (c *Client) watch(match func(*iec103.ASDU) bool) (*waiter, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch c.state {
	case iec103.StateClosed:
		return nil, iec103.ErrClosed
	case iec103.StateDown:
		return nil, iec103.ErrLinkDown
	case iec103.StateUp:
	}
	w := &waiter{match: match, ch: make(chan *iec103.ASDU, 1024), down: c.downCh}
	c.waiters[w] = struct{}{}
	return w, nil
}

func (c *Client) unwatch(w *waiter) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.waiters, w)
}

// wait returns the next ASDU of w.
func (c *Client) wait(ctx context.Context, w *waiter) (*iec103.ASDU, error) {
	select {
	case a := <-w.ch:
		return a, nil
	default:
	}
	c.mu.Lock()
	over := w.overflow
	c.mu.Unlock()
	if over {
		return nil, errOverflow
	}
	select {
	case a := <-w.ch:
		return a, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-w.down:
		return nil, iec103.ErrLinkDown
	case <-c.ctx.Done():
		return nil, iec103.ErrClosed
	}
}

// bound applies the request timeout to a context without deadline.
func (c *Client) bound(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, c.cfg.RequestTimeout)
}

// request registers a waiter, sends a and returns the waiter.
func (c *Client) request(ctx context.Context, a *iec103.ASDU, match func(*iec103.ASDU) bool) (*waiter, error) {
	raw, err := a.AppendIn(nil, c.cfg.Location)
	if err != nil {
		return nil, err
	}
	w, err := c.watch(match)
	if err != nil {
		return nil, err
	}
	if err := c.send(ctx, raw); err != nil {
		c.unwatch(w)
		return nil, err
	}
	return w, nil
}

func lock(m *sync.Mutex) error {
	if !m.TryLock() {
		return iec103.ErrBusy
	}
	return nil
}

// Interrogate runs a general interrogation (clause 7.4.3): ASDU 7 with a
// new scan number, then collects the answers with cause 9 until the
// termination (ASDU 8) with the same scan number. Time-tagged messages
// whose SIN is not the scan number are left out: clause 7.4.3 has the
// control system discard GI answers of an earlier cycle. Generic data
// (ASDU 10, the general interrogation of generic data) is not part of it.
func (c *Client) Interrogate(ctx context.Context) ([]*iec103.ASDU, error) {
	if err := lock(&c.giMu); err != nil {
		return nil, err
	}
	defer c.giMu.Unlock()
	ctx, cancel := c.bound(ctx)
	defer cancel()
	scn := c.next()
	w, err := c.request(ctx, iec103.New(iec103.CauseGI, c.ca, iec103.FunGlobal, iec103.InfGIEnd, iec103.GIInit{SCN: scn}),
		func(a *iec103.ASDU) bool {
			return a.CommonAddr == c.ca && (a.Type == iec103.TypeGITermination || a.Cause == iec103.CauseGI && a.Type != iec103.TypeGenericData && a.Type != iec103.TypeGenericIdent)
		})
	if err != nil {
		return nil, err
	}
	defer c.unwatch(w)
	var out []*iec103.ASDU
	for {
		a, err := c.wait(ctx, w)
		if err != nil {
			return out, fmt.Errorf("iec103: general interrogation: %w", err)
		}
		switch b := a.Body.(type) {
		case iec103.GITermination:
			if b.SCN == scn {
				return out, nil
			}
			continue
		case iec103.TimeTagged:
			if b.SIN != scn {
				continue
			}
		case iec103.RelTimeTagged:
			if b.SIN != scn {
				continue
			}
		}
		out = append(out, a)
	}
}

// syncReq returns the request of a time synchronization to common address
// ca for clock t: the ASDU 6 is encoded when it is transmitted, with t
// advanced by the time it waited for the link, so that it carries the
// time of its transmission (clause 7.4.2).
func (c *Client) syncReq(ca uint8, t time.Time) sendReq {
	offset := time.Until(t)
	return sendReq{stamp: func() []byte {
		a := iec103.New(iec103.CauseTimeSync, ca, iec103.FunGlobal, iec103.InfTimeSync, iec103.TimeSync{Time: cp.At(time.Now().Add(offset))})
		raw, _ := a.AppendIn(nil, c.cfg.Location) // a TimeSync always encodes
		return raw
	}}
}

// ClockSync synchronizes the clock of the protection equipment with an
// addressed ASDU 6 (send/confirm, clause 7.4.2) and returns the time of
// the ASDU 6 it answers with: the time of the command, corrected by the
// equipment for transmission and processing. The command carries t
// advanced by the time it waited for the link, the time of its
// transmission.
func (c *Client) ClockSync(ctx context.Context, t time.Time) (cp.Timestamp, error) {
	ctx, cancel := c.bound(ctx)
	defer cancel()
	w, err := c.watch(func(a *iec103.ASDU) bool {
		return a.CommonAddr == c.ca && a.Type == iec103.TypeTimeSync && a.Cause == iec103.CauseTimeSync
	})
	if err != nil {
		return cp.Timestamp{}, err
	}
	defer c.unwatch(w)
	if err := c.submit(ctx, c.syncReq(c.ca, t)); err != nil {
		return cp.Timestamp{}, err
	}
	a, err := c.wait(ctx, w)
	if err != nil {
		return cp.Timestamp{}, fmt.Errorf("iec103: time synchronization: %w", err)
	}
	return a.Body.(iec103.TimeSync).Time, nil
}

// BroadcastClockSync sends ASDU 6 with the global common address to link
// address 255 (send/no reply, clause 7.4.2), with t advanced by the time
// it waited for the link like [Client.ClockSync]. Equipment that answers a
// broadcast synchronization with ASDU 6 delivers it to the Handler.
func (c *Client) BroadcastClockSync(t time.Time) error {
	return c.broadcast(c.syncReq(iec103.CommonAddrGlobal, t))
}

// Command sends a general command (ASDU 20, clause 7.4.4) and waits for its
// acknowledgement: ASDU 1 with the same FUN and INF, cause 20 or 21 and the
// command's RII as SIN. A negative acknowledgement returns an error
// wrapping [iec103.ErrNegative]. The change of state the command causes
// follows with cause 12 and reaches the Handler. Clause 7.4.4 allows one
// command procedure at a time: a second concurrent call returns
// [iec103.ErrBusy]. A command that [iec103.CommandAllowed] refuses (DCO 0
// or 3, OFF where table 17 defines only ON, or an INF outside table 17
// with a compatible function type) returns [iec103.ErrInvalid].
func (c *Client) Command(ctx context.Context, fun iec103.FunctionType, inf iec103.InfoNumber, dco iec103.DPI) error {
	if !iec103.CommandAllowed(fun, inf, dco) {
		return fmt.Errorf("%w: DCO %d for FUN %d INF %d (clause 7.2.6.4, table 17)", iec103.ErrInvalid, dco, fun, inf)
	}
	if err := lock(&c.cmdMu); err != nil {
		return err
	}
	defer c.cmdMu.Unlock()
	ctx, cancel := c.bound(ctx)
	defer cancel()
	rii := c.next()
	w, err := c.request(ctx, iec103.New(iec103.CauseGeneralCommand, c.ca, fun, inf, iec103.GeneralCommand{DCO: dco, RII: rii}),
		func(a *iec103.ASDU) bool {
			tt, ok := a.Body.(iec103.TimeTagged)
			return ok && a.CommonAddr == c.ca && a.Fun == fun && a.Inf == inf && tt.SIN == rii &&
				(a.Cause == iec103.CauseCommandAck || a.Cause == iec103.CauseCommandNack)
		})
	if err != nil {
		return err
	}
	defer c.unwatch(w)
	a, err := c.wait(ctx, w)
	if err != nil {
		return fmt.Errorf("iec103: command FUN %d INF %d: %w", fun, inf, err)
	}
	if a.Cause == iec103.CauseCommandNack {
		return fmt.Errorf("%w: command FUN %d INF %d", iec103.ErrNegative, fun, inf)
	}
	return nil
}
