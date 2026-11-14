// SPDX-License-Identifier: MIT

package ft12

import (
	"errors"
	"io"
	"testing"
	"testing/synctest"
	"time"
)

// TestDecoderGap: octets that came before a pause longer than Gap are not
// completed by the octets after it. Two cases precede a whole fixed frame
// with a pause: the start of a variable frame with L = 255 that never ends
// (line noise, or a peer that stopped in the middle), and a rejected
// variable header with L = 255 whose rest never comes. With Gap the fixed
// frame is decoded; without it the decoder still waits for the rest and
// the stream ends first. The pause runs on the fake clock of
// testing/synctest.
func TestDecoderGap(t *testing.T) {
	p := Params{AddrSize: 1}
	want := Frame{Kind: KindFixed, Control: 0x49, Addr: 3}
	fixed, err := want.Encode(p)
	if err != nil {
		t.Fatal(err)
	}
	const gap = 20 * time.Millisecond
	for _, c := range []struct {
		name   string
		before []byte
		why    error // the error the octets before the pause are discarded with
	}{
		{"unfinished frame", []byte{StartVariable, 0xFF, 0xFF, StartVariable, 0x73, 0x03}, ErrShort},
		{"rejected header", []byte{StartVariable, 0xFF, 0xFF, 0x00}, ErrMalformed},
	} {
		for _, g := range []time.Duration{0, gap} {
			synctest.Test(t, func(t *testing.T) {
				d := NewDecoder(&chunks{parts: [][]byte{c.before, fixed}, pause: map[int]time.Duration{1: 3 * gap}}, p)
				d.Gap = g
				var discarded []byte
				var why error
				d.OnDiscard = func(b []byte, err error) { discarded, why = append(discarded, b...), err }
				f, err := d.Next()
				if g == 0 {
					// The fixed frame is taken as the rest of the frame
					// before it, and the stream ends.
					if err == nil {
						t.Errorf("%s without Gap: %v, want the stream to end inside the frame", c.name, f)
					}
					return
				}
				if err != nil || f.Kind != want.Kind || f.Control != want.Control || f.Addr != want.Addr {
					t.Fatalf("%s with Gap: %v %v, want %v", c.name, f, err, want)
				}
				if len(discarded) != len(c.before) || !errors.Is(why, c.why) {
					t.Errorf("%s: discarded % X (%v), want the %d octets before the pause with %v", c.name, discarded, why, len(c.before), c.why)
				}
			})
		}
	}
}

// TestDecoderGapKeepsAFrameWithoutPause: the rest of a rejected frame that
// arrives within Gap is still dropped whole.
func TestDecoderGapKeepsAFrameWithoutPause(t *testing.T) {
	p := Params{AddrSize: 1}
	b, _ := withE5.Encode(p)
	b[3] ^= 0x55 // detected after four octets
	next, _ := Frame{Kind: KindFixed, Control: 0x20, Addr: 1}.Encode(p)
	synctest.Test(t, func(t *testing.T) {
		gap := 20 * time.Millisecond
		d := NewDecoder(&chunks{parts: [][]byte{b[:4], b[4:], next}, pause: map[int]time.Duration{1: gap / 2}}, p)
		d.Gap = gap
		var discarded int
		d.OnDiscard = func(b []byte, _ error) { discarded += len(b) }
		f, err := d.Next()
		if err != nil || f.Kind != KindFixed {
			t.Fatalf("%v %v, want the fixed frame", f, err)
		}
		if discarded != len(b) {
			t.Errorf("discarded %d octets, want the %d of the rejected frame", discarded, len(b))
		}
	})
}

// TestDecoderResetForgetsARejectedFrame: after Reset the decoder neither
// drops the rest of a rejected frame nor refuses a single character.
func TestDecoderResetForgetsARejectedFrame(t *testing.T) {
	p := Params{AddrSize: 1}
	fixed, _ := Frame{Kind: KindFixed, Control: 0x20, Addr: 1}.Encode(p)
	for _, c := range []struct {
		name  string
		after []byte
		want  Kind
	}{
		{"fixed frame", fixed, KindFixed},
		{"single character", []byte{SingleChar}, KindSingle},
	} {
		r := &chunks{parts: [][]byte{{StartVariable, 0xFF, 0xFF, 0x00}}}
		d := NewDecoder(r, p)
		if _, err := d.Next(); !errors.Is(err, io.EOF) {
			t.Fatalf("%s: rejected header alone: %v, want io.EOF", c.name, err)
		}
		d.Reset()
		r.parts = [][]byte{c.after}
		f, err := d.Next()
		if err != nil || f.Kind != c.want {
			t.Errorf("%s after Reset: %v %v, want a frame of kind %v", c.name, f, err, c.want)
		}
	}
}
