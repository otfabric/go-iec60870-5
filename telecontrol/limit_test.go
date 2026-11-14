// SPDX-License-Identifier: MIT

package telecontrol

import (
	"context"
	"errors"
	"testing"

	"github.com/otfabric/go-iec60870-5/asdu"
)

// An interrogation answered by more process data than MaxResult ends at
// the bound with ErrResultLimit and the ASDUs so far; at most MaxResult
// ASDUs, and the termination, complete it; zero is no bound.
func TestCollectResultLimit(t *testing.T) {
	answer := func(n int, term bool) func(req *asdu.ASDU) []*asdu.ASDU {
		return func(req *asdu.ASDU) []*asdu.ASDU {
			out := []*asdu.ASDU{req.Reply(asdu.CauseActivationCon, false)}
			for i := range n {
				out = append(out, asdu.New(asdu.CauseInterrogatedStation, 1, asdu.SinglePoint{IOA: asdu.IOA(i + 1)}))
			}
			if term {
				out = append(out, req.Reply(asdu.CauseActivationTerm, false))
			}
			return out
		}
	}
	for _, tc := range []struct {
		name      string
		max, sent int
		term      bool
		want      int
		err       error
	}{
		{"below the bound", 4, 3, true, 3, nil},
		{"at the bound", 4, 4, true, 4, nil},
		{"one beyond", 4, 5, true, 4, ErrResultLimit},
		{"far beyond, no termination", 4, 100, false, 4, ErrResultLimit},
		{"no bound", 0, 100, true, 100, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := scripted(answer(tc.sent, tc.term))
			r.MaxResult = tc.max
			got, err := r.Interrogate(context.Background(), 1, asdu.QOIStation)
			if len(got) != tc.want || !errors.Is(err, tc.err) || (tc.err == nil) != (err == nil) {
				t.Fatalf("Interrogate: %d ASDUs, %v; want %d, %v", len(got), err, tc.want, tc.err)
			}
		})
	}
}
