// SPDX-License-Identifier: MIT

package e2e

import (
	"errors"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/otfabric/go-iec60870-5/iec101"
	"github.com/otfabric/go-iec60870-5/iec101/client"
	"github.com/otfabric/go-iec60870-5/iec101/server"
	"github.com/otfabric/go-iec60870-5/internal/testutil/serialline"
	"github.com/otfabric/go-iec60870-5/link"
)

// blockingStates records the OnState calls of a station and holds the call
// for state at until release is closed; entered is closed when it starts.
type blockingStates struct {
	log              serialline.States[iec101.State]
	at               iec101.State
	entered, release chan struct{}
}

func newBlockingStates(at iec101.State) *blockingStates {
	return &blockingStates{at: at, entered: make(chan struct{}), release: make(chan struct{})}
}

func (b *blockingStates) onState(s iec101.State, _ error) {
	b.log.Enter(s)
	defer b.log.Leave()
	if s == b.at {
		close(b.entered)
		<-b.release
	}
}

// closeWhileBlocked calls closeFn while OnState is held for b.at, checks
// that Close waits for that call and that StateClosed comes after it, one
// call at a time, and returns what Close returned.
func (b *blockingStates) closeWhileBlocked(t *testing.T, closeFn func() error, before []iec101.State) error {
	t.Helper()
	closed := make(chan error, 1)
	go func() { closed <- closeFn() }()
	synctest.Wait()
	states, overlap := b.log.Get()
	if overlap {
		t.Errorf("OnState called while another call had not returned: %v", states)
	}
	if !slices.Equal(states, before) {
		t.Errorf("states %v while OnState(%v) runs, want %v", states, b.at, before)
	}
	select {
	case err := <-closed:
		t.Errorf("Close returned (%v) while OnState(%v) had not returned", err, b.at)
		closed <- err // for the receive below
	default:
	}
	close(b.release)
	err := <-closed
	states, overlap = b.log.Get()
	if want := append(slices.Clone(before), iec101.StateClosed); overlap || !slices.Equal(states, want) {
		t.Errorf("states %v (overlap %v), want %v one at a time", states, overlap, want)
	}
	return err
}

// The server's notifications are told one at a time and in the order of
// the changes, StateClosed last: Close, called while OnState runs for the
// link coming up, reports StateClosed only after that call has returned,
// and returns after it. In unbalanced transmission OnState runs on the
// link's reader, which Close does not wait for on a transport that is not
// an io.Closer; in balanced transmission it runs on the server's
// supervisor. It runs on the fake clock of testing/synctest, whose Wait
// lets every goroutine get as far as it can before the test looks.
func TestServerStateNotificationsInOrder(t *testing.T) {
	t.Run("unbalanced", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			check := serialline.Goroutines(t)
			a, b := serialline.Pair()
			p := params(iec101.ModeUnbalanced)
			bs := newBlockingStates(iec101.StateUp)
			s, err := server.New(serialline.NoCloser{ReadWriter: b}, server.Config{Params: p, LinkAddr: 3, CommonAddr: ca, Handler: &station{}, OnState: bs.onState})
			if err != nil {
				t.Fatal(err)
			}
			lc := p.Link
			lc.Retries = 1
			pri, err := link.NewPrimary(a, lc)
			if err != nil {
				t.Fatal(err)
			}
			reset := make(chan struct{})
			go func() { defer close(reset); _, _ = pri.ResetLink(t.Context(), 3) }() // answered or not
			<-bs.entered
			if err := bs.closeWhileBlocked(t, s.Close, []iec101.State{iec101.StateUp}); err != nil {
				t.Errorf("Close: %v", err)
			}
			_ = pri.Close() // ends the line, and with it the server's pending Read
			<-reset
			check()
		})
	})
	t.Run("balanced", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			check := serialline.Goroutines(t)
			a, b := serialline.Pair()
			p := params(iec101.ModeBalanced)
			bs := newBlockingStates(iec101.StateUp)
			s, err := server.New(b, server.Config{Params: p, LinkAddr: 3, Remote: 1, CommonAddr: ca, Handler: &station{},
				RetryInterval: 50 * time.Millisecond, OnState: bs.onState})
			if err != nil {
				t.Fatal(err)
			}
			c, err := client.New(a, client.Config{Params: p, LinkAddr: 3, OwnAddr: 1, RetryInterval: 50 * time.Millisecond})
			if err != nil {
				t.Fatal(err)
			}
			<-bs.entered
			if err := bs.closeWhileBlocked(t, s.Close, []iec101.State{iec101.StateUp}); err != nil {
				t.Errorf("Close: %v", err)
			}
			_ = c.Close()
			check()
		})
	})
}

// The client's notifications are told one at a time and in the order of
// the changes, StateClosed last, also when the link goes down while Close
// runs on another goroutine: Close, called while OnState runs for the link
// going down, reports StateClosed only after that call has returned, and
// returns after it. It runs on the fake clock of testing/synctest.
func TestClientStateNotificationsInOrder(t *testing.T) {
	for _, mode := range []iec101.Mode{iec101.ModeUnbalanced, iec101.ModeBalanced} {
		t.Run(mode.String(), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				check := serialline.Goroutines(t)
				a, b := serialline.Pair()
				p := params(mode)
				s, err := server.New(b, server.Config{Params: p, LinkAddr: 3, Remote: 1, CommonAddr: ca, Handler: &station{},
					RetryInterval: 50 * time.Millisecond})
				if err != nil {
					t.Fatal(err)
				}
				bs := newBlockingStates(iec101.StateDown)
				c, err := client.New(a, client.Config{Params: p, LinkAddr: 3, OwnAddr: 1,
					PollInterval: 10 * time.Millisecond, RetryInterval: 50 * time.Millisecond, TestInterval: 50 * time.Millisecond,
					OnState: bs.onState})
				if err != nil {
					t.Fatal(err)
				}
				if err := c.WaitUp(t.Context()); err != nil {
					t.Fatal(err)
				}
				_ = s.Close() // the line fails under the client
				<-bs.entered
				err = bs.closeWhileBlocked(t, c.Close, []iec101.State{iec101.StateUp, iec101.StateDown})
				if err != nil && !errors.Is(err, serialline.ErrClosed) {
					t.Errorf("Close: %v", err)
				}
				check()
			})
		})
	}
}
