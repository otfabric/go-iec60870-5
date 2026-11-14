// SPDX-License-Identifier: MIT

package server

import (
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/otfabric/go-iec60870-5/asdu"
)

// Close waits for the goroutine of each event queue: it does not return
// while that goroutine is held at its end, and returns once it is let go.
// It runs in a bubble of testing/synctest, so "Close has not returned" is
// checked when every goroutine is blocked, not after a delay.
func TestCloseWaitsForEventQueues(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		reached, release := make(chan struct{}), make(chan struct{})
		s, err := New(nil, func(o *options) {
			o.failpoint = func(name string) {
				if name == "pump-end" {
					close(reached)
					<-release
				}
			}
		})
		if err != nil {
			t.Fatal(err)
		}
		// The event starts the queue's goroutine, which waits for an
		// active connection.
		if err := s.Enqueue(asdu.New(asdu.CauseSpontaneous, 1, asdu.SinglePoint{IOA: 1, Value: true})); err != nil {
			t.Fatal(err)
		}
		closed := make(chan error, 1)
		go func() { closed <- s.Close() }()
		<-reached
		synctest.Wait()
		select {
		case err := <-closed:
			t.Fatalf("Close returned (%v) while the goroutine of an event queue still runs", err)
		default:
		}
		close(release)
		if err := <-closed; err != nil {
			t.Fatal(err)
		}
	})
}

// An event queued after Close starts no goroutine: the queue has nothing
// to send to.
func TestNoEventQueueAfterClose(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var ran atomic.Bool
		s, err := New(nil, func(o *options) { o.failpoint = func(string) { ran.Store(true) } })
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		for _, g := range s.groups {
			g.enqueue([]byte{1}) // past the check of Enqueue
		}
		synctest.Wait()
		if ran.Load() {
			t.Error("an event queued after Close started the goroutine of its queue")
		}
	})
}
