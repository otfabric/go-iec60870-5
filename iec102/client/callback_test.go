// SPDX-License-Identifier: MIT

package client

import (
	"bytes"
	"errors"
	"log"
	"runtime"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	iec60870 "github.com/otfabric/go-iec60870-5"
	"github.com/otfabric/go-iec60870-5/ft12"
	"github.com/otfabric/go-iec60870-5/iec102"
	"github.com/otfabric/go-iec60870-5/internal/testutil/leakcheck"
	"github.com/otfabric/go-iec60870-5/internal/testutil/serialline"
	"github.com/otfabric/go-iec60870-5/link"
)

// stopValue is what a test callback ends with when it does not return
// normally.
const stopValue = "test callback stopped"

func within[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
	var zero T
	return zero
}

// L10: the Handler calls Close itself. Close returns at once, the request
// in progress ends with ErrClosed, Done closes and Err stays nil.
func TestCloseFromHandlerSynchronously(t *testing.T) {
	t.Cleanup(leakcheck.Check(t))
	foreign := enc(t, iec102.New(iec102.CauseSpontaneous, 2, 0, iec102.Raw{Data: []byte{1}}))
	st := &station{answers: [][]byte{foreign}} // never the answer
	closed := make(chan error, 1)
	var once sync.Once
	var c *Client
	ready := make(chan struct{})
	c, _ = counted(t, st, func(cfg *Config) {
		cfg.Handler = func(*iec102.ASDU) {
			<-ready
			once.Do(func() { closed <- c.Close() })
		}
	})
	close(ready)
	if _, err := c.ReadTime(tctx(t)); !errors.Is(err, iec102.ErrClosed) {
		t.Fatalf("ReadTime: %v, want ErrClosed", err)
	}
	if err := within(t, closed, "Close from the Handler"); err != nil {
		t.Fatal(err)
	}
	within(t, c.Done(), "the client to stop")
	if err := c.Err(); err != nil {
		t.Errorf("Err after Close: %v", err)
	}
}

// L10: OnState calls Close at every change, StateClosed included, which
// Close itself reports. Every call returns and the request fails with
// ErrClosed.
func TestCloseFromOnState(t *testing.T) {
	t.Cleanup(leakcheck.Check(t))
	st := &station{}
	var mu sync.Mutex
	var states []iec102.State
	var c *Client
	ready := make(chan struct{})
	c, _ = counted(t, st, func(cfg *Config) {
		cfg.OnState = func(s iec102.State, _ error) {
			<-ready
			mu.Lock()
			states = append(states, s)
			mu.Unlock()
			_ = c.Close()
		}
	})
	close(ready)
	done := make(chan error, 1)
	go func() { _, err := c.ReadTime(tctx(t)); done <- err }()
	if err := within(t, done, "the request whose OnState closes the client"); !errors.Is(err, iec102.ErrClosed) {
		t.Fatalf("ReadTime: %v, want ErrClosed", err)
	}
	within(t, c.Done(), "the client to stop")
	mu.Lock()
	defer mu.Unlock()
	if n := len(states); n < 2 || states[0] != iec102.StateUp || states[n-1] != iec102.StateClosed {
		t.Errorf("states %v, want up first and closed last", states)
	}
}

// A Handler or OnState that does not return normally is logged with its
// stack and closes the client: the request fails with ErrClosed, Done
// closes, and Err wraps ErrClosed and names the callback.
func TestCallbackThatDoesNotReturnNormally(t *testing.T) {
	foreign := func(t *testing.T) []byte {
		return enc(t, iec102.New(iec102.CauseSpontaneous, 2, 0, iec102.Raw{Data: []byte{1}}))
	}
	for _, tc := range []struct {
		name string
		mod  func(*Config)
	}{
		{"Handler", func(cfg *Config) { cfg.Handler = func(*iec102.ASDU) { panic(stopValue) } }},
		{"OnState", func(cfg *Config) {
			cfg.OnState = func(s iec102.State, _ error) {
				if s == iec102.StateUp {
					panic(stopValue)
				}
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Cleanup(leakcheck.Check(t))
			var mu sync.Mutex
			var logs bytes.Buffer
			st := &station{answers: [][]byte{foreign(t)}} // never the answer
			c, _ := counted(t, st, func(cfg *Config) {
				cfg.Logger = iec60870.NewStdLogger(log.New(writerFunc(func(p []byte) (int, error) {
					mu.Lock()
					defer mu.Unlock()
					return logs.Write(p)
				}), "", 0))
				tc.mod(cfg)
			})
			if _, err := c.ReadTime(tctx(t)); !errors.Is(err, iec102.ErrClosed) {
				t.Errorf("ReadTime: %v, want ErrClosed", err)
			}
			within(t, c.Done(), "the client to stop")
			if err := c.Err(); !errors.Is(err, iec102.ErrClosed) || !strings.Contains(err.Error(), tc.name) {
				t.Errorf("Err = %v, want ErrClosed naming %s", err, tc.name)
			}
			_ = c.Close()
			mu.Lock()
			text := logs.String()
			mu.Unlock()
			if !strings.Contains(text, "did not return normally") || !strings.Contains(text, "goroutine ") {
				t.Errorf("no log entry with the stack:\n%s", text)
			}
		})
	}
}

type writerFunc func(p []byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

// Done closes when the transport fails under the client, and Err names the
// cause; after Close, Err is nil.
func TestDoneAndErr(t *testing.T) {
	t.Cleanup(leakcheck.Check(t))
	c, conn := counted(t, &station{}, nil)
	select {
	case <-c.Done():
		t.Fatal("Done closed while the client runs")
	default:
	}
	if err := c.Err(); err != nil {
		t.Fatalf("Err while the client runs: %v", err)
	}
	_ = conn.Conn.Close() // the line fails under the client
	within(t, c.Done(), "the client to stop")
	if err := c.Err(); err == nil {
		t.Error("Err is nil after the transport failed")
	}
}

// A station that answers a request with more ASDUs than Config.MaxResult
// and never terminates it: the request ends at the bound with
// ErrResultLimit, long before its deadline, and allocates within a budget
// set by the bound, not by what the station sends. It runs on the fake
// clock of testing/synctest.
func TestRequestResultLimit(t *testing.T) {
	const limit, flood = 16, 1024
	const budget = limit * 16 << 10
	synctest.Test(t, func(t *testing.T) {
		check := serialline.Goroutines(t)
		answers := make([][]byte, flood)
		for i := range answers {
			answers[i] = enc(t, iec102.New(iec102.CauseRequest, 1, 50, iec102.SinglePoints{Points: []iec102.SinglePoint{{Addr: uint8(i % 128), On: true}}}))
		}
		st := &station{answers: answers} // no termination
		a, b := serialline.Pair()
		sec, err := link.NewSecondary(b, link.Config{FT12: ft12.Params{AddrSize: 2}}, 1, st)
		if err != nil {
			t.Fatal(err)
		}
		const deadline = time.Hour
		c, err := New(a, Config{LinkAddr: 1, CommonAddr: 1, PollInterval: time.Millisecond, RequestTimeout: deadline,
			MaxResult: limit, Link: link.Config{Timeout: 100 * time.Millisecond, Retries: 1}})
		if err != nil {
			t.Fatal(err)
		}
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		start := time.Now()
		got, err := c.ReadSinglePoints(t.Context(), 50, nil, nil)
		took := time.Since(start)
		runtime.ReadMemStats(&after)
		if !errors.Is(err, iec102.ErrResultLimit) || len(got) != limit {
			t.Errorf("ReadSinglePoints: %d points, %v; want %d and ErrResultLimit", len(got), err, limit)
		}
		if took >= deadline {
			t.Errorf("the request took %v, the whole deadline", took)
		}
		if n := after.TotalAlloc - before.TotalAlloc; n > budget {
			t.Errorf("%d octets allocated during the request, want at most %d", n, budget)
		}
		_ = c.Close()
		_ = sec.Close()
		check()
	})
}
