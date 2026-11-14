// SPDX-License-Identifier: MIT

package sec

import (
	"bytes"
	"testing"
	"time"

	"github.com/otfabric/go-iec60870-5/asdu"
)

// FuzzDecode feeds arbitrary octets to the security message decoder and
// to both roles of an Endpoint with an established session: every input
// is handled, what decodes must encode to the same octets, and the segments
// both Endpoints keep for reassembly never exceed maxMessageFields, however
// many inputs continue a series.
func FuzzDecode(f *testing.F) {
	for _, g := range golden {
		f.Add(unhex(f, g.hex))
	}
	p := &pair{t: &testing.T{}, now: time.Now(), a: &station{}, b: &station{}}
	ca, cb := configs()
	var err error
	if p.a.e, err = New(ca); err != nil {
		f.Fatal(err)
	}
	if p.b.e, err = New(cb); err != nil {
		f.Fatal(err)
	}
	p.up()
	if !p.until(p.ready, time.Minute) {
		f.Fatal("no session")
	}
	for _, s := range []*station{p.a, p.b} {
		out, _ := s.e.Protect(p.now, appData)
		f.Add(out[0])
	}
	// The first and a later segment of a series without its last one.
	head := Header{Type: asdu.S_AQ_NA_1, Cause: asdu.CauseAssociation, CommonAddr: 0x1234}.append(nil, asdu.IEC104)
	for _, asc := range []ASC{{FIR: true}, {Seq: 1}} {
		f.Add(append(append(bytes.Clone(head), asc.Byte()), seq(242, 0x33)...))
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		for _, d := range []Direction{Control, Monitor} {
			h, m, err := Decode(b, asdu.IEC104, d)
			if err != nil {
				continue
			}
			again, err := Encode(h, asdu.IEC104, m)
			if err != nil || !bytes.Equal(again, b) {
				t.Fatalf("%x decoded as %+v, encoded as %x, %v", b, m, again, err)
			}
		}
		_, _, _ = p.a.e.Receive(p.now, b)
		_, _, _ = p.b.e.Receive(p.now, b)
		for _, e := range []*Endpoint{p.a.e, p.b.e} {
			if n := len(e.reasm.buf); n > maxMessageFields {
				t.Fatalf("%s keeps %d octets for reassembly, more than %d", e.Role(), n, maxMessageFields)
			}
		}
	})
}
