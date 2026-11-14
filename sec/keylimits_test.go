// SPDX-License-Identifier: MIT

package sec

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/otfabric/go-iec60870-5/asdu"
)

// linkTick runs the timers of each station only when its Deadline is due,
// as the IEC 104 link does.
func (p *pair) linkTick() {
	for _, s := range []*station{p.a, p.b} {
		if d := s.e.Deadline(); !d.IsZero() && !p.now.Before(d) {
			p.send(s, s.e.Tick(p.now))
		}
	}
	p.run()
}

// reachUses runs exchanges until the controlled station's session keys
// reached Settings.KeyUses (4).
func reachUses(t *testing.T) *pair {
	t.Helper()
	p := newPair(t, func(_, b *Config) { b.Settings.KeyUses = 4 })
	p.up()
	p.mustReady()
	for range 2 {
		p.now = p.now.Add(10 * time.Second)
		p.linkTick()
		p.exchange()
	}
	if p.b.e.Ready() {
		t.Fatal("controlled station ready with its key uses reached")
	}
	if _, err := p.b.e.Protect(p.now, appData); !errors.Is(err, ErrNotReady) {
		t.Fatalf("protect with its key uses reached: %v", err)
	}
	if dl := p.b.e.Deadline(); dl.IsZero() || dl.After(p.now) {
		t.Fatalf("controlled deadline %v after the key uses were reached", dl.Sub(p.now))
	}
	return p
}

// renewed checks that the controlled station asked for new session keys
// and got them, with the timers run only when due.
func renewed(t *testing.T, p *pair) {
	t.Helper()
	for i := 0; i < 10 && !p.ready(); i++ {
		p.linkTick()
		p.now = p.now.Add(time.Second)
	}
	if !p.ready() {
		t.Fatalf("no new session: controlling %+v, controlled %+v", p.a.e.Status(), p.b.e.Status())
	}
	if n := p.a.count(StatSuccessfulSessionKeyChanges); n != 2 {
		t.Errorf("session key changes %d", n)
	}
	p.exchange()
}

// A controlled station stops accepting session keys that reached
// Settings.KeyUses when they are used, not only at its next timer, and asks
// for new ones without waiting for an unrelated timer.
func TestControlledKeyUsesEnforced(t *testing.T) {
	t.Run("at use", func(t *testing.T) {
		p := reachUses(t)
		n := len(p.b.data)
		out, err := p.a.e.Protect(p.now, appData)
		if err != nil {
			t.Fatal(err)
		}
		p.send(p.a, out)
		p.run()
		if len(p.b.data) != n || !errors.Is(p.b.lastErr(), ErrUnexpected) {
			t.Fatalf("secure data past the key uses: received %d, err %v", len(p.b.data)-n, p.b.lastErr())
		}
		if c := p.b.count(StatKeyInvalidationsCount); c != 1 || !p.b.hasEvent("SKEY_INV_USECNT") {
			t.Fatalf("key invalidations %d", c)
		}
		renewed(t, p)
		if c := p.b.count(StatKeyInvalidationsCount); c != 1 {
			t.Errorf("key invalidations %d after the new session", c)
		}
	})
	t.Run("at the deadline", func(t *testing.T) {
		p := reachUses(t)
		p.linkTick()
		if c := p.b.count(StatKeyInvalidationsCount); c != 1 {
			t.Fatalf("key invalidations %d", c)
		}
		renewed(t, p)
	})
}

// Session keys past their lifetime are refused when they are used.
func TestControlledKeyLifetimeAtUse(t *testing.T) {
	p := newPair(t, func(_, b *Config) { b.Settings.KeyLifetime = time.Minute })
	p.up()
	p.mustReady()
	p.now = p.now.Add(time.Minute)
	out, err := p.a.e.Protect(p.now, appData)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := deliver(p.b.e, p.now, out); !errors.Is(err, ErrUnexpected) {
		t.Fatalf("secure data under expired keys: %v", err)
	}
	if c := p.b.count(StatKeyInvalidationsTime); c != 1 {
		t.Fatalf("key invalidations %d", c)
	}
	p.mustReady()
	p.exchange()
}

// A new Station Association ends the session at the controlled station: the
// session keys of before are not accepted again.
func TestAssociationEndsSession(t *testing.T) {
	p := newPair(t, nil)
	p.up()
	p.mustReady()
	p.exchange()
	old, err := p.a.e.Protect(p.now, appData) // never delivered
	if err != nil {
		t.Fatal(err)
	}
	p.drop = func(from *station, b []byte) bool { return from == p.a && b[0] == byte(asdu.S_SQ_NA_1) }
	if err := p.a.e.Reassociate(); err != nil {
		t.Fatal(err)
	}
	ch := p.b.e.Changed()
	p.tick()
	if n := p.b.count(StatSuccessfulAssociations); n != 2 {
		t.Fatalf("associations %d", n)
	}
	select {
	case <-ch:
	default:
		t.Error("no change reported for the ended session")
	}
	if st := p.b.e.Status(); st.Ready || st.Data != "" {
		t.Fatalf("controlled status after the association %+v", st)
	}
	if _, err := deliver(p.b.e, p.now, old); !errors.Is(err, ErrUnexpected) {
		t.Fatalf("secure data under the keys of the earlier association: %v", err)
	}
	if st, _, _ := p.b.e.cfg.Store.Load(); st.ControlKey != nil || st.MonitorKey != nil {
		t.Error("session keys of the earlier association kept")
	}
	p.drop = nil
	p.mustReady()
	p.exchange()
}

// The remote certificate becomes the association's only once the Station
// Association authenticated it, in both roles.
func TestRemoteAfterAuthentication(t *testing.T) {
	idC := mustIdentity(IdentityOptions{})
	trustAC := Trust{Fingerprints: [][32]byte{Fingerprint(idA.Certificate), Fingerprint(idC.Certificate)}}

	// Controlling: not before the Update Key Change Response.
	p := newPair(t, nil)
	p.drop = func(from *station, b []byte) bool { return from == p.b && b[0] == byte(asdu.S_UP_NA_1) }
	p.up()
	if r := p.a.e.Status().Remote; r != nil {
		t.Fatalf("controlling remote %q before the update key change response", r.Subject)
	}
	p.drop = nil
	p.mustReady()
	if r := p.a.e.Status().Remote; r == nil || !r.Equal(idB.Certificate) {
		t.Fatalf("controlling remote %v", r)
	}

	// Controlled: another trusted station's request does not replace the
	// certificate of the association before its update key change request.
	p = newPair(t, func(_, b *Config) { b.Trust = trustAC })
	p.up()
	p.mustReady()
	cc, _ := configs()
	cc.Identity = idC
	c := p.endpoint(&station{}, cc)
	ar := c.Up(p.now)
	ap, err := deliver(p.b.e, p.now, ar)
	if err != nil || len(ap) == 0 {
		t.Fatalf("association response %d, %v", len(ap), err)
	}
	if r := p.b.e.Status().Remote; !r.Equal(idA.Certificate) {
		t.Fatalf("controlled remote %q before the update key change request", r.Subject)
	}
	// The same request again: the same response.
	again, err := deliver(p.b.e, p.now, ar)
	if err != nil || !same(ap, again) {
		t.Fatalf("repeated request: %v", err)
	}
	// The established station associates again, and its request wins.
	if err := p.a.e.Reassociate(); err != nil {
		t.Fatal(err)
	}
	p.tick()
	p.mustReady()
	if r := p.b.e.Status().Remote; !r.Equal(idA.Certificate) {
		t.Fatalf("controlled remote %q", r.Subject)
	}
	p.exchange()
}

// receiveAll passes frames received on connection owner to e.
func receiveAll(e *Endpoint, owner any, now time.Time, frames [][]byte) (out [][]byte, bound bool, err error) {
	for _, f := range frames {
		_, o, b, ferr := e.receiveAs(owner, now, f)
		out = append(out, o...)
		bound = bound || b
		if err == nil {
			err = ferr
		}
	}
	return out, bound, err
}

// A connection not bound to the association answers one Station
// Association per request timeout; a repetition gets the same answer.
func TestCandidateAssociationRequests(t *testing.T) {
	idC := mustIdentity(IdentityOptions{})
	p := newPair(t, func(_, b *Config) {
		b.Trust = Trust{Fingerprints: [][32]byte{Fingerprint(idA.Certificate), Fingerprint(idC.Certificate)}}
	})
	p.b.e.Up(p.now)
	ca, _ := configs()
	arA := p.endpoint(&station{}, ca).Up(p.now)
	ca.Identity = idC
	arC := p.endpoint(&station{}, ca).Up(p.now)

	ap, _, err := receiveAll(p.b.e, "c1", p.now, arA)
	if err != nil || len(ap) == 0 {
		t.Fatalf("association response %d, %v", len(ap), err)
	}
	again, _, err := receiveAll(p.b.e, "c1", p.now, arA)
	if err != nil || !same(ap, again) {
		t.Fatalf("repeated request: %v", err)
	}
	out, _, err := receiveAll(p.b.e, "c1", p.now, arC)
	if !errors.Is(err, ErrUnexpected) || len(out) != 0 {
		t.Fatalf("other request within the request timeout: %d out, %v", len(out), err)
	}
	p.now = p.now.Add(3 * time.Second)
	if out, _, err := receiveAll(p.b.e, "c1", p.now, arC); err != nil || len(out) == 0 {
		t.Fatalf("other request after the request timeout: %d out, %v", len(out), err)
	}
}

// Secure Data on a connection not bound to the association does not bind
// it under session keys that reached their limits.
func TestCandidateKeyUses(t *testing.T) {
	p := reachUses(t)
	for i := range 2 {
		out, err := p.a.e.Protect(p.now, appData)
		if err != nil {
			t.Fatal(err)
		}
		if _, bound, err := receiveAll(p.b.e, "c1", p.now, out); bound || !errors.Is(err, ErrUnexpected) {
			t.Fatalf("message %d: bound %v, %v", i, bound, err)
		}
	}
	if c := p.b.count(StatKeyInvalidationsCount); c != 1 {
		t.Fatalf("key invalidations %d", c)
	}
}

// A Station Association completed on a connection not bound to the
// association binds it without the session keys of before.
func TestCandidateAssociationEndsSession(t *testing.T) {
	p := newPair(t, nil)
	p.up()
	p.mustReady()
	old, err := p.a.e.Protect(p.now, appData) // never delivered
	if err != nil {
		t.Fatal(err)
	}
	ca, _ := configs()
	c := p.endpoint(&station{}, ca)
	q := c.Up(p.now)
	bound := false
	for i := 0; i < 10 && !bound; i++ {
		var out [][]byte
		out, bound, err = receiveAll(p.b.e, "c1", p.now, q)
		if err != nil {
			t.Fatal(err)
		}
		q = step(t, c, p.now, out)
	}
	if !bound || !p.b.e.Owns("c1") {
		t.Fatal("not bound by the association")
	}
	if p.b.e.Ready() {
		t.Fatal("ready with the session keys of before")
	}
	if _, _, err := receiveAll(p.b.e, "c1", p.now, old); !errors.Is(err, ErrUnexpected) {
		t.Fatalf("secure data under the keys of before: %v", err)
	}
	// The Session Key Change follows on the bound connection.
	for i := 0; i < 10 && len(q) > 0; i++ {
		out, _, err := receiveAll(p.b.e, "c1", p.now, q)
		if err != nil {
			t.Fatal(err)
		}
		q = step(t, c, p.now, out)
	}
	if !c.Ready() || !p.b.e.Ready() {
		t.Fatalf("no session: %+v, %+v", c.Status(), p.b.e.Status())
	}
}

// selfSigned makes a self-signed P-256 certificate with the key usage ku.
func selfSigned(t *testing.T, ku x509.KeyUsage) *x509.Certificate {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "station"},
		NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), KeyUsage: ku}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &k.PublicKey, k)
	if err != nil {
		t.Fatal(err)
	}
	c, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestTrustKeyUsage(t *testing.T) {
	for _, c := range []struct {
		name string
		ku   x509.KeyUsage
		ok   bool
	}{
		{"absent", 0, true},
		{"key agreement", x509.KeyUsageKeyAgreement, true},
		{"digital signature", x509.KeyUsageDigitalSignature, true},
		{"certificate signing only", x509.KeyUsageCertSign, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			cert := selfSigned(t, c.ku)
			tr := Trust{Fingerprints: [][32]byte{Fingerprint(cert)}}
			_, _, notAuth, err := tr.verify(cert.Raw, P256, time.Now(), false)
			if c.ok != (err == nil) || notAuth || err != nil && !errors.Is(err, ErrCertificate) {
				t.Errorf("verify: %v (not authorized %v)", err, notAuth)
			}
		})
	}
}

func TestTrustAuthorityValidity(t *testing.T) {
	now := time.Now()
	name := pkix.Name{CommonName: "CA"}
	old := mustIdentity(IdentityOptions{CA: true, Subject: name, NotBefore: now.AddDate(-2, 0, 0), NotAfter: now.AddDate(-1, 0, 0)})
	station := mustIdentity(IdentityOptions{Issuer: old, Subject: pkix.Name{CommonName: "station"}})
	// The authority renewed under the same name and key.
	tmpl := *old.Certificate
	tmpl.NotBefore, tmpl.NotAfter = now.Add(-time.Minute), now.AddDate(1, 0, 0)
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, old.Key.(*ecdsa.PrivateKey).Public(), old.Key)
	if err != nil {
		t.Fatal(err)
	}
	renewed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}

	expired := Trust{Authorities: []*x509.Certificate{old.Certificate}}
	if _, _, notAuth, err := expired.verify(station.Certificate.Raw, P256, now, false); err == nil || notAuth ||
		!bytes.Contains([]byte(err.Error()), []byte("validity")) {
		t.Errorf("expired authority: %v (not authorized %v)", err, notAuth)
	}
	if _, _, _, err := expired.verify(station.Certificate.Raw, P256, now, true); err != nil {
		t.Errorf("expired authority, validity ignored: %v", err)
	}
	both := Trust{Authorities: []*x509.Certificate{old.Certificate, renewed}}
	if _, _, _, err := both.verify(station.Certificate.Raw, P256, now, false); err != nil {
		t.Errorf("renewed authority: %v", err)
	}
}
