// SPDX-License-Identifier: MIT

package telecontrol

import (
	"context"
	"errors"
	"testing"
	"time"

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

// While Send has not returned the request takes nothing from its queue:
// the queue holds no more than MaxResult and a small margin, and what is
// beyond it ends the request with ErrResultLimit. A station that answers
// within the bound during Send completes the request.
func TestCollectQueueBound(t *testing.T) {
	const limit = 4
	data := func(n int) []*asdu.ASDU {
		out := make([]*asdu.ASDU, n)
		for i := range out {
			out[i] = asdu.New(asdu.CauseInterrogatedStation, 1, asdu.SinglePoint{IOA: asdu.IOA(i + 1)})
		}
		return out
	}
	for _, tc := range []struct {
		name   string
		answer func(req *asdu.ASDU) []*asdu.ASDU
		want   int
		err    error
	}{
		{"within the bound", func(req *asdu.ASDU) []*asdu.ASDU {
			out := append([]*asdu.ASDU{req.Reply(asdu.CauseActivationCon, false)}, data(limit)...)
			return append(out, req.Reply(asdu.CauseActivationTerm, false))
		}, limit, nil},
		{"data beyond the bound", func(req *asdu.ASDU) []*asdu.ASDU {
			return append([]*asdu.ASDU{req.Reply(asdu.CauseActivationCon, false)}, data(1000)...)
		}, limit, ErrResultLimit},
		{"mirrors beyond the bound", func(req *asdu.ASDU) []*asdu.ASDU {
			var out []*asdu.ASDU
			for range 1000 {
				out = append(out, req.Reply(asdu.CauseActivationTerm, false))
			}
			return append(out, req.Reply(asdu.CauseActivationCon, false), req.Reply(asdu.CauseActivationTerm, false))
		}, 0, ErrResultLimit},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &Requester{Params: p, Timeout: time.Second, MaxResult: limit}
			queued := 0
			r.Send = func(_ context.Context, req *asdu.ASDU) error {
				for _, a := range tc.answer(req) {
					r.Dispatch(a)
				}
				r.mu.Lock()
				x := r.exchanges[0]
				r.mu.Unlock()
				x.mu.Lock()
				queued = len(x.items)
				x.mu.Unlock()
				return nil
			}
			got, err := r.Interrogate(context.Background(), 1, asdu.QOIStation)
			if len(got) != tc.want || !errors.Is(err, tc.err) || (tc.err == nil) != (err == nil) {
				t.Fatalf("Interrogate: %d ASDUs, %v; want %d, %v", len(got), err, tc.want, tc.err)
			}
			if queued > limit+queueMargin {
				t.Errorf("%d ASDUs queued, want at most %d", queued, limit+queueMargin)
			}
		})
	}
}
