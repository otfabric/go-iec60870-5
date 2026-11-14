// SPDX-License-Identifier: MIT

package client

import (
	"context"
	"errors"
	"math"
	"net"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/otfabric/go-iec60870-5/asdu"
	iec104 "github.com/otfabric/go-iec60870-5/iec104"
	"github.com/otfabric/go-iec60870-5/iec104/apci"
	"github.com/otfabric/go-iec60870-5/iec104/internal/testutil"
)

// The pause for a delay d lies between d/2 and d, both included.
func TestJitterBounds(t *testing.T) {
	lowest := options{randN: func(int64) int64 { return 0 }}
	highest := options{randN: func(n int64) int64 { return n - 1 }}
	for _, c := range []struct{ d, lo, hi time.Duration }{
		{0, 0, 0},
		{1, 1, 1},
		{2, 1, 2},
		{3, 1, 3},
		{time.Second, 500 * time.Millisecond, time.Second},
		{30 * time.Second, 15 * time.Second, 30 * time.Second},
		{math.MaxInt64, math.MaxInt64 / 2, math.MaxInt64},
	} {
		if got := lowest.jitter(c.d); got != c.lo {
			t.Errorf("lowest pause for %v: %v, want %v", c.d, got, c.lo)
		}
		if got := highest.jitter(c.d); got != c.hi {
			t.Errorf("highest pause for %v: %v, want %v", c.d, got, c.hi)
		}
	}
}

// checkPauses fails t unless every pause lies between half its delay and
// the delay, and not every pause equals its delay. Time is the fake time of
// a synctest bubble, so a pause is measured exactly.
func checkPauses(t *testing.T, what string, pauses, delays []time.Duration) {
	t.Helper()
	if len(pauses) != len(delays) {
		t.Fatalf("%s: %d pauses, want %d", what, len(pauses), len(delays))
	}
	equal := 0
	for i, p := range pauses {
		d := delays[i]
		if p < d/2 || p > d {
			t.Errorf("%s: pause %d is %v, want %v to %v", what, i+1, p, d/2, d)
		}
		if p == d {
			equal++
		}
	}
	if equal == len(pauses) {
		t.Errorf("%s: every pause equals its delay %v: no jitter", what, delays)
	}
}

// failingDialer records when it is called and fails every call after the
// first ok ones, which return the client end of a net.Pipe whose station
// confirms STARTDT and closes the connection when drop is closed.
type failingDialer struct {
	ok    int
	drop  chan struct{}
	mu    sync.Mutex
	calls []time.Time
	call  chan struct{}
}

func (d *failingDialer) DialContext(context.Context, string, string) (net.Conn, error) {
	d.mu.Lock()
	d.calls = append(d.calls, time.Now())
	n := len(d.calls)
	d.mu.Unlock()
	select {
	case d.call <- struct{}{}:
	default:
	}
	if n > d.ok {
		return nil, errors.New("station unreachable")
	}
	station, cli := net.Pipe()
	go func() {
		if f, err := apci.ReadFrame(station); err != nil || f.Function != apci.StartDTAct {
			return
		}
		con, _ := apci.NewU(apci.StartDTCon).MarshalBinary()
		if _, err := station.Write(con); err != nil {
			return
		}
		for {
			if _, err := apci.ReadFrame(station); err != nil {
				return
			}
		}
	}()
	go func() {
		<-d.drop
		_ = station.Close()
	}()
	return cli, nil
}

func (d *failingDialer) times() []time.Time {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]time.Time(nil), d.calls...)
}

func gaps(ts []time.Time) []time.Duration {
	var out []time.Duration
	for i := 1; i < len(ts); i++ {
		out = append(out, ts[i].Sub(ts[i-1]))
	}
	return out
}

// After a lost connection the client waits a jittered pause before each
// attempt: between half the delay and the delay, which doubles up to
// MaxDelay.
func TestReconnectPausesAreJittered(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := &failingDialer{ok: 1, drop: make(chan struct{}), call: make(chan struct{}, 1)}
		c, err := New("station:2404", WithDialer(d), WithParams(testutil.Params()),
			WithReconnect(Reconnect{MinDelay: time.Second, MaxDelay: 8 * time.Second}))
		if err != nil {
			t.Fatal(err)
		}
		if err := c.Connect(context.Background()); err != nil {
			t.Fatal(err)
		}
		close(d.drop) // the connection is lost
		// The connection, then 6 attempts that fail.
		for len(d.times()) < 7 {
			<-d.call
		}
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
		// The pauses between the failed attempts: 2 s, 4 s, then 8 s.
		checkPauses(t, "reconnect", gaps(d.times()[1:7]),
			[]time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second, 8 * time.Second, 8 * time.Second})
	})
}

// retryTimes records when a retry is announced.
type retryTimes struct {
	testutil.Metrics
	mu    sync.Mutex
	times []time.Time
}

func (r *retryTimes) OnRequest(net.Addr, asdu.TypeID, asdu.CommonAddr) {}

func (r *retryTimes) OnRequestDone(net.Addr, asdu.TypeID, asdu.CommonAddr, time.Duration, error) {}

func (r *retryTimes) OnRetry(net.Addr, asdu.TypeID, asdu.CommonAddr, int, error) {
	r.mu.Lock()
	r.times = append(r.times, time.Now())
	r.mu.Unlock()
}

var _ iec104.RequestMetrics = (*retryTimes)(nil)

// A retried request waits a jittered pause before each further attempt.
func TestRetryPausesAreJittered(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rec := &retryTimes{}
		c, err := New("station:2404", WithMetrics(rec),
			WithRetry(Retry{Attempts: 6, Backoff: time.Second, MaxBackoff: 4 * time.Second}))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = c.Close() }()
		// Not connected: every attempt fails at once, and is retried.
		if _, err := c.Interrogate(context.Background(), 1, asdu.QOIStation); !errors.Is(err, iec104.ErrNotConnected) {
			t.Fatalf("Interrogate: %v, want ErrNotConnected", err)
		}
		rec.mu.Lock()
		times := append([]time.Time(nil), rec.times...)
		rec.mu.Unlock()
		checkPauses(t, "retry", gaps(times),
			[]time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 4 * time.Second})
	})
}

// A connection of a group that fails to connect is dialed again after a
// jittered pause: between half the delay and the delay.
func TestGroupRedialPausesAreJittered(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := &failingDialer{drop: make(chan struct{}), call: make(chan struct{}, 1)}
		g, err := NewGroup([]string{"station:2404"}, WithDialer(d), WithParams(testutil.Params()),
			WithReconnect(Reconnect{MinDelay: time.Second, MaxDelay: 8 * time.Second}))
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		connected := make(chan error, 1)
		go func() { connected <- g.Connect(ctx) }()
		var pauses, delays []time.Duration
		for range 5 {
			<-d.call
			synctest.Wait() // the failed attempt has set the next one
			g.dialMu.Lock()
			pauses = append(pauses, time.Until(g.notYet[0]))
			delays = append(delays, g.delay[0])
			g.dialMu.Unlock()
		}
		cancel()
		if err := <-connected; !errors.Is(err, iec104.ErrConnectFailed) {
			t.Errorf("Connect: %v, want ErrConnectFailed", err)
		}
		if err := g.Close(); err != nil {
			t.Fatal(err)
		}
		checkPauses(t, "group redial", pauses, delays)
	})
}
