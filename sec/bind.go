// SPDX-License-Identifier: MIT

package sec

import (
	"fmt"
	"time"

	"github.com/otfabric/go-iec60870-5/internal/secbind"
)

// A controlled station that serves several connections of one controlling
// station (a redundancy group) shares one Endpoint between them. A
// connection that does not carry the association is a candidate: what it
// receives runs through a copy of the association of its own, so that its
// key management changes nothing the carrying connection depends on. When
// a message of the candidate authenticates (a Station Association or
// Session Key Change completes, or Secure Data verifies under the current
// session keys), the candidate takes the association over with what it
// established, as [Endpoint.Attach] does. A candidate is dropped by
// [Endpoint.Detach] of its connection.
//
// Secure Data binds a connection under session keys established on an
// earlier connection too, as long as they are within their limits: a
// controlling station that keeps its session keys over a reconnect sends
// data under them before it changes them, and refusing that data would
// hold it until the keys run out. The sequence numbers still have to
// increase. A Station Association completed on a candidate ends the
// session: the connection binds without session keys, and the Session
// Key Change follows on it.

func init() {
	secbind.ReceiveAs = func(ep, owner any, now time.Time, raw []byte) ([]byte, [][]byte, bool, error) {
		e, ok := ep.(*Endpoint)
		if !ok || e == nil {
			return nil, nil, false, fmt.Errorf("%w: not an endpoint", ErrConfig)
		}
		return e.receiveAs(owner, now, raw)
	}
}

// receiveAs is [Endpoint.Receive] for a connection owner that does not
// carry the association; bound reports that owner carries it now.
func (e *Endpoint) receiveAs(owner any, now time.Time, raw []byte) (data []byte, out [][]byte, bound bool, err error) {
	e.mu.Lock()
	defer e.unlock()
	e.clock = now
	if e.cfg.Role != Controlled {
		return nil, nil, false, fmt.Errorf("%w: only a controlled station binds connections to a shared association", ErrConfig)
	}
	if e.up && e.owner == owner {
		data, out, err = e.receive(now, raw)
		return data, e.flushReports(now, out), true, err
	}
	// Keys that reached their limits are invalidated first, so that the
	// candidate does not authenticate under them.
	e.invalidateWornOut(now)
	c := e.cands[owner]
	if c == nil {
		c = e.candidate()
		if e.cands == nil {
			e.cands = map[any]*Endpoint{}
		}
		e.cands[owner] = c
	}
	c.follow(e)
	c.clock = now
	data, out, err = c.receive(now, raw)
	if !c.authenticated {
		return data, out, false, err
	}
	delete(e.cands, owner)
	e.takeOver(c, owner, now)
	return data, e.flushReports(now, out), true, err
}

// candidate returns a copy of the association for a connection that does
// not carry it: it saves nothing, sends no reports, and counts in the
// statistics of e. The caller holds e.mu.
func (e *Endpoint) candidate() *Endpoint {
	cfg := e.cfg
	cfg.Store = &MemoryStore{}
	cfg.OnEvent = nil
	cfg.Reports = nil
	c := &Endpoint{cfg: cfg, priv: e.priv, curve: e.curve, changed: make(chan struct{}), parent: e, up: true}
	c.reasm = reassembler{p: cfg.Params, discarded: func() { e.inc(StatDiscardedMessages, nil) }}
	c.skc = skcNoAssociation
	return c
}

// follow brings the copy of the association in c up to date with e,
// except for what a procedure of c is using: c authenticates against the
// keys and the sequence numbers e has now. The caller holds e.mu.
func (c *Endpoint) follow(e *Endpoint) {
	if !c.waitUH && c.skc != skcWaitKeyChange {
		c.st, c.assoc, c.keys, c.mal, c.remote = e.st, e.assoc, e.keys, e.mal, e.remote
		c.skc = skcNoAssociation
		if c.assoc {
			c.skc = skcIdle
		}
	}
	c.session, c.stale, c.sess, c.dp = e.session, e.stale, e.sess, e.dp
	c.keySince, c.keyUses, c.txSeq, c.rxSeq = e.keySince, e.keyUses, e.txSeq, e.rxSeq
}

// takeOver makes owner the connection that carries the association, with
// what its candidate c established. The caller holds e.mu.
func (e *Endpoint) takeOver(c *Endpoint, owner any, now time.Time) {
	if e.up {
		e.down()
	}
	e.owner = owner
	e.upLocked(now)
	e.seq = c.seq
	e.st, e.assoc, e.keys, e.mal, e.remote = c.st, c.assoc, c.keys, c.mal, c.remote
	e.peerCert, e.peerDH, e.arRecv = c.peerCert, c.peerDH, c.arRecv
	e.waitUH, e.saTimer, e.apSent, e.apRandom, e.uhValid, e.upSent = c.waitUH, c.saTimer, c.apSent, c.apRandom, c.uhValid, c.upSent
	e.skc, e.skcTimer, e.sqRecv, e.spRandom, e.khValid, e.kpSent = c.skc, c.skcTimer, c.sqRecv, c.spRandom, c.khValid, c.kpSent
	e.solicited = c.solicited
	e.sess, e.dp, e.keySince, e.keyUses, e.txSeq, e.rxSeq = c.sess, c.dp, c.keySince, c.keyUses, c.txSeq, c.rxSeq
	// The session as the candidate left it: ended by a Station Association
	// on it, and fresh after a Session Key Change on it. Otherwise its keys
	// are of an earlier connection.
	e.session = c.session
	e.stale = e.session && c.authBy != StatSuccessfulSessionKeyChanges
	e.save()
	e.notify()
}
