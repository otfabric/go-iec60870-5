// SPDX-License-Identifier: MIT

package sec

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/otfabric/go-iec60870-5/asdu"
)

// Length fields at the edges of their octets: the largest value that fits
// is encoded, the next one is refused, never wrapped.
func TestAppendLenLimits(t *testing.T) {
	for _, c := range []struct {
		size, n int
		want    []byte // nil: refused
	}{
		{1, 0, []byte{0}},
		{1, 255, []byte{0xff}},
		{1, 256, nil},
		{2, 256, []byte{0x00, 0x01}},
		{2, 65535, []byte{0xff, 0xff}},
		{2, 65536, nil},
	} {
		got, err := appendLen(nil, make([]byte, c.n), c.size, "test")
		switch {
		case c.want == nil && !errors.Is(err, ErrMessage):
			t.Errorf("%d octets in %d: %v, want ErrMessage", c.n, c.size, err)
		case c.want != nil && (err != nil || !bytes.Equal(got, c.want)):
			t.Errorf("%d octets in %d: % X, %v, want % X", c.n, c.size, got, err, c.want)
		}
	}
}

func TestProtectLengthLimits(t *testing.T) {
	key := seq(32, 0xa0)
	for _, code := range []uint8{HMACSHA256.Code, AES256GCM} {
		dp, _ := DataProtectionByCode(code)
		h := Header{CommonAddr: 1}
		if _, err := protect(asdu.IEC104, h, SecureData{AIM: 1, AIS: 1}, make([]byte, 0x10000), dp, key); !errors.Is(err, ErrMessage) {
			t.Errorf("%s: 65536 octets: %v, want ErrMessage", dp, err)
		}
		w, err := protect(asdu.IEC104, h, SecureData{AIM: 1, AIS: 1}, make([]byte, 0xffff), dp, key)
		if err != nil {
			t.Fatalf("%s: 65535 octets: %v", dp, err)
		}
		_, m, err := Decode(w, asdu.IEC104, Control)
		if err != nil {
			t.Fatalf("%s: decode: %v", dp, err)
		}
		plain, err := open(w, asdu.IEC104, m.(SecureData), dp, key)
		if err != nil || len(plain) != 0xffff {
			t.Errorf("%s: open: %d octets, %v", dp, len(plain), err)
		}
	}
}

func TestSessionInitiationRandomLimits(t *testing.T) {
	for _, n := range []int{3, 4, 64, 65, 255, 256} {
		m := SessionInitiationRequest{AIM: 1, AIS: 2, Random: make([]byte, n)}
		b, err := m.appendBody(nil, false)
		if valid := n >= 4 && n <= 64; valid != (err == nil) {
			t.Errorf("%d octets of random data: %v", n, err)
			continue
		}
		if err == nil && int(b[4]) != n {
			t.Errorf("%d octets of random data: length octet %d", n, b[4])
		}
	}
}

func TestWrapStep(t *testing.T) {
	for _, c := range []struct{ n, j, i int }{{2, 0, 1}, {2, 5, 2}, {64, 5, 64}} {
		if got := wrapStep(c.n, c.j, c.i); got != uint64(c.n*c.j+c.i) {
			t.Errorf("wrapStep(%d, %d, %d) = %d", c.n, c.j, c.i, got)
		}
	}
	if got := wrapStep(-1, 1, 0); got != 0 {
		t.Errorf("negative step = %d, want 0", got)
	}
	// Wrapping keys of every length the protocol carries round-trips.
	kek := seq(32, 0x10)
	for n := 16; n <= 64; n += 8 {
		data := seq(n, byte(n))
		w, err := keyWrap(kek, data)
		if err != nil {
			t.Fatal(err)
		}
		back, err := keyUnwrap(kek, w)
		if err != nil || !bytes.Equal(back, data) {
			t.Errorf("%d octets: %x, %v", n, back, err)
		}
		w[len(w)-1] ^= 1
		if _, err := keyUnwrap(kek, w); err == nil {
			t.Errorf("%d octets: unwrapped altered data", n)
		}
	}
}

// LoadIdentity cleans the paths it is given; a path through a directory
// and back resolves to the same files, and a directory is not a key.
func TestLoadIdentityPaths(t *testing.T) {
	certPEM, keyPEM, err := mustIdentity(IdentityOptions{}).PEM()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	cf, kf := filepath.Join(dir, "c.pem"), filepath.Join(dir, "k.pem")
	if os.WriteFile(cf, certPEM, 0o600) != nil || os.WriteFile(kf, keyPEM, 0o600) != nil {
		t.Fatal("writing the PEM files")
	}
	if _, err := LoadIdentity(sub+"/../c.pem", sub+"/./../k.pem"); err != nil {
		t.Errorf("path through a directory: %v", err)
	}
	if _, err := LoadIdentity(cf, sub); err == nil {
		t.Error("loaded a directory as the key")
	}
	if _, err := LoadIdentity(kf, cf); err == nil {
		t.Error("loaded a key as the certificate")
	}
}

// A reassembled message is at most a data unit identifier and
// maxMessageFields octets: one of exactly that length completes; in a longer
// one the segment that crosses the bound abandons the series, and it and
// every later segment of the series count as discarded.
func TestReassemblyLengthLimit(t *testing.T) {
	head := Header{Type: asdu.S_AQ_NA_1, Cause: asdu.CauseAssociation, CommonAddr: 1}.append(nil, asdu.IEC104)
	for _, c := range []struct {
		fields int
		whole  bool
	}{
		{1, true},
		{maxMessageFields - 1, true},
		{maxMessageFields, true},
		{maxMessageFields + 1, false},
		{2 * maxMessageFields, false},
	} {
		msg := append(bytes.Clone(head), seq(c.fields, 0x5a)...)
		segs, _, err := Segment(msg, asdu.IEC104, 249, 0)
		if err != nil {
			t.Fatal(err)
		}
		var discarded int
		r := reassembler{p: asdu.IEC104, discarded: func() { discarded++ }}
		var got []byte
		fields, want := 0, 0
		for _, s := range segs {
			if fields += len(s) - len(head) - 1; fields > maxMessageFields {
				want++
			}
			if w := r.add(s); w != nil {
				got = w
			}
			if len(r.buf) > maxMessageFields {
				t.Fatalf("%d octets: %d octets kept", c.fields, len(r.buf))
			}
		}
		switch {
		case c.whole && (!bytes.Equal(got, msg) || discarded != 0):
			t.Errorf("%d octets of fields: reassembled %d octets, %d discarded, want the message", c.fields, len(got), discarded)
		case !c.whole && (got != nil || discarded != want || r.busy):
			t.Errorf("%d octets of fields: reassembled %d octets, %d discarded, busy %v, want the series abandoned and %d discarded",
				c.fields, len(got), discarded, r.busy, want)
		}
	}
}

// A series of segments that never ends is abandoned at maxMessageFields:
// what a controlled station allocates for it stays within a small multiple
// of that bound, however many segments follow, and the series is counted as
// discarded.
func TestReassemblyAllocationBudget(t *testing.T) {
	p := newPair(t, nil)
	p.up()
	p.mustReady()
	const segments = 20000 // about 4.8 MB of fields, 70 times the bound
	head := Header{Type: asdu.S_AQ_NA_1, Cause: asdu.CauseAssociation, CommonAddr: 0x1234}.append(nil, asdu.IEC104)
	n := len(head)
	seg := append(append(bytes.Clone(head), 0), seq(249-n-1, 0x33)...)
	before := p.b.count(StatDiscardedMessages)
	var ms runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&ms)
	start := ms.TotalAlloc
	for i := range segments {
		seg[n] = ASC{FIR: i == 0, Seq: uint8(i)}.Byte()
		if data, _, _ := p.b.e.Receive(p.now, seg); data != nil {
			t.Fatalf("segment %d completed a message", i)
		}
	}
	runtime.ReadMemStats(&ms)
	allocated := ms.TotalAlloc - start
	if budget := uint64(8 * maxMessageFields); allocated > budget {
		t.Errorf("%d segments without FIN allocated %d octets, want at most %d", segments, allocated, budget)
	}
	if kept := len(p.b.e.reasm.buf); kept > maxMessageFields {
		t.Errorf("%d octets kept, want at most %d", kept, maxMessageFields)
	}
	if got := p.b.count(StatDiscardedMessages) - before; got == 0 {
		t.Error("the abandoned series was not counted as discarded")
	}
	// The association goes on.
	p.exchange()
}
