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

// acquire returns the association of the controlling station aim for a
// connection that names it: the kept one, or the pending one, made on
// first use. pending reports the latter; the caller then calls release or
// commit.
func (a *associations) acquire(aim uint16) (ep *sec.Endpoint, pending bool, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if ep, ok := a.byAIM[aim]; ok {
		return ep, false, nil
	}
	if p, ok := a.pending[aim]; ok {
		p.refs++
		return p.ep, true, nil
	}
	ep, err = a.newEndpoint(a.group, aim)
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

func (r *route) Attach(owner any, now time.Time) [][]byte {
	if ep := r.endpoint(); ep != nil {
		return ep.Attach(owner, now)
	}
	r.owner, r.started = owner, true
	return nil
}

func (r *route) Detach(owner any) {
	if ep := r.endpoint(); ep != nil {
		ep.Detach(owner)
		return
	}
	r.drop(owner)
	r.owner, r.started = nil, false
}

// drop gives up the association the session waits for.
func (r *route) drop(owner any) {
	if r.cand == nil {
		return
	}
	r.cand.Detach(owner) // drops what the session's key management began
	if r.candPending {
		r.set.release(r.candAIM, r.cand)
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

func (r *route) Receive(now time.Time, raw []byte) ([]byte, [][]byte, error) {
	if ep := r.endpoint(); ep != nil {
		return ep.Receive(now, raw)
	}
	if aim, ok := sec.AssociationOf(raw, r.set.params); ok && (r.cand == nil || aim != r.candAIM) {
		// The first message, or one that names another controlling station.
		r.drop(r.owner)
		ep, pending, err := r.set.acquire(aim)
		if err != nil {
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
	data, out, bound, err := secbind.ReceiveAs(r.cand, r.owner, now, raw)
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

func (r *route) Protect(now time.Time, data []byte) ([][]byte, error) {
	if ep := r.endpoint(); ep != nil {
		return ep.Protect(now, data)
	}
	return nil, sec.ErrNotReady
}

func (r *route) Tick(now time.Time) [][]byte {
	if ep := r.endpoint(); ep != nil {
		return ep.Tick(now)
	}
	return nil
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
