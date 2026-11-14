// SPDX-License-Identifier: MIT

package iec103

import (
	"errors"
	"math"
	"testing"
)

// MVAL is 13 bits of two's complement: a Raw in -4096..4095 encodes and
// decodes unchanged, any other is ErrInvalid instead of a different value
// on the wire.
func TestMeasurandRawBounds(t *testing.T) {
	for _, tc := range []struct {
		raw int16
		ok  bool
	}{
		{-4096, true}, {-1, true}, {0, true}, {1, true}, {4095, true},
		{-4097, false}, {4096, false}, {-5000, false}, {5000, false},
		{math.MinInt16, false}, {math.MaxInt16, false},
	} {
		a := New(CauseCyclic, 1, 160, 144, Measurands{Values: []MEA{{Raw: 1}, {Raw: tc.raw}}})
		b, err := a.AppendIn(nil, nil)
		if !tc.ok {
			if !errors.Is(err, ErrInvalid) {
				t.Errorf("Raw %d: %v, want ErrInvalid", tc.raw, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("Raw %d: %v", tc.raw, err)
			continue
		}
		d, err := DecodeIn(b, nil)
		if err != nil {
			t.Fatalf("Raw %d: decode: %v", tc.raw, err)
		}
		if got := d.Body.(Measurands).Values[1].Raw; got != tc.raw {
			t.Errorf("Raw %d decodes as %d", tc.raw, got)
		}
	}
}
