// SPDX-License-Identifier: MIT

package iec103

import (
	"encoding/binary"
	"errors"
	"math"
	"strings"
	"testing"
)

// TestI16AllPatterns checks the octet-wise two's complement codec of
// DisturbanceValues against encoding/binary for every 16-bit pattern.
func TestI16AllPatterns(t *testing.T) {
	for w := range 1 << 16 {
		want := binary.LittleEndian.AppendUint16(nil, uint16(w))
		r := reader{b: want}
		v := r.i16()
		if got := appendI16(nil, v); string(got) != string(want) {
			t.Fatalf("%#04x: % x, want % x", w, got, want)
		}
		if int(v) != w-(w&0x8000)<<1 {
			t.Fatalf("%#04x decodes to %d", w, v)
		}
	}
}

// TestMEAAllWords checks that every word of a measurand with quality
// descriptor (clause 7.2.6.8) decodes and encodes back to itself apart from
// the spare bit RES, and that Raw is the signed 13-bit MVAL.
func TestMEAAllWords(t *testing.T) {
	for w := range 1 << 16 {
		m := meaOf(uint16(w))
		mval := w >> 3
		if want := mval - (mval&0x1000)<<1; int(m.Raw) != want {
			t.Fatalf("%#04x: Raw %d, want %d", w, m.Raw, want)
		}
		if got := m.word(); int(got) != w&^4 {
			t.Fatalf("%#04x: word %#04x", w, got)
		}
	}
}

// TestIntsRoundTrip checks Ints and Value.Int for each width and the
// extreme values of that width.
func TestIntsRoundTrip(t *testing.T) {
	for size := 1; size <= 8; size++ {
		lo, hi := int64(-1)<<(8*size-1), int64(1)<<(8*size-1)-1
		if size == 8 {
			lo, hi = math.MinInt64, math.MaxInt64
		}
		vs := []int64{lo, -1, 0, 1, hi}
		v := Ints(size, vs...)
		if want := strings.Repeat("\x00", size-1) + "\x80"; len(v.Data) != size*len(vs) || string(v.Data[:size]) != want {
			t.Fatalf("size %d: % x", size, v.Data)
		}
		for i, want := range vs {
			if got, err := v.Int(i); err != nil || got != want {
				t.Fatalf("size %d element %d: %d %v, want %d", size, i, got, err, want)
			}
		}
	}
}

// TestGDDCountsDoNotTruncate checks that a DATASIZE or NUMBER beyond one
// octet is refused when encoding, not announced modulo 256.
func TestGDDCountsDoNotTruncate(t *testing.T) {
	for name, v := range map[string]Value{
		"ascii":   ASCII(strings.Repeat("x", 256+5)),
		"uints":   Uints(DataUint, 1, make([]uint64, 256+1)...),
		"size":    Uints(DataUint, 256+1, 1),
		"float32": Float32s(make([]float32, 256+3)...),
		"float64": Float64s(make([]float64, 256+2)...),
	} {
		if v.Size != 0 && v.Number != 0 {
			t.Fatalf("%s: %+v", name, v.GDD)
		}
		if _, err := appendValue(nil, v); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if v := ASCII(strings.Repeat("x", 255)); v.Size != 255 {
		t.Fatal(v.GDD)
	}
}
