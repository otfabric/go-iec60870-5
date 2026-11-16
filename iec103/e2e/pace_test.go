// SPDX-License-Identifier: MIT

package e2e

import (
	"errors"
	"testing"

	"github.com/otfabric/go-iec60870-5/iec103"
	"github.com/otfabric/go-iec60870-5/iec103/server"
)

// TestPacedChannels transfers a record whose channels (20 ASDUs 30 each)
// are longer than the class 1 queue (4). With PaceChannels the server
// queues each channel as the client's polls take the queue and the
// transfer completes; without it the record is not announced (TOO 34,
// the v0.7.0 bound, kept) and the transfer ends aborted.
func TestPacedChannels(t *testing.T) {
	for _, pace := range []bool{true, false} {
		r := start(t, options{noInit: true, handler: true, server: func(sc *server.Config) {
			sc.QueueSize = 4
			sc.PaceChannels = pace
		}})
		want := r.rec.rec
		want.Tags = want.Tags[:2]
		for i := range want.Channels {
			ch := &want.Channels[i]
			ch.Values = ch.Values[:0]
			for v := range 20 * iec103.MaxValues {
				ch.Values = append(ch.Values, int16(v*int(ch.ACC)))
			}
		}
		rec, err := r.c.Disturbance(ctx(t), fun, 7)
		if !pace {
			if !errors.Is(err, iec103.ErrAborted) {
				t.Fatalf("without PaceChannels: %v, want ErrAborted", err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("with PaceChannels: %v", err)
		}
		if len(rec.Channels) != 2 {
			t.Fatalf("%d channels", len(rec.Channels))
		}
		for i, ch := range rec.Channels {
			w := want.Channels[i]
			if ch.ACC != w.ACC || len(ch.Values) != len(w.Values) || ch.Values[len(ch.Values)-1] != w.Values[len(w.Values)-1] {
				t.Fatalf("channel %d: ACC %d, %d values", i, ch.ACC, len(ch.Values))
			}
		}
		if acks := r.rec.waitAcks(t); len(acks) != 1 || !acks[0] {
			t.Fatalf("acknowledgements %v", acks)
		}
	}
}
