// SPDX-License-Identifier: MIT

package server

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/otfabric/go-iec60870-5/asdu"
	iec104 "github.com/otfabric/go-iec60870-5/iec104"
	"github.com/otfabric/go-iec60870-5/internal/callback"
	"github.com/otfabric/go-iec60870-5/internal/logx"
	"github.com/otfabric/go-iec60870-5/internal/secbind"
	"github.com/otfabric/go-iec60870-5/sec"
)

// associations are the associations of a redundancy group: one per
// controlling station, by its AIM. An association is made when that
// station is first heard; it is kept once a message has authenticated a
// connection for it, and dropped with the last connection that waits for
// that otherwise.
type associations struct {
	group       string
	params      asdu.Params
	newEndpoint func(group string, aim uint16) (*sec.Endpoint, error)

	mu      sync.Mutex
	byAIM   map[uint16]*sec.Endpoint // authenticated
	pending map[uint16]*pendingAssoc // made, nothing authenticated yet
}

// pendingAssoc is an association no message has authenticated yet, and
// how many connections wait for one.
type pendingAssoc struct {
	ep   *sec.Endpoint
	refs int
}

// errFactory is why a session ends whose security endpoint factory (see
// [WithSecurity]) did not return normally.
var errFactory = errors.New("security endpoint factory did not return normally")

// acquire returns the association of the controlling station aim for a
// connection that names it: the kept one, or the pending one, made on
// first use. pending reports the latter; the caller then calls release or
// commit. log logs a factory that does not return normally, which acquire
// reports as errFactory.
func (a *associations) acquire(aim uint16, log *logx.Logger) (ep *sec.Endpoint, pending bool, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if ep, ok := a.byAIM[aim]; ok {
		return ep, false, nil
	}
	if p, ok := a.pending[aim]; ok {
		p.refs++
		return p.ep, true, nil
	}
	if cerr := callback.Call(log, "security endpoint factory", func() { ep, err = a.newEndpoint(a.group, aim) }); cerr != nil {
		return nil, false, fmt.Errorf("%w: %w", errFactory, cerr)
	}
	if err != nil {
		return nil, false, err
	}
	if ep == nil || ep.Role() != sec.Controlled {
		return nil, false, fmt.Errorf("%w: security of redundancy group %q, AIM %d: the endpoint needs the controlled role",
			iec104.ErrInvalidOption, a.group, aim)
	}
	if a.pending == nil {
		a.pending = map[uint16]*pendingAssoc{}
	}
	a.pending[aim] = &pendingAssoc{ep: ep, refs: 1}
	return ep, true, nil
}

// release is called by a connection that acquired the pending association
// aim and stops waiting for it; the last one drops it.
func (a *associations) release(aim uint16, ep *sec.Endpoint) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if p, ok := a.pending[aim]; ok && p.ep == ep {
		if p.refs--; p.refs == 0 {
			delete(a.pending, aim)
		}
	}
}

// commit keeps the pending association aim: a message has authenticated a
// connection for it. The connection stops waiting.
func (a *associations) commit(aim uint16, ep *sec.Endpoint) {
	a.mu.Lock()
	defer a.mu.Unlock()
	p, ok := a.pending[aim]
	if !ok || p.ep != ep {
		return
	}
	if p.refs--; p.refs == 0 {
		delete(a.pending, aim)
	}
	if a.byAIM == nil {
		a.byAIM = map[uint16]*sec.Endpoint{}
	}
	a.byAIM[aim] = ep
}

// count returns how many associations are kept and pending.
func (a *associations) count() (kept, pending int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.byAIM), len(a.pending)
}

// errUnbound is why a session discards what it receives before the
// controlling station has said which association it uses.
var errUnbound = errors.New("no association yet: waiting for a security message")

// route is the security of one session, the link.Security of its link. It
// binds the session to the association of the controlling station that the
// first security message names, once a message on the session has been
// authenticated for that association, and from then on hands everything to
// that association, which other sessions of the same station (a client
// redundancy group) share. Until then the session's key management runs on
// its own (secbind.ReceiveAs): it neither carries nor changes the
// association.
//
// The link calls it from its protocol goroutine only; ep is published for
// the session's other goroutines.
type route struct {
	set *associations
	log *logx.Logger
	// abort ends the session; nil in tests that have none.
	abort func()
	// call is the session's Session.call, through which the calls of an
	// association run that may tell the application's sec.Config.OnEvent
	// of a security event; nil in tests that call the association
	// directly.
	call func(name string, f func()) bool

	ep      atomic.Pointer[sec.Endpoint]
	owner   any // the link, while data transfer is started and ep is nil
	started bool

	// The association the session waits to be bound to, while ep is nil.
	cand        *sec.Endpoint
	candAIM     uint16
	candPending bool // acquired as pending: released or committed
}

// endpoint returns the association of the session, nil until it is bound.
func (r *route) endpoint() *sec.Endpoint { return r.ep.Load() }

// onEvent names the application's sec.Config.OnEvent in the log entry of
// one that does not return normally.
const onEvent = "sec.Config.OnEvent"

// errOnEvent is what a call of the association reports whose
// sec.Config.OnEvent did not return normally, which ends the session.
var errOnEvent = fmt.Errorf("%w: %s did not return normally", iec104.ErrClosed, onEvent)

// event runs f, a call of an association that may tell the application's
// sec.Config.OnEvent of a security event, on the goroutine that drives the
// association, through the session's call: an OnEvent that does not return
// normally is logged with its stack and ends the session, and the server
// goes on serving the others. It reports whether f returned normally.
func (r *route) event(f func()) bool {
	if r.call == nil {
		f()
		return true
	}
	return r.call(onEvent, f)
}

func (r *route) Attach(owner any, now time.Time) (out [][]byte) {
	if ep := r.endpoint(); ep != nil {
		r.event(func() { out = ep.Attach(owner, now) })
		return out
	}
	r.owner, r.started = owner, true
	return nil
}

func (r *route) Detach(owner any) {
	if ep := r.endpoint(); ep != nil {
		r.event(func() { ep.Detach(owner) })
		return
	}
	r.drop(owner)
	r.owner, r.started = nil, false
}

// drop gives up the association the session waits for.
func (r *route) drop(owner any) {
	cand := r.cand
	if cand == nil {
		return
	}
	r.event(func() { cand.Detach(owner) }) // drops what the session's key management began
	if r.candPending {
		r.set.release(r.candAIM, cand)
	}
	r.cand, r.candPending = nil, false
}

// Owns reports true for a started session that is not bound yet, so that
// the link hands it what it receives.
func (r *route) Owns(owner any) bool {
	if ep := r.endpoint(); ep != nil {
		return ep.Owns(owner)
	}
	return r.started && r.owner == owner
}

func (r *route) Receive(now time.Time, raw []byte) (data []byte, out [][]byte, err error) {
	if ep := r.endpoint(); ep != nil {
		if !r.event(func() { data, out, err = ep.Receive(now, raw) }) {
			return nil, nil, errOnEvent
		}
		return data, out, err
	}
	if aim, ok := sec.AssociationOf(raw, r.set.params); ok && (r.cand == nil || aim != r.candAIM) {
		// The first message, or one that names another controlling station.
		r.drop(r.owner)
		ep, pending, err := r.set.acquire(aim, r.log)
		if err != nil {
			if errors.Is(err, errFactory) && r.abort != nil {
				r.abort()
			}
			r.log.Warn("no association for the controlling station", "aim", aim, "error", err)
			return nil, nil, err
		}
		r.cand, r.candAIM, r.candPending = ep, aim, pending
	}
	if r.cand == nil {
		if len(raw) > 0 && !asdu.TypeID(raw[0]).IsSecurity() {
			return nil, nil, fmt.Errorf("%w: %s", sec.ErrUnsecured, asdu.TypeID(raw[0]))
		}
		return nil, nil, errUnbound
	}
	var bound bool
	cand := r.cand
	if !r.event(func() { data, out, bound, err = secbind.ReceiveAs(cand, r.owner, now, raw) }) {
		return nil, nil, errOnEvent
	}
	if bound {
		ep := r.cand
		if r.candPending {
			r.set.commit(r.candAIM, ep)
		}
		r.cand, r.candPending = nil, false
		r.ep.Store(ep)
		r.owner = nil
		r.log.Debug("session bound to the association of the controlling station", "aim", r.candAIM)
	}
	return data, out, err
}

func (r *route) Protect(now time.Time, data []byte) (out [][]byte, err error) {
	if ep := r.endpoint(); ep != nil {
		if !r.event(func() { out, err = ep.Protect(now, data) }) {
			return nil, errOnEvent
		}
		return out, err
	}
	return nil, sec.ErrNotReady
}

func (r *route) Tick(now time.Time) (out [][]byte) {
	if ep := r.endpoint(); ep != nil {
		r.event(func() { out = ep.Tick(now) })
	}
	return out
}

func (r *route) Deadline() time.Time {
	if ep := r.endpoint(); ep != nil {
		return ep.Deadline()
	}
	return time.Time{}
}

// Changed is nil until the session is bound: binding happens on the link's
// own goroutine, which looks again afterwards.
func (r *route) Changed() <-chan struct{} {
	if ep := r.endpoint(); ep != nil {
		return ep.Changed()
	}
	return nil
}
