// SPDX-License-Identifier: MIT

package client

import (
	"errors"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/otfabric/go-iec60870-5/ft12"
	"github.com/otfabric/go-iec60870-5/iec102"
	"github.com/otfabric/go-iec60870-5/internal/testutil/leakcheck"
	"github.com/otfabric/go-iec60870-5/internal/testutil/serialline"
	"github.com/otfabric/go-iec60870-5/link"
)

// L9: OnState runs outside the request slot, so it may issue a request. The
// request that brings the link up returns once OnState has returned, and
// the request OnState issues is answered too.
func TestRequestFromOnState(t *testing.T) {
	t.Cleanup(leakcheck.Check(t))
	answer := enc(t, iec102.New(iec102.CauseRequest, 1, 0, iec102.SystemTime{Time: iec102.TimeBOf(time.Now(), nil)}))
	st := &station{answers: [][]byte{answer}}
	inner := make(chan error, 1)
	var c *Client
	ready := make(chan struct{})
	c, _ = counted(t, st, func(cfg *Config) {
		cfg.OnState = func(s iec102.State, _ error) {
			if s != iec102.StateUp {
				return
			}
			<-ready
			_, err := c.ReadTime(tctx(t))
			inner <- err
		}
	})
	close(ready)
	outer := make(chan error, 1)
	go func() { _, err := c.ReadTime(tctx(t)); outer <- err }()
	if err := within(t, outer, "the request that brought the link up"); err != nil {
		t.Errorf("ReadTime: %v", err)
	}
	select {
	case err := <-inner:
		if err != nil {
			t.Errorf("ReadTime from OnState: %v", err)
		}
	default:
		t.Error("the request that brought the link up returned before OnState did")
	}
}

// The notifications are told one at a time and in the order of the
// changes, StateClosed last, also when a request brings the link down
// while Close runs on another goroutine: StateClosed waits for the
// notification in progress, and Close returns after StateClosed. It runs
// on the fake clock of testing/synctest, whose Wait lets every goroutine
// get as far as it can before the test looks.
func TestStateNotificationsInOrder(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		check := serialline.Goroutines(t)
		a, b := serialline.Pair()
		sec, err := link.NewSecondary(b, link.Config{FT12: ft12.Params{AddrSize: 2}}, 1, &station{})
		if err != nil {
			t.Fatal(err)
		}
		var log serialline.States[iec102.State]
		downEntered, release := make(chan struct{}), make(chan struct{})
		c, err := New(a, Config{LinkAddr: 1, CommonAddr: 1, PollInterval: time.Millisecond,
			Link: link.Config{Timeout: 100 * time.Millisecond, Retries: 1},
			OnState: func(s iec102.State, _ error) {
				log.Enter(s)
				defer log.Leave()
				if s == iec102.StateDown {
					close(downEntered)
					<-release
				}
			}})
		if err != nil {
			t.Fatal(err)
		}
		if err := c.Init(t.Context()); err != nil {
			t.Fatal(err)
		}
		_ = sec.Close() // the line fails under the client
		read := make(chan error, 1)
		go func() { _, err := c.ReadTime(t.Context()); read <- err }()
		<-downEntered
		closed := make(chan error, 1)
		go func() { closed <- c.Close() }()
		synctest.Wait()
		states, overlap := log.Get()
		if overlap {
			t.Errorf("OnState called while another call had not returned: %v", states)
		}
		if !slices.Equal(states, []iec102.State{iec102.StateUp, iec102.StateDown}) {
			t.Errorf("states %v while OnState(down) runs, want [up down]", states)
		}
		select {
		case err := <-closed:
			t.Errorf("Close returned (%v) while OnState(down) had not returned", err)
			closed <- err // for the receive below
		default:
		}
		close(release)
		if err := <-closed; err != nil {
			t.Errorf("Close: %v", err)
		}
		if err := <-read; !errors.Is(err, iec102.ErrLinkDown) && !errors.Is(err, iec102.ErrClosed) {
			t.Errorf("ReadTime: %v, want ErrLinkDown or ErrClosed", err)
		}
		states, overlap = log.Get()
		if overlap || !slices.Equal(states, []iec102.State{iec102.StateUp, iec102.StateDown, iec102.StateClosed}) {
			t.Errorf("states %v (overlap %v), want [up down closed] one at a time", states, overlap)
		}
		check()
	})
}
