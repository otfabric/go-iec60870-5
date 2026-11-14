// SPDX-License-Identifier: MIT

package e2e

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/otfabric/go-iec60870-5/asdu"
	"github.com/otfabric/go-iec60870-5/ft12"
	"github.com/otfabric/go-iec60870-5/iec101"
	"github.com/otfabric/go-iec60870-5/iec101/client"
	"github.com/otfabric/go-iec60870-5/iec101/server"
	"github.com/otfabric/go-iec60870-5/internal/testutil/serialline"
	"github.com/otfabric/go-iec60870-5/link"
)

// TestRequestDeadlineWhileTheLinkRepeats: a request returns at its
// deadline while the poller repeats user data the station does not
// acknowledge for Timeout x (Retries + 1). It runs on the fake clock of
// testing/synctest.
func TestRequestDeadlineWhileTheLinkRepeats(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		check := serialline.Goroutines(t)
		a, b := serialline.Pair()
		peer := serialline.NewPeer(t, b, ft12.Params{AddrSize: 1})
		rs := serialline.NewRawSecondary(peer, 3)
		var mu sync.Mutex
		mute := false
		rs.Hook = misbehave(&mu, &mute, func(f ft12.Frame, a [][]byte) [][]byte {
			if serialline.Func(f) == int(link.FuncUserDataConf) {
				return nil
			}
			return a
		})
		rs.Start()
		p := params(iec101.ModeUnbalanced)
		p.Link.Timeout = 300 * time.Millisecond
		c, err := client.New(a, client.Config{Params: p, LinkAddr: 3, PollInterval: 10 * time.Millisecond,
			RetryInterval: 50 * time.Millisecond, RequestTimeout: 5 * time.Second})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := c.WaitUp(ctx); err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		mute = true
		mu.Unlock()
		rctx, rcancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
		defer rcancel()
		start := time.Now()
		err = c.Command(rctx, ca, asdu.SingleCommand{IOA: 500, Select: true})
		if d := time.Since(start); d > 150*time.Millisecond || !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("command with a 150 ms deadline: %v after %v", err, d)
		}
		if err := serialline.Within(t, closeBound, "client Close", c.Close); err != nil {
			t.Error(err)
		}
		_ = b.Close()
		check()
	})
}

// lossyEnd is the client's end of a line that loses the primary messages
// of the functions set in drop, as a line that corrupts them would: they
// never reach the station. Each lost function is sent on lost, when
// there is room.
type lossyEnd struct {
	*serialline.End
	mu   sync.Mutex
	drop map[byte]bool
	lost chan byte
}

func (l *lossyEnd) set(fns ...byte) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.drop = map[byte]bool{}
	for _, fn := range fns {
		l.drop[fn] = true
	}
}

func (l *lossyEnd) Write(b []byte) (int, error) {
	var ctl byte
	switch {
	case len(b) >= 5 && b[0] == ft12.StartVariable:
		ctl = b[4]
	case len(b) >= 2 && b[0] == ft12.StartFixed:
		ctl = b[1]
	}
	l.mu.Lock()
	lose := link.Control(ctl).PRM() && l.drop[link.Control(ctl).Func()]
	l.mu.Unlock()
	if lose {
		select {
		case l.lost <- link.Control(ctl).Func():
		default:
		}
		return len(b), nil
	}
	return l.End.Write(b)
}

// balancedPair starts a balanced client, whose writes go through a
// lossyEnd, and a balanced server with the counting station. Both are
// closed, and the goroutines checked, at the end of the test.
func balancedPair(t *testing.T, timeout, testInterval time.Duration) (*client.Client, *lossyEnd, *counted) {
	t.Helper()
	check := serialline.Goroutines(t)
	a, b := serialline.Pair()
	p := params(iec101.ModeBalanced)
	p.Link.Timeout = timeout
	st := newCounted()
	s, err := server.New(b, server.Config{Params: p, LinkAddr: 3, Remote: 1, CommonAddr: ca, Handler: st,
		RetryInterval: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	line := &lossyEnd{End: a, lost: make(chan byte, 16)}
	c, err := client.New(line, client.Config{Params: p, LinkAddr: 3, OwnAddr: 1,
		RetryInterval: 50 * time.Millisecond, TestInterval: testInterval, RequestTimeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := serialline.Within(t, closeBound, "client Close", c.Close); err != nil {
			t.Error(err)
		}
		if err := serialline.Within(t, closeBound, "server Close", s.Close); err != nil {
			t.Error(err)
		}
		check()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.WaitUp(ctx); err != nil {
		t.Fatal(err)
	}
	return c, line, st
}

// TestBalancedRequestDeadlineWhileTheLinkRepeats: in balanced
// transmission a request returns at its deadline while the link repeats a
// test frame the station does not acknowledge, for Timeout x (Retries +
// 1) = 4 s. It runs on the real clock, with a margin of a whole link
// time-out: the request waits on channels and its context only.
func TestBalancedRequestDeadlineWhileTheLinkRepeats(t *testing.T) {
	c, line, _ := balancedPair(t, time.Second, 50*time.Millisecond)
	line.set(link.FuncTestLink, link.FuncUserDataConf)
	for fn := range line.lost {
		if fn == link.FuncTestLink {
			break // the client is repeating a test of the link
		}
	}
	rctx, rcancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer rcancel()
	start := time.Now()
	err := c.Command(rctx, ca, asdu.SingleCommand{IOA: 500, Select: true})
	if d := time.Since(start); d >= time.Second || !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("command with a 150 ms deadline: %v after %v", err, d)
	}
	line.set()
}

// TestBalancedSendAfterItsDeadline: in balanced transmission the client
// finishes the exchange of a request whose deadline passed, so the
// station receives its user data exactly once when the line recovers, and
// the next message with the same ASDU is a new one.
func TestBalancedSendAfterItsDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, line, st := balancedPair(t, 300*time.Millisecond, -1)
		cmd := asdu.New(asdu.CauseActivation, ca, asdu.SingleCommand{IOA: 500, Select: true})
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := c.Send(ctx, cmd); err != nil {
			t.Fatal(err)
		}
		waitCount(t, st, 500, 1)
		line.set(link.FuncUserDataConf)
		rctx, rcancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
		defer rcancel()
		if err := c.Send(rctx, cmd); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("send with a 150 ms deadline while its frames are lost: %v", err)
		}
		line.set()
		waitCount(t, st, 500, 2) // the repetition after the line recovered
		synctest.Wait()
		if n := st.count(500); n != 2 {
			t.Fatalf("the ASDU of the send past its deadline was delivered %d times, want once", n-1)
		}
		if err := c.Send(ctx, cmd); err != nil {
			t.Fatal(err)
		}
		waitCount(t, st, 500, 3)
	})
}
