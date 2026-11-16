// SPDX-License-Identifier: MIT

package server

import (
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/otfabric/go-iec60870-5/ft12"
	"github.com/otfabric/go-iec60870-5/iec103"
	"github.com/otfabric/go-iec60870-5/internal/testutil/serialline"
	"github.com/otfabric/go-iec60870-5/link"
)

// The notifications are told one at a time and in the order of the
// changes, StateClosed last, also on a transport that is not an io.Closer,
// whose pending Read Close does not wait for: Close, called while OnState
// runs on the link's reader for the reset of the communication unit, reports
// StateClosed only after that call has returned, and returns after it. It
// runs on the fake clock of testing/synctest, whose Wait lets every
// goroutine get as far as it can before the test looks.
func TestStateNotificationsInOrder(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		check := serialline.Goroutines(t)
		a, b := serialline.Pair()
		var log serialline.States[iec103.State]
		upEntered, release := make(chan struct{}), make(chan struct{})
		s, err := New(serialline.NoCloser{ReadWriter: b}, Config{LinkAddr: 3, Fun: 128, OnState: func(st iec103.State, _ error) {
			log.Enter(st)
			defer log.Leave()
			if st == iec103.StateUp {
				close(upEntered)
				<-release
			}
		}})
		if err != nil {
			t.Fatal(err)
		}
		pri, err := link.NewPrimary(a, link.Config{FT12: ft12.Params{AddrSize: 1}, Timeout: 100 * time.Millisecond, Retries: 1})
		if err != nil {
			t.Fatal(err)
		}
		reset := make(chan struct{})
		go func() { defer close(reset); _, _ = pri.ResetLink(t.Context(), 3) }() // answered or not
		<-upEntered
		closed := make(chan error, 1)
		go func() { closed <- s.Close() }()
		synctest.Wait()
		states, overlap := log.Get()
		if overlap {
			t.Errorf("OnState called while another call had not returned: %v", states)
		}
		if !slices.Equal(states, []iec103.State{iec103.StateUp}) {
			t.Errorf("states %v while OnState(up) runs, want [up]", states)
		}
		select {
		case err := <-closed:
			t.Errorf("Close returned (%v) while OnState(up) had not returned", err)
			closed <- err // for the receive below
		default:
		}
		close(release)
		if err := <-closed; err != nil {
			t.Errorf("Close: %v", err)
		}
		states, overlap = log.Get()
		if overlap || !slices.Equal(states, []iec103.State{iec103.StateUp, iec103.StateClosed}) {
			t.Errorf("states %v (overlap %v), want [up closed] one at a time", states, overlap)
		}
		_ = pri.Close() // ends the line, and with it the server's pending Read
		<-reset
		check()
	})
}
