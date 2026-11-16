// SPDX-License-Identifier: MIT

package client

import (
	"errors"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/otfabric/go-iec60870-5/iec103"
	"github.com/otfabric/go-iec60870-5/iec103/server"
	"github.com/otfabric/go-iec60870-5/internal/testutil/serialline"
	"github.com/otfabric/go-iec60870-5/link"
)

// The notifications are told one at a time and in the order of the
// changes, StateClosed last, also when the link goes down while Close runs
// on another goroutine: Close, called while OnState runs on the link loop
// for the link going down, reports StateClosed only after that call has
// returned, and returns after it. It runs on the fake clock of
// testing/synctest, whose Wait lets every goroutine get as far as it can
// before the test looks.
func TestStateNotificationsInOrder(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		check := serialline.Goroutines(t)
		a, b := serialline.Pair()
		lc := link.Config{Timeout: 50 * time.Millisecond, Retries: 3}
		srv, err := server.New(b, server.Config{Link: lc, LinkAddr: 3, Fun: iec103.FunDistance, SyncDelay: -1})
		if err != nil {
			t.Fatal(err)
		}
		var log serialline.States[iec103.State]
		downEntered, release := make(chan struct{}), make(chan struct{})
		c, err := New(a, Config{Link: lc, LinkAddr: 3, NoInitGI: true, NoInitTimeSync: true,
			OnState: func(s iec103.State, _ error) {
				log.Enter(s)
				defer log.Leave()
				if s == iec103.StateDown {
					close(downEntered)
					<-release
				}
			}})
		if err != nil {
			t.Fatal(err)
		}
		if err := c.WaitUp(t.Context()); err != nil {
			t.Fatal(err)
		}
		_ = srv.Close() // the line fails under the client
		<-downEntered
		closed := make(chan error, 1)
		go func() { closed <- c.Close() }()
		synctest.Wait()
		states, overlap := log.Get()
		if overlap {
			t.Errorf("OnState called while another call had not returned: %v", states)
		}
		if !slices.Equal(states, []iec103.State{iec103.StateUp, iec103.StateDown}) {
			t.Errorf("states %v while OnState(down) runs, want [up down]", states)
		}
		select {
		case err := <-closed:
			t.Errorf("Close returned (%v) while OnState(down) had not returned", err)
			closed <- err // for the receive below
		default:
		}
		close(release)
		if err := <-closed; err != nil && !errors.Is(err, serialline.ErrClosed) {
			t.Errorf("Close: %v", err)
		}
		states, overlap = log.Get()
		if overlap || !slices.Equal(states, []iec103.State{iec103.StateUp, iec103.StateDown, iec103.StateClosed}) {
			t.Errorf("states %v (overlap %v), want [up down closed] one at a time", states, overlap)
		}
		check()
	})
}
