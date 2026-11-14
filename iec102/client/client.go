// SPDX-License-Identifier: MIT

// Package client is the controlling station of IEC 60870-5-102: the
// primary station of unbalanced transmission that reads integrated totals,
// single-point information, the product specification and the system time
// of one integrated total station (a meter or a data concentrator).
//
// IEC 60870-5-102 is request driven: the client polls only while a request
// is open. A request is sent with send/confirm (function 3); the client
// then polls for user data, class 1 while the station sets ACD and class 2
// otherwise, until the answer is complete:
//
//   - [UntilReply]: the first ASDU of the station that is not the mirror of
//     the request (C_RD_NA_2 is answered by P_MP_NA_2, C_TI_NA_2 by
//     M_TI_TA_2);
//   - [UntilConfirmation]: the activation confirmation, the request
//     mirrored with cause 7;
//   - [UntilTermination]: the activation termination, the request mirrored
//     with cause 10, after the confirmation and the data (cause 5).
//
// A mirror with the P/N bit or a cause 13..18 ends any request with an
// [*iec102.RefusedError]. A mirror is an ASDU of the station with the type
// identification, common address and record address of the request
// ([iec102.ASDU.Mirrors]) and its information object too: the requested
// addresses and time range. These procedures are those of the recorded
// traffic (innoWatt log: C_AC_NA_2 confirmed with cause 7; C_CI_NU_2
// confirmed, 744 ASDUs M_IT_TK_2 with cause 5, terminated with cause 10)
// and of gisce/iec870ree; the Beckhoff TS650x manual describes no
// application procedure. They are interpretations; see
// docs/iec102/INTEROPERABILITY.md.
//
// The link is initialized (request status of link, reset of remote link)
// by [Client.Init] or the first request, and again after the station
// stopped answering (link.ErrTimeout, which link.ErrUnexpected also wraps
// when only answers to something else came until the last time-out).
// Requests run one at a time; a second waits. A request that ends before
// its answer is complete (its context ended, or the link failed) leaves the
// rest of that answer at the station: the next request first polls the
// station until it has no more data and drops what of it belongs to its
// common address, so that it is not taken for the next answer.
package client

import (
	"bytes"
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

// Defaults of [Config].
const (
	DefaultPollInterval   = 50 * time.Millisecond
	DefaultRequestTimeout = 30 * time.Second
	// DefaultMaxResult is the default of Config.MaxResult: 131072 ASDUs
	// hold more than three years of quarter-hour integration periods, one
	// period to an ASDU.
	DefaultMaxResult = 1 << 17
)

// Config configures a client.
type Config struct {
	// Link holds the link timing (Timeout, Retries) and hooks; Link.FT12
	// is set from LinkAddrSize.
	Link link.Config
	// LinkAddrSize is the size of the link address field: 1 or 2 octets,
	// [iec102.NoLinkAddr] for none. Zero means
	// [iec102.DefaultLinkAddrSize].
	LinkAddrSize int
	// LinkAddr is the link address of the integrated total station.
	LinkAddr uint16
	// CommonAddr is the common address of ASDU (the REE profile's
	// measuring point address) the requests carry.
	CommonAddr uint16
	// Params are the codec parameters: common address size, time zone and
	// profile (for example ree.Profile).
	Params iec102.Params

	// PollInterval is the pause between polls the station answers without
	// data while a request is open. Zero means [DefaultPollInterval].
	PollInterval time.Duration
	// RequestTimeout bounds every request whose context has no deadline.
	// Zero means [DefaultRequestTimeout]; long load profiles need more.
	RequestTimeout time.Duration
	// MaxResult bounds the ASDUs one request collects: the request that
	// would collect more ends with an error wrapping
	// [iec102.ErrResultLimit], with the ASDUs so far, instead of growing
	// until its deadline. Zero means [DefaultMaxResult]; negative means no
	// bound, as before v0.6.0.
	MaxResult int

	// Handler receives the ASDUs that answer no request: those of another
	// common address. It runs on a goroutine of its own, in order, through
	// a queue of 64 ASDUs; one that does not fit is dropped and logged. It
	// may call the client's methods: a request from it waits for the
	// request in progress like any other. It may call Close, which then
	// does not wait for the Handler's goroutine: it returns at once, the
	// Handler is not called again, and the close completes when the
	// Handler has returned. A Handler that does not return normally is
	// logged with its stack and closes the client; Err reports it. The
	// ASDU is the Handler's own: the client keeps no reference to it.
	Handler func(a *iec102.ASDU)
	// OnState is called with each change of the link state, from the
	// goroutine of the request that changed it (while that request holds
	// the request slot), or from Close for StateClosed. It must return
	// quickly. It must not issue a request, which waits for the request
	// slot the calling request holds; start one in a new goroutine. It may
	// call Close; the request then fails with [iec102.ErrClosed]. An
	// OnState that does not return normally is logged and closes the
	// client, as for the Handler; the request fails with ErrClosed.
	OnState func(s iec102.State, err error)
	// Logger receives the log entries; nil is silent.
	Logger iec60870.Logger
}

// Until says when a request is complete.
type Until uint8

// Completion rules of a request; see the package documentation.
const (
	UntilReply Until = iota + 1
	UntilConfirmation
	UntilTermination
)

// Client is a controlling station.
type Client struct {
	cfg Config
	log *logx.Logger
	pri *link.Primary

	sem     chan struct{} // one request at a time
	handled chan *iec102.ASDU

	mu    sync.Mutex
	state iec102.State
	acd   bool
	// dirty is set when a request to common address dirtyCA ended before
	// its answer was complete: the next request drains the station first.
	dirty   bool
	dirtyCA uint16

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	closeOnce sync.Once
	closeErr  error

	handlerID atomic.Uint64 // the goroutine of deliver
	failMu    sync.Mutex
	failed    error // why a callback stopped the client
}

// New starts a controlling station on rw. Close stops it and closes rw
// when it is an io.Closer.
func New(rw io.ReadWriter, cfg Config) (*Client, error) {
	fp, err := iec102.LinkParams(cfg.LinkAddrSize)
	if err != nil {
		return nil, err
	}
	if err := cfg.Params.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %w", iec102.ErrInvalidConfig, err)
	}
	if cfg.LinkAddr > fp.MaxAddr() {
		return nil, fmt.Errorf("%w: link address %d does not fit %d octet(s)", iec102.ErrInvalidConfig, cfg.LinkAddr, fp.AddrSize)
	}
	cfg.Link.FT12 = fp
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = DefaultPollInterval
	}
	if cfg.RequestTimeout <= 0 {
		cfg.RequestTimeout = DefaultRequestTimeout
	}
	switch {
	case cfg.MaxResult == 0:
		cfg.MaxResult = DefaultMaxResult
	case cfg.MaxResult < 0:
		cfg.MaxResult = 0 // no bound
	}
	lc := cfg.Link
	if lc.Logger == nil {
		lc.Logger = cfg.Logger
	}
	pri, err := link.NewPrimary(rw, lc)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", iec102.ErrInvalidConfig, err)
	}
	c := &Client{
		cfg:     cfg,
		log:     logx.New(cfg.Logger, "iec102.client", "link", cfg.LinkAddr),
		pri:     pri,
		sem:     make(chan struct{}, 1),
		handled: make(chan *iec102.ASDU, 64),
	}
	c.ctx, c.cancel = context.WithCancel(context.Background())
	c.wg.Add(1)
	go c.deliver()
	return c, nil
}

// State returns the link state.
func (c *Client) State() iec102.State {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state
}

// CommonAddr returns the common address of ASDU the requests carry.
func (c *Client) CommonAddr() uint16 { return c.cfg.CommonAddr }

// Done is closed when the station stops: after Close, when the transport
// fails, or when a callback did not return normally. Err tells which.
func (c *Client) Done() <-chan struct{} { return c.pri.Done() }

// Err returns the error that stopped the station: the transport's, or that
// of a callback that did not return normally (wrapping
// [iec102.ErrClosed]); nil while it runs or after Close.
func (c *Client) Err() error {
	c.failMu.Lock()
	failed := c.failed
	c.failMu.Unlock()
	if failed != nil {
		return failed
	}
	return c.pri.Err()
}

// Close stops the station. Outstanding requests, and requests made
// afterwards, fail with [iec102.ErrClosed]. Close is safe to call more
// than once and from several goroutines: the first call closes the
// transport and returns its error; later calls return nil.
//
// Called from the Handler, Close cannot wait for the Handler's goroutine:
// it stops the client and returns nil at once, and the close completes,
// StateClosed reported included, when the Handler has returned.
func (c *Client) Close() error {
	if c.handlerID.Load() == callback.Goroutine() {
		c.closeAsync()
		return nil
	}
	return c.close()
}

func (c *Client) closeAsync() {
	c.cancel()
	go func() { _ = c.close() }()
}

// stop ends the client after a callback did not return normally: Err
// reports err from now on, unless the client was closing already.
func (c *Client) stop(err error) {
	c.failMu.Lock()
	if c.failed == nil && c.ctx.Err() == nil {
		c.failed = fmt.Errorf("%w: %w", iec102.ErrClosed, err)
	}
	c.failMu.Unlock()
	c.closeAsync()
}

func (c *Client) close() error {
	first := false
	c.closeOnce.Do(func() {
		first = true
		c.cancel()
		c.closeErr = c.pri.Close()
		c.wg.Wait()
	})
	if !first {
		return nil
	}
	// Outside the Once: OnState(StateClosed) may call Close.
	c.setState(iec102.StateClosed, nil)
	return c.closeErr
}

func (c *Client) setState(s iec102.State, err error) {
	c.mu.Lock()
	if c.state == s || c.state == iec102.StateClosed {
		c.mu.Unlock()
		return
	}
	c.state = s
	c.mu.Unlock()
	c.log.Info("link state", "state", s.String(), "error", err)
	if f := c.cfg.OnState; f != nil {
		if cerr := callback.Call(c.log, "OnState", func() { f(s, err) }); cerr != nil {
			c.stop(cerr)
		}
	}
}

func (c *Client) deliver() {
	defer c.wg.Done()
	c.handlerID.Store(callback.Goroutine())
	for {
		select {
		case a := <-c.handled:
			// Not after Close from the Handler, which may have come first.
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

// acquire takes the request slot and bounds ctx by RequestTimeout.
func (c *Client) acquire(ctx context.Context) (context.Context, func(), error) {
	if c.State() == iec102.StateClosed {
		return nil, nil, iec102.ErrClosed
	}
	select {
	case c.sem <- struct{}{}:
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	case <-c.ctx.Done():
		return nil, nil, iec102.ErrClosed
	}
	cancel := func() {}
	if _, ok := ctx.Deadline(); !ok {
		ctx, cancel = context.WithTimeout(ctx, c.cfg.RequestTimeout)
	}
	ctx, stop := context.WithCancel(ctx)
	unhook := context.AfterFunc(c.ctx, stop)
	return ctx, func() { unhook(); stop(); cancel(); <-c.sem }, nil
}

// linkErr marks the link down after a transport failure or a station that
// stopped answering, and wraps err.
func (c *Client) linkErr(err error) error {
	if c.ctx.Err() != nil {
		return iec102.ErrClosed
	}
	if errors.Is(err, link.ErrTimeout) || errors.Is(err, link.ErrClosed) {
		c.setState(iec102.StateDown, err)
		return fmt.Errorf("%w: %w", iec102.ErrLinkDown, err)
	}
	return err
}

// Init initializes the link: request status of link, then reset of remote
// link (IEC 60870-5-2 as profiled by IEC 60870-5-101:2003, clause 6.2;
// gisce/iec870ree and daiiniel/IEC60870-5-102 do the same).
func (c *Client) Init(ctx context.Context) error {
	ctx, release, err := c.acquire(ctx)
	if err != nil {
		return err
	}
	defer release()
	return c.init(ctx)
}

func (c *Client) init(ctx context.Context) error {
	addr := c.cfg.LinkAddr
	if _, err := c.pri.RequestStatus(ctx, addr); err != nil {
		return c.linkErr(err)
	}
	st, err := c.pri.ResetLink(ctx, addr)
	if err != nil {
		return c.linkErr(err)
	}
	c.mu.Lock()
	c.acd = st.ACD
	c.mu.Unlock()
	c.setState(iec102.StateUp, nil)
	return nil
}

// Request sends a and collects the station's answers until the request is
// complete by rule until. It returns every ASDU of the station's common
// address received meanwhile, the mirrors included. A refusal returns the
// answers so far and an [*iec102.RefusedError]. An answer of more ASDUs
// than Config.MaxResult ends the request with the ASDUs so far and an
// error wrapping [iec102.ErrResultLimit].
//
// When an earlier request ended before its answer was complete, Request
// first polls the station until it reports no more data, dropping what of
// that answer arrives (ASDUs of other common addresses still reach the
// Handler); the rest of an abandoned answer is never returned as the
// answer of this request.
func (c *Client) Request(ctx context.Context, a *iec102.ASDU, until Until) (_ []*iec102.ASDU, err error) {
	raw, err := a.Encode(c.cfg.Params)
	if err != nil {
		return nil, err
	}
	obj, err := a.Body.AppendTo(nil, c.cfg.Params)
	if err != nil {
		return nil, err
	}
	ctx, release, err := c.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	if c.State() != iec102.StateUp {
		if err := c.init(ctx); err != nil {
			return nil, err
		}
	}
	if ca, dirty := c.isDirty(); dirty {
		if err := c.drain(ctx, ca); err != nil {
			return nil, err
		}
	}
	// From the first transmission on, an answer may be under way: a
	// request that ends without it complete leaves the link dirty.
	defer func() {
		var refused *iec102.RefusedError
		if err != nil && !errors.As(err, &refused) {
			c.setDirty(true, a.CommonAddr)
		}
	}()
	addr := c.cfg.LinkAddr
	for {
		st, err := c.pri.SendConfirm(ctx, addr, raw)
		if err == nil {
			c.setACD(st.ACD)
			break
		}
		if !errors.Is(err, link.ErrNack) {
			return nil, c.linkErr(err)
		}
		// NACK: the station is busy (DFC); try again later.
		if err := c.pause(ctx); err != nil {
			return nil, err
		}
	}
	var out []*iec102.ASDU
	for {
		class := link.Class2
		if c.getACD() {
			class = link.Class1
		}
		data, st, err := c.pri.RequestData(ctx, addr, class)
		if err != nil {
			return out, c.linkErr(err)
		}
		c.setACD(st.ACD)
		if data == nil {
			if st.ACD {
				continue
			}
			if err := c.pause(ctx); err != nil {
				return out, err
			}
			continue
		}
		r, err := iec102.Decode(data, c.cfg.Params)
		if err != nil {
			c.log.Warn("undecodable ASDU dropped", "error", err)
			continue
		}
		if r.CommonAddr != a.CommonAddr {
			c.handoff(r)
			continue
		}
		if c.cfg.MaxResult > 0 && len(out) >= c.cfg.MaxResult {
			return out, fmt.Errorf("%w: more than %d ASDUs answer %s", iec102.ErrResultLimit, c.cfg.MaxResult, a.Type)
		}
		out = append(out, r)
		if !c.mirrors(r, a, obj) {
			if until == UntilReply {
				return out, nil
			}
			continue
		}
		switch {
		case r.Cause.Refusal():
			return out, &iec102.RefusedError{ASDU: r}
		case r.Cause.Code() == iec102.CauseActivationTerm:
			return out, nil
		case r.Cause.Code() == iec102.CauseActivationCon && until == UntilConfirmation:
			return out, nil
		}
	}
}

// mirrors reports whether r is the mirror of request a, whose encoded
// information object is obj: [iec102.ASDU.Mirrors], and the same
// information object, so that the addresses and the time range are the
// requested ones. The recorded traffic (innoWatt log) has the confirmation
// and the termination repeat the request's object; that a refusal does so
// too is an interpretation (docs/iec102/INTEROPERABILITY.md).
func (c *Client) mirrors(r, a *iec102.ASDU, obj []byte) bool {
	if !r.Mirrors(a) {
		return false
	}
	got, err := r.Body.AppendTo(nil, c.cfg.Params)
	return err == nil && bytes.Equal(got, obj)
}

// drain polls the station until it has no more data, after a request to
// common address ca that ended before its answer was complete. ASDUs of
// other common addresses go to the Handler; those of ca are the rest of
// that answer and are dropped.
func (c *Client) drain(ctx context.Context, ca uint16) error {
	addr := c.cfg.LinkAddr
	dropped := 0
	for {
		class := link.Class2
		if c.getACD() {
			class = link.Class1
		}
		data, st, err := c.pri.RequestData(ctx, addr, class)
		if err != nil {
			return c.linkErr(err)
		}
		c.setACD(st.ACD)
		if data == nil {
			if st.ACD {
				continue
			}
			c.setDirty(false, 0)
			if dropped > 0 {
				c.log.Debug("rest of an unfinished answer dropped", "asdus", dropped)
			}
			return nil
		}
		r, err := iec102.Decode(data, c.cfg.Params)
		if err != nil {
			c.log.Warn("undecodable ASDU dropped", "error", err)
			continue
		}
		if r.CommonAddr != ca {
			c.handoff(r)
			continue
		}
		dropped++
	}
}

func (c *Client) isDirty() (uint16, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.dirtyCA, c.dirty
}

func (c *Client) setDirty(v bool, ca uint16) {
	c.mu.Lock()
	c.dirty, c.dirtyCA = v, ca
	c.mu.Unlock()
}

func (c *Client) handoff(a *iec102.ASDU) {
	select {
	case c.handled <- a:
	default:
		c.log.Warn("handler queue full, ASDU dropped", "asdu", a.String())
	}
}

func (c *Client) setACD(v bool) {
	c.mu.Lock()
	c.acd = v
	c.mu.Unlock()
}

func (c *Client) getACD() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.acd
}

func (c *Client) pause(ctx context.Context) error {
	t := time.NewTimer(c.cfg.PollInterval)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		if c.ctx.Err() != nil {
			return iec102.ErrClosed
		}
		return fmt.Errorf("iec102: waiting for the answer: %w", ctx.Err())
	}
}

// request builds an ASDU for this client's common address.
func (c *Client) request(cause iec102.Cause, record uint8, body iec102.Body) *iec102.ASDU {
	return iec102.New(cause, c.cfg.CommonAddr, record, body)
}

// ReadProductSpec reads the manufacturer and product specification:
// C_RD_NA_2 with cause 5, answered by P_MP_NA_2 (gisce/iec870ree).
func (c *Client) ReadProductSpec(ctx context.Context) (iec102.ProductSpec, error) {
	out, err := c.Request(ctx, c.request(iec102.CauseRequest, 0, iec102.Read{T: iec102.C_RD_NA_2}), UntilReply)
	if err != nil {
		return iec102.ProductSpec{}, err
	}
	if b, ok := out[len(out)-1].Body.(iec102.ProductSpec); ok {
		return b, nil
	}
	return iec102.ProductSpec{}, fmt.Errorf("%w: C_RD_NA_2 answered by %s", iec102.ErrInvalid, out[len(out)-1])
}

// ReadTime reads the current system time of the station: C_TI_NA_2 with
// cause 5, answered by M_TI_TA_2 (gisce/iec870ree).
func (c *Client) ReadTime(ctx context.Context) (iec102.TimeB, error) {
	out, err := c.Request(ctx, c.request(iec102.CauseRequest, 0, iec102.Read{T: iec102.C_TI_NA_2}), UntilReply)
	if err != nil {
		return iec102.TimeB{}, err
	}
	if b, ok := out[len(out)-1].Body.(iec102.SystemTime); ok {
		return b.Time, nil
	}
	return iec102.TimeB{}, fmt.Errorf("%w: C_TI_NA_2 answered by %s", iec102.ErrInvalid, out[len(out)-1])
}

// ReadTotals reads integrated totals with one of the read commands
// C_CI_NA_2 .. C_CI_NU_2 (q.T; the fields q carries depend on it, see
// iec102.Read) from record address record, with cause 6, and returns the
// integration periods the station sent, in order.
func (c *Client) ReadTotals(ctx context.Context, record uint8, q iec102.Read) ([]iec102.IntegratedTotals, error) {
	if q.T.Selection() == iec102.SelectNone {
		return nil, fmt.Errorf("%w: %s is not a read of integrated totals", iec102.ErrInvalid, q.T)
	}
	out, err := c.Request(ctx, c.request(iec102.CauseActivation, record, q), UntilTermination)
	var res []iec102.IntegratedTotals
	for _, a := range out {
		if b, ok := a.Body.(iec102.IntegratedTotals); ok {
			res = append(res, b)
		}
	}
	return res, err
}

// ReadSinglePoints reads the single-point information of record address
// record (a list address 50..55, Beckhoff TS650x manual, section 9.1.8):
// all of it with C_SP_NA_2, or the time range from..to with C_SP_NB_2 when
// both are given; cause 6.
func (c *Client) ReadSinglePoints(ctx context.Context, record uint8, from, to *iec102.TimeA) ([]iec102.SinglePoint, error) {
	q := iec102.Read{T: iec102.C_SP_NA_2}
	if from != nil && to != nil {
		q = iec102.Read{T: iec102.C_SP_NB_2, From: *from, To: *to}
	}
	out, err := c.Request(ctx, c.request(iec102.CauseActivation, record, q), UntilTermination)
	var res []iec102.SinglePoint
	for _, a := range out {
		if b, ok := a.Body.(iec102.SinglePoints); ok {
			res = append(res, b.Points...)
		}
	}
	return res, err
}
