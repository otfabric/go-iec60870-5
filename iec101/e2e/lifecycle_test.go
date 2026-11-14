// SPDX-License-Identifier: MIT

package e2e

import (
	"context"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/otfabric/go-iec60870-5/asdu"
	"github.com/otfabric/go-iec60870-5/iec101"
	"github.com/otfabric/go-iec60870-5/iec101/client"
	"github.com/otfabric/go-iec60870-5/iec101/server"
	"github.com/otfabric/go-iec60870-5/internal/testutil/leakcheck"
	"github.com/otfabric/go-iec60870-5/internal/testutil/serialline"
)

// Contract rows L1, L2 and L10 of the lifecycle matrix (TESTING.md) for
// the IEC 101 client and server, in both modes. Every wait is on an
// event; testTimeout only bounds a wait that would otherwise hang.

const testTimeout = 10 * time.Second

func recv[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(testTimeout):
		t.Fatalf("timed out waiting for %s", what)
	}
	var zero T
	return zero
}

// countedEnd counts the calls of Close of a transport.
type countedEnd struct {
	io.ReadWriteCloser
	closes atomic.Int32
}

func (c *countedEnd) Close() error { c.closes.Add(1); return c.ReadWriteCloser.Close() }

// stations starts a client and a server on the two ends of a line, each
// end counting its Close calls, with mod applied to both configurations,
// and waits until the client's link is up unless mod sets the client's
// OnState. The caller closes both.
func stations(t *testing.T, mode iec101.Mode, mod func(*client.Config, *server.Config)) (*client.Client, *server.Server, *countedEnd, *countedEnd) {
	t.Helper()
	a, b := serialline.Pair()
	ca1, cb := &countedEnd{ReadWriteCloser: a}, &countedEnd{ReadWriteCloser: b}
	p := params(mode)
	sc := server.Config{Params: p, LinkAddr: 3, Remote: 1, CommonAddr: ca, Handler: &station{}, RetryInterval: 50 * time.Millisecond}
	cc := client.Config{Params: p, LinkAddr: 3, OwnAddr: 1, PollInterval: 10 * time.Millisecond,
		RetryInterval: 50 * time.Millisecond, TestInterval: 50 * time.Millisecond}
	if mod != nil {
		mod(&cc, &sc)
	}
	s, err := server.New(cb, sc)
	if err != nil {
		t.Fatal(err)
	}
	c, err := client.New(ca1, cc)
	if err != nil {
		t.Fatal(err)
	}
	if cc.OnState == nil {
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()
		if err := c.WaitUp(ctx); err != nil {
			t.Fatal(err)
		}
	}
	return c, s, ca1, cb
}

var modes = []iec101.Mode{iec101.ModeUnbalanced, iec101.ModeBalanced}

// TestL01CloseTwice: the second Close of the client and of the server
// returns nil; each transport is closed once.
func TestL01CloseTwice(t *testing.T) {
	for _, mode := range modes {
		t.Run(mode.String(), func(t *testing.T) {
			check := leakcheck.Check(t)
			c, s, cEnd, sEnd := stations(t, mode, nil)
			for i := range 2 {
				if err := c.Close(); err != nil {
					t.Fatalf("client Close #%d: %v", i+1, err)
				}
			}
			for i := range 2 {
				if err := s.Close(); err != nil {
					t.Fatalf("server Close #%d: %v", i+1, err)
				}
			}
			if n, m := cEnd.closes.Load(), sEnd.closes.Load(); n != 1 || m != 1 {
				t.Fatalf("transports closed %d (client) and %d (server) times, want 1", n, m)
			}
			check()
		})
	}
}

// TestL02ConcurrentClose: Close of the client and of the server from
// several goroutines at once; every call returns nil, each transport is
// closed once, and no goroutine is left.
func TestL02ConcurrentClose(t *testing.T) {
	for _, mode := range modes {
		t.Run(mode.String(), func(t *testing.T) {
			check := leakcheck.Check(t)
			c, s, cEnd, sEnd := stations(t, mode, nil)
			const n = 8
			start := make(chan struct{})
			errs := make(chan error, 2*n)
			for range n {
				go func() { <-start; errs <- c.Close() }()
				go func() { <-start; errs <- s.Close() }()
			}
			close(start) // a barrier: all goroutines call Close at once
			for range 2 * n {
				if err := recv(t, errs, "Close to return"); err != nil {
					t.Fatal(err)
				}
			}
			if n, m := cEnd.closes.Load(), sEnd.closes.Load(); n != 1 || m != 1 {
				t.Fatalf("transports closed %d (client) and %d (server) times, want 1", n, m)
			}
			check()
		})
	}
}

// TestL10CallbackClosesAsynchronously: a callback closes its station in a
// new goroutine, as the documents of Handler and OnState say; Close returns
// once the callback has returned, and nothing is left running. ready holds
// the goroutine until the test has the station it closes.
func TestL10CallbackClosesAsynchronously(t *testing.T) {
	for _, mode := range modes {
		t.Run(mode.String()+"/client Handler", func(t *testing.T) {
			check := leakcheck.Check(t)
			closed, ready := make(chan error, 1), make(chan struct{})
			var once sync.Once // the callback may run more than once
			var c *client.Client
			c, s, _, _ := stations(t, mode, func(cc *client.Config, _ *server.Config) {
				cc.Handler = func(*asdu.ASDU) { once.Do(func() { go func() { <-ready; closed <- c.Close() }() }) }
			})
			close(ready) // c and s are set
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
			var once sync.Once // the callback may run more than once
			var c *client.Client
			c, s, _, _ := stations(t, mode, func(cc *client.Config, _ *server.Config) {
				cc.OnState = func(st iec101.State, _ error) {
					if st == iec101.StateUp {
						once.Do(func() { go func() { <-ready; closed <- c.Close() }() })
					}
				}
			})
			close(ready) // c and s are set
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
			closed, ready := make(chan error, 1), make(chan struct{})
			var once sync.Once // the callback may run more than once
			var s *server.Server
			c, s, _, _ := stations(t, mode, func(_ *client.Config, sc *server.Config) {
				sc.Handler = server.HandlerFunc(func(*server.Server, *asdu.ASDU) { once.Do(func() { go func() { <-ready; closed <- s.Close() }() }) })
			})
			close(ready) // c and s are set
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
			var once sync.Once // the callback may run more than once
			var s *server.Server
			c, s, _, _ := stations(t, mode, func(cc *client.Config, sc *server.Config) {
				sc.OnState = func(st iec101.State, _ error) {
					if st == iec101.StateUp {
						once.Do(func() { go func() { <-ready; closed <- s.Close() }() })
					}
				}
				cc.OnState = func(iec101.State, error) {} // do not wait for the link
			})
			close(ready) // c and s are set
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
