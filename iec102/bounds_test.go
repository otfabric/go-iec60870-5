// SPDX-License-Identifier: MIT

package iec102_test

import (
	"errors"
	"testing"

	"github.com/otfabric/go-iec60870-5/iec102"
)

// The single-point qualifier SPQ is seven bits (bits 1-7 of the octet):
// 0..127 encode and decode unchanged, a larger Qualifier is ErrInvalid
// instead of losing its high bit.
func TestSinglePointQualifierBounds(t *testing.T) {
	for _, tc := range []struct {
		q  uint8
		ok bool
	}{{0, true}, {1, true}, {126, true}, {127, true}, {128, false}, {200, false}, {255, false}} {
		a := iec102.New(iec102.CauseRequest, 1, 50, iec102.SinglePoints{Points: []iec102.SinglePoint{{Addr: 7, On: true, Qualifier: tc.q}}})
		raw, err := a.Encode(iec102.Params{})
		if !tc.ok {
			if !errors.Is(err, iec102.ErrInvalid) {
				t.Errorf("Qualifier %d: %v, want ErrInvalid", tc.q, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("Qualifier %d: %v", tc.q, err)
			continue
		}
		d, err := iec102.Decode(raw, iec102.Params{})
		if err != nil {
			t.Fatalf("Qualifier %d: decode: %v", tc.q, err)
		}
		if p := d.Body.(iec102.SinglePoints).Points[0]; p.Qualifier != tc.q || !p.On {
			t.Errorf("Qualifier %d: decoded %+v", tc.q, p)
		}
	}
}
