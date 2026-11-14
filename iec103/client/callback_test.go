// SPDX-License-Identifier: MIT

package client

import (
	"bytes"
	"context"
	"errors"
	"log"
	"runtime"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	iec60870 "github.com/otfabric/go-iec60870-5"
	"github.com/otfabric/go-iec60870-5/cp"
	"github.com/otfabric/go-iec60870-5/iec103"
	"github.com/otfabric/go-iec60870-5/iec103/server"
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

// pair starts protection equipment with scfg and a client with cfg on the
// two ends of a line, without waiting for the link. Both are closed when
// the test ends.
func pair(t *testing.T, cfg Config, scfg server.Config) (*Client, *server.Server) {
	t.Helper()
	a, b := serialline.Pair()
	lc := link.Config{Timeout: 50 * time.Millisecond, Retries: 3}
	scfg.Link, scfg.LinkAddr, scfg.Fun, scfg.SyncDelay = lc, 3, iec103.FunDistance, -1
	srv, err := server.New(b, scfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	cfg.Link, cfg.LinkAddr, cfg.NoInitGI, cfg.NoInitTimeSync = lc, 3, true, true
	c, err := New(a, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c, srv
}

// L10: the Handler and OnState call Close themselves. Close returns at
// once, without waiting for the goroutine it runs on, the client stops,
// and Err stays nil.
func TestCloseFromCallbacksSynchronously(t *testing.T) {
	for _, name := range []string{"Handler", "OnState"} {
		t.Run(name, func(t *testing.T) {
			t.Cleanup(leakcheck.Check(t))
			closed, ready := make(chan error, 1), make(chan struct{})
			var once sync.Once
			var c *Client
			stop := func() { <-ready; once.Do(func() { closed <- c.Close() }) }
			cfg := Config{}
			if name == "Handler" {
				cfg.Handler = func(*iec103.ASDU) { stop() } // the identification after the reset
			} else {
				cfg.OnState = func(s iec103.State, _ error) {
					if s == iec103.StateUp {
						stop()
					}
				}
			}
			c, _ = pair(t, cfg, server.Config{})
			close(ready) // c is set
			if err := within(t, closed, "Close from the "+name); err != nil {
				t.Fatal(err)
			}
			within(t, c.Done(), "the client to stop")
			if err := c.Err(); err != nil {
				t.Errorf("Err after Close: %v", err)
			}
		})
	}
}

// A Handler or OnState that does not return normally is logged with its
// stack and stops the client only: Done closes, and Err wraps ErrClosed
// and names the callback.
func TestCallbackThatDoesNotReturnNormally(t *testing.T) {
	for _, name := range []string{"Handler", "OnState"} {
		t.Run(name, func(t *testing.T) {
			t.Cleanup(leakcheck.Check(t))
			var mu sync.Mutex
			var logs bytes.Buffer
			cfg := Config{Logger: iec60870.NewStdLogger(log.New(writerFunc(func(p []byte) (int, error) {
				mu.Lock()
				defer mu.Unlock()
				return logs.Write(p)
			}), "", 0))}
			if name == "Handler" {
				cfg.Handler = func(*iec103.ASDU) { panic(stopValue) }
			} else {
				cfg.OnState = func(s iec103.State, _ error) {
					if s == iec103.StateUp {
						panic(stopValue)
					}
				}
			}
			c, _ := pair(t, cfg, server.Config{})
			within(t, c.Done(), "the client to stop")
			if err := c.Err(); !errors.Is(err, iec103.ErrClosed) || !strings.Contains(err.Error(), name) {
				t.Errorf("Err = %v, want ErrClosed naming %s", err, name)
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

// requests are the client's methods that send and wait, each as called
// from a callback.
func requests(c *Client, ctx context.Context) map[string]func() error {
	return map[string]func() error{
		"Interrogate": func() error { _, err := c.Interrogate(ctx); return err },
		"ClockSync":   func() error { _, err := c.ClockSync(ctx, time.Now()); return err },
		"Command": func() error {
			return c.Command(ctx, iec103.FunDistance, 16, iec103.DPIOn)
		},
		"GenericRead": func() error { _, err := c.ReadHeadings(ctx); return err },
		"GenericInterrogation": func() error {
			_, err := c.GenericInterrogation(ctx)
			return err
		},
		"DisturbanceList": func() error { _, err := c.DisturbanceList(ctx, iec103.FunDistance); return err },
		"Disturbance":     func() error { _, err := c.Disturbance(ctx, iec103.FunDistance, 1); return err },
		"Send": func() error {
			return c.Send(ctx, iec103.New(iec103.CauseGI, 3, iec103.FunGlobal, iec103.InfGIEnd, iec103.GIInit{SCN: 9}))
		},
		"Broadcast": func() error {
			return c.Broadcast(iec103.New(iec103.CauseTimeSync, iec103.CommonAddrGlobal, iec103.FunGlobal, iec103.InfTimeSync, iec103.TimeSync{Time: cp.At(time.Now())}))
		},
		"BroadcastClockSync": func() error { return c.BroadcastClockSync(time.Now()) },
	}
}

// L9: a request made from the Handler or OnState returns ErrInHandler at
// once, without being sent, instead of waiting for an answer that the
// goroutine it runs on would have to let through.
func TestRequestFromHandlerFailsAtOnce(t *testing.T) {
	for _, name := range []string{"Handler", "OnState"} {
		t.Run(name, func(t *testing.T) {
			t.Cleanup(leakcheck.Check(t))
			got, ready := make(chan map[string]error, 1), make(chan struct{})
			var once sync.Once
			var c *Client
			call := func() {
				<-ready
				once.Do(func() {
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					errs := map[string]error{}
					for m, f := range requests(c, ctx) {
						errs[m] = f()
					}
					got <- errs
				})
			}
			cfg := Config{}
			if name == "Handler" {
				cfg.Handler = func(*iec103.ASDU) { call() }
			} else {
				cfg.OnState = func(s iec103.State, _ error) {
					if s == iec103.StateUp {
						call()
					}
				}
			}
			c, _ = pair(t, cfg, server.Config{})
			close(ready) // c is set
			for m, err := range within(t, got, "the requests from the "+name) {
				if !errors.Is(err, iec103.ErrInHandler) {
					t.Errorf("%s from the %s: %v, want ErrInHandler", m, name, err)
				}
			}
		})
	}
}

// A request in flight when the client is closed, and every request made
// after Close, fail with ErrClosed.
func TestRequestsFailWithErrClosed(t *testing.T) {
	t.Cleanup(leakcheck.Check(t))
	held := make(chan struct{}, 1)
	c, _ := pair(t, Config{}, server.Config{Handler: server.HandlerFunc(func(_ *server.Server, a *iec103.ASDU) {
		if _, ok := a.Body.(iec103.GIInit); ok {
			select { // never answered: the interrogation stays in flight
			case held <- struct{}{}:
			default:
			}
		}
	})})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := c.WaitUp(ctx); err != nil {
		t.Fatal(err)
	}
	inFlight := make(chan error, 1)
	go func() { _, err := c.Interrogate(ctx); inFlight <- err }()
	within(t, held, "the interrogation to reach the equipment")
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if err := within(t, inFlight, "the interrogation in flight"); !errors.Is(err, iec103.ErrClosed) {
		t.Errorf("interrogation in flight at Close: %v, want ErrClosed", err)
	}
	for m, f := range requests(c, ctx) {
		if err := f(); !errors.Is(err, iec103.ErrClosed) {
			t.Errorf("%s after Close: %v, want ErrClosed", m, err)
		}
	}
}

// A request that missed answers reports ErrOverflow.
func TestWaitOverflow(t *testing.T) {
	t.Cleanup(leakcheck.Check(t))
	c, _ := pair(t, Config{}, server.Config{})
	w := &waiter{ch: make(chan *iec103.ASDU, 1), overflow: true}
	if _, err := c.wait(context.Background(), w); !errors.Is(err, iec103.ErrOverflow) {
		t.Fatalf("wait: %v, want ErrOverflow", err)
	}
}

// Equipment that answers a general interrogation with more ASDUs than
// Config.MaxResult and no termination: the request ends at the bound with
// ErrResultLimit, long before its deadline, and allocates within a budget
// set by the bound, not by what the equipment sends. It runs on the fake
// clock of testing/synctest.
func TestInterrogationResultLimit(t *testing.T) {
	const limit, flood = 16, 1024
	const budget = limit * 16 << 10
	synctest.Test(t, func(t *testing.T) {
		check := serialline.Goroutines(t)
		const deadline = time.Hour
		var cfg Config
		cfg.MaxResult, cfg.RequestTimeout = limit, deadline
		c, srv := pair(t, cfg, server.Config{QueueSize: 2 * flood, Handler: server.HandlerFunc(func(s *server.Server, a *iec103.ASDU) {
			gi, ok := a.Body.(iec103.GIInit)
			if !ok {
				return
			}
			for i := range flood {
				_ = s.Send(iec103.New(iec103.CauseGI, 3, iec103.FunDistance, iec103.InfoNumber(16+i%64), iec103.TimeTagged{DPI: iec103.DPIOn, SIN: gi.SCN}), server.Class1)
			}
			// No termination.
		})})
		ctx, cancel := context.WithTimeout(context.Background(), deadline)
		defer cancel()
		if err := c.WaitUp(ctx); err != nil {
			t.Fatal(err)
		}
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		start := time.Now()
		got, err := c.Interrogate(ctx)
		took := time.Since(start)
		runtime.ReadMemStats(&after)
		if !errors.Is(err, iec103.ErrResultLimit) || len(got) != limit {
			t.Errorf("Interrogate: %d ASDUs, %v; want %d and ErrResultLimit", len(got), err, limit)
		}
		if took >= deadline {
			t.Errorf("Interrogate took %v, the whole deadline", took)
		}
		if n := after.TotalAlloc - before.TotalAlloc; n > budget {
			t.Errorf("%d octets allocated during the request, want at most %d", n, budget)
		}
		_ = c.Close()
		_ = srv.Close()
		check()
	})
}
