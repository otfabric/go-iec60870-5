// SPDX-License-Identifier: MIT

package e2e

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
	"github.com/otfabric/go-iec60870-5/asdu"
	"github.com/otfabric/go-iec60870-5/iec101"
	"github.com/otfabric/go-iec60870-5/iec101/client"
	"github.com/otfabric/go-iec60870-5/iec101/server"
	"github.com/otfabric/go-iec60870-5/internal/testutil/leakcheck"
	"github.com/otfabric/go-iec60870-5/internal/testutil/serialline"
	"github.com/otfabric/go-iec60870-5/telecontrol"
)

// stopValue is what a test callback ends with when it does not return
// normally.
const stopValue = "test callback stopped"

// TestL10CallbackClosesSynchronously: a callback calls Close itself, on its
// own goroutine. Close returns at once instead of waiting for that
// goroutine, the station stops, and nothing is left running once the
// callback has returned.
func TestL10CallbackClosesSynchronously(t *testing.T) {
	for _, mode := range modes {
		t.Run(mode.String()+"/client Handler", func(t *testing.T) {
			check := leakcheck.Check(t)
			closed, ready := make(chan error, 1), make(chan struct{})
			var once sync.Once
			var c *client.Client
			c, s, _, _ := stations(t, mode, func(cc *client.Config, _ *server.Config) {
				cc.Handler = func(*asdu.ASDU) { <-ready; once.Do(func() { closed <- c.Close() }) }
			})
			close(ready) // c is set
			if err := s.Send(asdu.New(asdu.CauseSpontaneous, ca, asdu.SinglePoint{IOA: 1, Value: true})); err != nil {
				t.Fatal(err)
			}
			if err := recv(t, closed, "Close from the Handler"); err != nil {
				t.Fatal(err)
			}
			recv(t, c.Done(), "the client to stop")
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			check()
		})
		t.Run(mode.String()+"/client OnState", func(t *testing.T) {
			check := leakcheck.Check(t)
			closed, ready := make(chan error, 1), make(chan struct{})
			var once sync.Once
			var c *client.Client
			c, s, _, _ := stations(t, mode, func(cc *client.Config, _ *server.Config) {
				cc.OnState = func(st iec101.State, _ error) {
					if st == iec101.StateUp {
						<-ready
						once.Do(func() { closed <- c.Close() })
					}
				}
			})
			close(ready) // c is set
			if err := recv(t, closed, "Close from OnState"); err != nil {
				t.Fatal(err)
			}
			recv(t, c.Done(), "the client to stop")
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			check()
		})
		t.Run(mode.String()+"/server Handler", func(t *testing.T) {
			check := leakcheck.Check(t)
			closed := make(chan error, 1)
			var once sync.Once
			c, s, _, _ := stations(t, mode, func(_ *client.Config, sc *server.Config) {
				sc.Handler = server.HandlerFunc(func(s *server.Server, _ *asdu.ASDU) { once.Do(func() { closed <- s.Close() }) })
			})
			ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
			defer cancel()
			// The server may close before its link acknowledges the frame.
			_ = c.Send(ctx, asdu.New(asdu.CauseActivation, ca, asdu.SingleCommand{IOA: 500, Select: true}))
			if err := recv(t, closed, "Close from the Handler"); err != nil {
				t.Fatal(err)
			}
			recv(t, s.Done(), "the server to stop")
			if err := c.Close(); err != nil {
				t.Fatal(err)
			}
			check()
		})
		t.Run(mode.String()+"/server OnState", func(t *testing.T) {
			check := leakcheck.Check(t)
			closed, ready := make(chan error, 1), make(chan struct{})
			var once sync.Once
			var s *server.Server
			c, s, _, _ := stations(t, mode, func(cc *client.Config, sc *server.Config) {
				sc.OnState = func(st iec101.State, _ error) {
					if st == iec101.StateUp {
						<-ready
						once.Do(func() { closed <- s.Close() })
					}
				}
				cc.OnState = func(iec101.State, error) {} // do not wait for the link
			})
			close(ready) // s is set
			if err := recv(t, closed, "Close from OnState"); err != nil {
				t.Fatal(err)
			}
			recv(t, s.Done(), "the server to stop")
			if err := c.Close(); err != nil {
				t.Fatal(err)
			}
			check()
		})
	}
}

// TestCallbackThatDoesNotReturnNormally: a Handler or OnState of the
// client or the server that does not return normally is logged with its
// stack and stops that station only: Done closes, Err wraps ErrClosed and
// names the callback, and the test process goes on.
func TestCallbackThatDoesNotReturnNormally(t *testing.T) {
	type site struct {
		name   string
		client bool // the station that stops
		mod    func(*client.Config, *server.Config)
		// trigger makes the callback run, once the stations are up.
		trigger func(t *testing.T, c *client.Client, s *server.Server)
	}
	spontaneous := func(t *testing.T, _ *client.Client, s *server.Server) {
		if err := s.Send(asdu.New(asdu.CauseSpontaneous, ca, asdu.SinglePoint{IOA: 1, Value: true})); err != nil {
			t.Fatal(err)
		}
	}
	command := func(_ *testing.T, c *client.Client, _ *server.Server) {
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()
		_ = c.Send(ctx, asdu.New(asdu.CauseActivation, ca, asdu.SingleCommand{IOA: 500, Select: true}))
	}
	nothing := func(*testing.T, *client.Client, *server.Server) {}
	sites := []site{
		{"client Handler", true, func(cc *client.Config, _ *server.Config) {
			cc.Handler = func(*asdu.ASDU) { panic(stopValue) }
		}, spontaneous},
		{"client OnState", true, func(cc *client.Config, _ *server.Config) {
			cc.OnState = func(st iec101.State, _ error) {
				if st == iec101.StateUp {
					panic(stopValue)
				}
			}
		}, nothing},
		{"server Handler", false, func(_ *client.Config, sc *server.Config) {
			sc.Handler = server.HandlerFunc(func(*server.Server, *asdu.ASDU) { panic(stopValue) })
		}, command},
		{"server OnState", false, func(cc *client.Config, sc *server.Config) {
			sc.OnState = func(st iec101.State, _ error) {
				if st == iec101.StateUp {
					panic(stopValue)
				}
			}
			cc.OnState = func(iec101.State, error) {} // do not wait for the link
		}, nothing},
	}
	for _, mode := range modes {
		for _, x := range sites {
			t.Run(mode.String()+"/"+x.name, func(t *testing.T) {
				check := leakcheck.Check(t)
				var mu sync.Mutex
				var logs bytes.Buffer
				logger := iec60870.NewStdLogger(log.New(&lockedWriter{mu: &mu, w: &logs}, "", 0))
				c, s, _, _ := stations(t, mode, func(cc *client.Config, sc *server.Config) {
					cc.Logger, sc.Logger = logger, logger
					x.mod(cc, sc)
				})
				x.trigger(t, c, s)
				done, errOf, other := c.Done(), c.Err, func() error { return s.Close() }
				if !x.client {
					done, errOf, other = s.Done(), s.Err, func() error { return c.Close() }
				}
				recv(t, done, "the station to stop")
				name := strings.Fields(x.name)[1]
				if err := errOf(); !errors.Is(err, iec101.ErrClosed) || !strings.Contains(err.Error(), name) {
					t.Errorf("Err = %v, want ErrClosed naming %s", err, name)
				}
				if err := other(); err != nil {
					t.Fatal(err)
				}
				if x.client {
					_ = c.Close()
				} else {
					_ = s.Close()
				}
				mu.Lock()
				text := logs.String()
				mu.Unlock()
				if !strings.Contains(text, "did not return normally") || !strings.Contains(text, "goroutine ") {
					t.Errorf("no log entry with the stack:\n%s", text)
				}
				check()
			})
		}
	}
}

type lockedWriter struct {
	mu *sync.Mutex
	w  *bytes.Buffer
}

func (l *lockedWriter) Write(b []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(b)
}

// TestRequestFromHandlerFailsAtOnce: a request that waits for an answer,
// made from the client's Handler or OnState, returns ErrInHandler at once
// instead of waiting for an answer that goroutine itself would have to
// let through.
func TestRequestFromHandlerFailsAtOnce(t *testing.T) {
	for _, mode := range modes {
		t.Run(mode.String()+"/Handler", func(t *testing.T) {
			check := leakcheck.Check(t)
			got, ready := make(chan error, 1), make(chan struct{})
			var once sync.Once
			var c *client.Client
			c, s, _, _ := stations(t, mode, func(cc *client.Config, _ *server.Config) {
				cc.Handler = func(a *asdu.ASDU) {
					<-ready
					if a.Cause != asdu.CauseSpontaneous {
						return
					}
					once.Do(func() {
						ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
						defer cancel()
						_, err := c.Interrogate(ctx, ca, asdu.QOIStation)
						got <- err
					})
				}
			})
			close(ready) // c is set
			if err := s.Send(asdu.New(asdu.CauseSpontaneous, ca, asdu.SinglePoint{IOA: 1, Value: true})); err != nil {
				t.Fatal(err)
			}
			if err := recv(t, got, "the interrogation from the Handler"); !errors.Is(err, iec101.ErrInHandler) {
				t.Errorf("Interrogate from the Handler: %v, want ErrInHandler", err)
			}
			_ = c.Close()
			_ = s.Close()
			check()
		})
		t.Run(mode.String()+"/OnState", func(t *testing.T) {
			check := leakcheck.Check(t)
			got, ready := make(chan error, 2), make(chan struct{})
			var once sync.Once
			var c *client.Client
			c, s, _, _ := stations(t, mode, func(cc *client.Config, _ *server.Config) {
				cc.OnState = func(st iec101.State, _ error) {
					if st != iec101.StateUp {
						return
					}
					<-ready
					once.Do(func() {
						ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
						defer cancel()
						_, err := c.Interrogate(ctx, ca, asdu.QOIStation)
						got <- err
						got <- c.Send(ctx, asdu.New(asdu.CauseActivation, ca, asdu.ResetProcess{Qualifier: 1}))
					})
				}
			})
			close(ready) // c is set
			if err := recv(t, got, "the interrogation from OnState"); !errors.Is(err, iec101.ErrInHandler) {
				t.Errorf("Interrogate from OnState: %v, want ErrInHandler", err)
			}
			if err := recv(t, got, "Send from OnState"); !errors.Is(err, iec101.ErrInHandler) {
				t.Errorf("Send from OnState: %v, want ErrInHandler", err)
			}
			_ = c.Close()
			_ = s.Close()
			check()
		})
	}
}

// TestRequestsFailWithErrClosed: a request in flight when the client is
// closed, and every request made after Close, fail with ErrClosed, in both
// modes; the last state reported is "closed".
func TestRequestsFailWithErrClosed(t *testing.T) {
	for _, mode := range modes {
		t.Run(mode.String(), func(t *testing.T) {
			check := leakcheck.Check(t)
			held := make(chan struct{}, 1)
			var mu sync.Mutex
			var states []iec101.State
			c, s, _, _ := stations(t, mode, func(cc *client.Config, sc *server.Config) {
				// The interrogation is never answered: it stays in flight.
				sc.Handler = server.HandlerFunc(func(s *server.Server, a *asdu.ASDU) {
					if a.Type == asdu.C_IC_NA_1 {
						select {
						case held <- struct{}{}:
						default:
						}
						return
					}
					(&station{}).HandleASDU(s, a)
				})
				cc.OnState = func(st iec101.State, _ error) { mu.Lock(); states = append(states, st); mu.Unlock() }
			})
			ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
			defer cancel()
			if err := c.WaitUp(ctx); err != nil {
				t.Fatal(err)
			}
			inFlight := make(chan error, 1)
			go func() {
				_, err := c.Interrogate(ctx, ca, asdu.QOIStation)
				inFlight <- err
			}()
			recv(t, held, "the interrogation to reach the server")
			if err := c.Close(); err != nil {
				t.Fatal(err)
			}
			if err := recv(t, inFlight, "the interrogation in flight"); !errors.Is(err, iec101.ErrClosed) {
				t.Errorf("interrogation in flight at Close: %v, want ErrClosed", err)
			}
			after := map[string]func() error{
				"Send": func() error {
					return c.Send(ctx, asdu.New(asdu.CauseActivation, ca, asdu.ResetProcess{Qualifier: 1}))
				},
				"Interrogate": func() error { _, err := c.Interrogate(ctx, ca, asdu.QOIStation); return err },
				"Command": func() error {
					return c.Command(ctx, ca, asdu.SingleCommand{IOA: 500, Select: true})
				},
				"ClockSync": func() error { return c.ClockSync(ctx, ca, time.Now()) },
			}
			for name, call := range after {
				if err := call(); !errors.Is(err, iec101.ErrClosed) {
					t.Errorf("%s after Close: %v, want ErrClosed", name, err)
				}
			}
			mu.Lock()
			if n := len(states); n == 0 || states[n-1] != iec101.StateClosed {
				t.Errorf("states %v, want closed last", states)
			}
			mu.Unlock()
			_ = s.Close()
			check()
		})
	}
}

// TestInterrogationResultLimit: a station that answers an interrogation
// with more ASDUs than Config.MaxResult, and no termination, ends the
// request at the bound with telecontrol.ErrResultLimit, long before its
// deadline, and what the request allocates stays within a budget set by
// the bound, not by what the station sends. It runs on the fake clock of
// testing/synctest.
func TestInterrogationResultLimit(t *testing.T) {
	const limit, flood = 16, 1024
	const budget = limit * 16 << 10 // octets allocated while the request runs
	synctest.Test(t, func(t *testing.T) {
		check := serialline.Goroutines(t)
		a, b := serialline.Pair()
		p := params(iec101.ModeUnbalanced)
		s, err := server.New(b, server.Config{Params: p, LinkAddr: 3, CommonAddr: ca, QueueSize: 2 * flood,
			Handler: server.HandlerFunc(func(s *server.Server, a *asdu.ASDU) {
				if a.Type != asdu.C_IC_NA_1 {
					return
				}
				_ = s.Confirm(a)
				for i := range flood {
					_ = s.Send(asdu.New(asdu.CauseInterrogatedStation, ca, asdu.SinglePoint{IOA: asdu.IOA(i + 1), Value: true}))
				}
				// No termination.
			})})
		if err != nil {
			t.Fatal(err)
		}
		c, err := client.New(a, client.Config{Params: p, LinkAddr: 3, PollInterval: 10 * time.Millisecond, MaxResult: limit})
		if err != nil {
			t.Fatal(err)
		}
		const deadline = time.Hour
		ctx, cancel := context.WithTimeout(context.Background(), deadline)
		defer cancel()
		if err := c.WaitUp(ctx); err != nil {
			t.Fatal(err)
		}
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		start := time.Now()
		got, err := c.Interrogate(ctx, ca, asdu.QOIStation)
		took := time.Since(start)
		runtime.ReadMemStats(&after)
		if !errors.Is(err, telecontrol.ErrResultLimit) || len(got) != limit {
			t.Errorf("Interrogate: %d ASDUs, %v; want %d and ErrResultLimit", len(got), err, limit)
		}
		if took >= deadline {
			t.Errorf("Interrogate took %v, the whole deadline", took)
		}
		if n := after.TotalAlloc - before.TotalAlloc; n > budget {
			t.Errorf("%d octets allocated during the request, want at most %d", n, budget)
		}
		_ = c.Close()
		_ = s.Close()
		check()
	})
}
