// SPDX-License-Identifier: MIT

package ft12

import (
	"errors"
	"io"
	"testing"
	"time"
)

// TestDecoderGap is the regression test of a decoder that took the octets
// of the next frames as the rest of a frame that never ended: a start of a
// variable frame with L = 255 (line noise, or a peer that stopped in the
// middle), a pause, then a whole fixed frame. With Gap the stale octets are
// discarded with ErrShort and the fixed frame is decoded.
func TestDecoderGap(t *testing.T) {
	p := Params{AddrSize: 1}
	want := Frame{Kind: KindFixed, Control: 0x49, Addr: 3}
	fixed, err := want.Encode(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, gap := range []time.Duration{0, 20 * time.Millisecond} {
		r, w := io.Pipe()
		d := NewDecoder(r, p)
		d.Gap = gap
		var discarded []byte
		var why error
		d.OnDiscard = func(b []byte, err error) { discarded, why = append(discarded, b...), err }
		go func() {
			_, _ = w.Write([]byte{StartVariable, 0xFF, 0xFF, StartVariable, 0x73, 0x03})
			time.Sleep(60 * time.Millisecond)
			_, _ = w.Write(fixed)
			time.Sleep(200 * time.Millisecond)
			_ = w.Close()
		}()
		f, err := d.Next()
		switch gap {
		case 0:
			// Without the check the fixed frame is swallowed and the
			// stream ends inside the long frame.
			if !errors.Is(err, io.ErrUnexpectedEOF) {
				t.Errorf("without Gap: %v %v, want io.ErrUnexpectedEOF", f, err)
			}
		default:
			if err != nil || f.Kind != want.Kind || f.Control != want.Control || f.Addr != want.Addr {
				t.Fatalf("with Gap: %v %v, want %v", f, err, want)
			}
			if len(discarded) != 6 || !errors.Is(why, ErrShort) {
				t.Errorf("discarded % X (%v), want the 6 stale octets with ErrShort", discarded, why)
			}
		}
		_ = r.Close()
	}
}
