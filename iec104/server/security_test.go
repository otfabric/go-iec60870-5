// SPDX-License-Identifier: MIT

package server

import (
	"bytes"
	"crypto/x509/pkix"
	"errors"
	"testing"
	"time"

	"github.com/otfabric/go-iec60870-5/asdu"
	"github.com/otfabric/go-iec60870-5/sec"
)

// These tests drive the security routes of sessions directly, as their
// links do: a controlling station's sec.Endpoint on one side, the route of
// a session on the other, every wire ASDU handed across by the test.

type secFixture struct {
	t       *testing.T
	now     time.Time
	srvID   *sec.Identity
	cliID   *sec.Identity
	made    int // endpoints newEndpoint made
	set     *associations
	cliKeys sec.KeyStore // the controlling station's association, shared by its endpoints
}

const testAIM = 7

func newSecFixture(t *testing.T) *secFixture {
	t.Helper()
	srvID, err := sec.NewIdentity(sec.IdentityOptions{Subject: pkix.Name{CommonName: "station"}})
	if err != nil {
		t.Fatal(err)
	}
	cliID, err := sec.NewIdentity(sec.IdentityOptions{Subject: pkix.Name{CommonName: "scada"}})
	if err != nil {
		t.Fatal(err)
	}
	f := &secFixture{t: t, now: time.Now(), srvID: srvID, cliID: cliID, cliKeys: &sec.MemoryStore{}}
	f.set = &associations{group: "test", params: asdu.IEC104, newEndpoint: func(string, uint16) (*sec.Endpoint, error) {
		f.made++
		return sec.New(sec.Config{Role: sec.Controlled, AssociationID: 9, Identity: f.srvID, Trust: f.cliID.Pin(), CommonAddr: 1})
	}}
	return f
}

// controlling returns an endpoint of the controlling station testAIM.
func (f *secFixture) controlling() *sec.Endpoint {
	f.t.Helper()
	ep, err := sec.New(sec.Config{Role: sec.Controlling, AssociationID: testAIM, Identity: f.cliID, Trust: f.srvID.Pin(),
		CommonAddr: 1, DataProtection: sec.HMACSHA256.Code, Store: f.cliKeys})
	if err != nil {
		f.t.Fatal(err)
	}
	return ep
}

// session is the route of one session of the station, as its link uses it.
type session struct {
	r     *route
	owner *int
}

func (f *secFixture) session() *session {
	return &session{r: &route{set: f.set}, owner: new(int)}
}

// connect starts data transfer between a controlling endpoint and a
// session and runs the exchange until the controlling station is ready.
func (f *secFixture) connect(ctl *sec.Endpoint, s *session) {
	f.t.Helper()
	toStation := ctl.Up(f.now)
	if out := s.r.Attach(s.owner, f.now); len(out) > 0 {
		f.t.Fatalf("an unbound session sent %d ASDUs when it started", len(out))
	}
	f.exchange(ctl, s, toStation)
	if !ctl.Ready() {
		f.t.Fatalf("the controlling station is not ready: %+v", ctl.Status())
	}
}

// exchange hands frames to the session and its answers back until neither
// side has anything more to send.
func (f *secFixture) exchange(ctl *sec.Endpoint, s *session, toStation [][]byte) {
	f.t.Helper()
	for range 20 {
		var toControlling [][]byte
		for _, raw := range toStation {
			_, out, _ := s.r.Receive(f.now, raw)
			toControlling = append(toControlling, out...)
		}
		toStation = nil
		for _, raw := range toControlling {
			_, out, _ := ctl.Receive(f.now, raw)
			toStation = append(toStation, out...)
		}
		toStation = append(toStation, ctl.Tick(f.now)...)
		if len(toStation) == 0 {
			return
		}
	}
	f.t.Fatal("the key management did not settle")
}

// carries checks that s carries the association: it owns it, protects what
// the station sends, and verifies what ctl protects.
func (f *secFixture) carries(ctl *sec.Endpoint, s *session, what string) {
	f.t.Helper()
	ep := s.r.endpoint()
	if ep == nil || !s.r.Owns(s.owner) || !ep.Status().Up {
		f.t.Fatalf("%s: the session does not carry the association", what)
	}
	app, err := asdu.New(asdu.CauseSpontaneous, 1, asdu.SinglePoint{IOA: 1, Value: true}).Encode(asdu.IEC104)
	if err != nil {
		f.t.Fatal(err)
	}
	frames, err := s.r.Protect(f.now, app)
	if err != nil {
		f.t.Fatalf("%s: Protect: %v", what, err)
	}
	var got []byte
	for _, raw := range frames {
		if data, _, err := ctl.Receive(f.now, raw); err != nil {
			f.t.Fatalf("%s: the controlling station refused the station's data: %v", what, err)
		} else if data != nil {
			got = data
		}
	}
	if !bytes.Equal(got, app) {
		f.t.Fatalf("%s: the controlling station received % X, want % X", what, got, app)
	}
	frames, err = ctl.Protect(f.now, app)
	if err != nil {
		f.t.Fatalf("%s: controlling Protect: %v", what, err)
	}
	got = nil
	for _, raw := range frames {
		data, _, err := s.r.Receive(f.now, raw)
		if err != nil {
			f.t.Fatalf("%s: the session refused the controlling station's data: %v", what, err)
		}
		if data != nil {
			got = data
		}
	}
	if !bytes.Equal(got, app) {
		f.t.Fatalf("%s: the session received % X, want % X", what, got, app)
	}
}

// wire encodes one security message from the controlling direction as
// its wire segments.
func wire(t *testing.T, m sec.Message) [][]byte {
	t.Helper()
	h := sec.Header{CommonAddr: 1}
	h.Cause, _ = sec.PermittedCause(m.Type(), sec.Control)
	whole, err := sec.Encode(h, asdu.IEC104, m)
	if err != nil {
		t.Fatal(err)
	}
	segs, _, err := sec.Segment(whole, asdu.IEC104, 249, 0)
	if err != nil {
		t.Fatal(err)
	}
	return segs
}

// Messages that no key of the association authenticates.
func unauthenticated(t *testing.T) map[string][][]byte {
	random := bytes.Repeat([]byte{0x5a}, 32)
	return map[string][][]byte{
		"session request": wire(t, sec.SessionRequest{AIM: testAIM, AIS: 9, Protocol: sec.Version1, Random: random}),
		"session key change request": wire(t, sec.SessionKeyChangeRequest{AIM: testAIM, AIS: 9,
			DataProtection: sec.HMACSHA256.Code, Wrapped: make([]byte, 72), Tag: make([]byte, 16)}),
		"secure data": wire(t, sec.SecureData{AIM: testAIM, AIS: 9, Sequence: 1 << 20, Payload: make([]byte, 40)}),
		"update key change request": wire(t, sec.UpdateKeyChangeRequest{AIM: testAIM, AIS: 9, KeyWrap: sec.KeyWrapAES256,
			MAC: sec.HMACSHA256.Code, Random: random, Tag: make([]byte, 16)}),
	}
}

// A session that has not authenticated anything for the association of
// an AIM neither takes it over nor changes it: the session that carries it
// still owns it, protects what the station sends and verifies what the
// controlling station sends.
func TestUnauthenticatedMessageLeavesTheAssociation(t *testing.T) {
	for name, frames := range unauthenticated(t) {
		t.Run(name, func(t *testing.T) {
			f := newSecFixture(t)
			ctl := f.controlling()
			first := f.session()
			f.connect(ctl, first)
			f.carries(ctl, first, "before")

			other := f.session()
			other.r.Attach(other.owner, f.now)
			for _, raw := range frames {
				_, _, _ = other.r.Receive(f.now, raw)
			}
			if other.r.endpoint() != nil {
				t.Errorf("the other session is bound to the association")
			}
			f.carries(ctl, first, "after the other session's "+name)

			other.r.Detach(other.owner)
			f.carries(ctl, first, "after the other session ended")
		})
	}
}

// A second session of the same controlling station that authenticates
// (a Session Key Change with the keys of the association) takes the
// association over; the first one no longer carries it.
func TestAuthenticatedSessionTakesTheAssociationOver(t *testing.T) {
	f := newSecFixture(t)
	first, second := f.session(), f.session()
	ctlA := f.controlling()
	f.connect(ctlA, first)
	f.carries(ctlA, first, "first")

	ctlB := f.controlling() // the same station on another path, with the same keys
	f.connect(ctlB, second)
	f.carries(ctlB, second, "second")
	if first.r.Owns(first.owner) {
		t.Error("the first session still carries the association")
	}
	if first.r.endpoint() != second.r.endpoint() {
		t.Error("the sessions are bound to different associations")
	}
	if kept, pending := f.set.count(); kept != 1 || pending != 0 {
		t.Errorf("%d associations kept, %d pending, want 1 and 0", kept, pending)
	}
}

// An association that no message authenticated is not kept: sessions that
// name distinct AIMs and end leave nothing behind.
func TestUnauthenticatedAssociationsAreNotKept(t *testing.T) {
	for _, n := range []int{1, 2, 64} {
		f := newSecFixture(t)
		for aim := range uint16(n) {
			s := f.session()
			s.r.Attach(s.owner, f.now)
			for _, raw := range wire(t, sec.SessionRequest{AIM: 100 + aim, AIS: 9, Protocol: sec.Version1, Random: make([]byte, 32)}) {
				_, _, _ = s.r.Receive(f.now, raw)
			}
			if kept, pending := f.set.count(); kept != 0 || pending != 1 {
				t.Fatalf("%d AIMs: while session %d waits: %d kept, %d pending, want 0 and 1", n, aim, kept, pending)
			}
			s.r.Detach(s.owner)
		}
		if kept, pending := f.set.count(); kept != 0 || pending != 0 {
			t.Errorf("%d AIMs named by sessions that ended: %d associations kept, %d pending, want none", n, kept, pending)
		}
		if f.made != n {
			t.Errorf("%d AIMs: %d endpoints made", n, f.made)
		}
	}
}

// Sessions that wait for the same AIM share its pending association; the
// first to authenticate keeps it, and it stays after the session ends.
func TestPendingAssociationIsShared(t *testing.T) {
	f := newSecFixture(t)
	waiting := f.session()
	waiting.r.Attach(waiting.owner, f.now)
	for _, raw := range wire(t, sec.SessionRequest{AIM: testAIM, AIS: 9, Protocol: sec.Version1, Random: make([]byte, 32)}) {
		_, _, _ = waiting.r.Receive(f.now, raw)
	}
	ctl := f.controlling()
	s := f.session()
	f.connect(ctl, s)
	f.carries(ctl, s, "authenticated")
	if f.made != 1 {
		t.Errorf("%d endpoints made for one AIM", f.made)
	}
	waiting.r.Detach(waiting.owner)
	s.r.Detach(s.owner)
	if kept, pending := f.set.count(); kept != 1 || pending != 0 {
		t.Errorf("after both sessions ended: %d kept, %d pending, want 1 and 0", kept, pending)
	}
}

// A security endpoint factory that does not return normally ends the
// session that asked for the association, and leaves nothing behind.
func TestEndpointFactoryPanicEndsTheSession(t *testing.T) {
	f := newSecFixture(t)
	f.set.newEndpoint = func(string, uint16) (*sec.Endpoint, error) { panic("boom") }
	ended := 0
	s := f.session()
	s.r.abort = func() { ended++ }
	ctl := f.controlling()
	s.r.Attach(s.owner, f.now)
	_, out, err := s.r.Receive(f.now, ctl.Up(f.now)[0])
	if !errors.Is(err, errFactory) || len(out) != 0 {
		t.Fatalf("Receive: %d ASDUs, %v; want none and errFactory", len(out), err)
	}
	if ended == 0 {
		t.Fatal("the session was not ended")
	}
	if kept, pending := f.set.count(); kept != 0 || pending != 0 {
		t.Fatalf("%d associations kept and %d pending after the factory failed", kept, pending)
	}
}
