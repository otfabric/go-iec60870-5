// SPDX-License-Identifier: MIT

package client

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/otfabric/go-iec60870-5/iec103"
	"github.com/otfabric/go-iec60870-5/iec103/server"
	"github.com/otfabric/go-iec60870-5/internal/testutil/leakcheck"
	"github.com/otfabric/go-iec60870-5/internal/testutil/serialline"
	"github.com/otfabric/go-iec60870-5/link"
)

func TestMain(m *testing.M) { leakcheck.Main(m) }

// countingEnd counts the Close calls on the client's end of the line.
type countingEnd struct {
	*serialline.End
	closes atomic.Int32
}

func (c *countingEnd) Close() error { c.closes.Add(1); return c.End.Close() }

// station starts a client against protection equipment of package server
// and waits until the link is up.
func station(t *testing.T, cfg Config) (*Client, *countingEnd) {
	t.Helper()
	a, b := serialline.Pair()
	lc := link.Config{Timeout: 50 * time.Millisecond, Retries: 3}
	srv, err := server.New(b, server.Config{Link: lc, LinkAddr: 3, Fun: iec103.FunDistance, SyncDelay: -1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	end := &countingEnd{End: a}
	cfg.Link, cfg.LinkAddr, cfg.NoInitGI, cfg.NoInitTimeSync = lc, 3, true, true
	c, err := New(end, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := c.WaitUp(ctx); err != nil {
		t.Fatal(err)
	}
	return c, end
}

// L1: a second Close returns nil and closes nothing again; requests after
// Close fail with ErrClosed.
func TestCloseTwice(t *testing.T) {
	c, end := station(t, Config{})
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if n := end.closes.Load(); n != 1 {
		t.Fatalf("transport closed %d times, want 1", n)
	}
	if _, err := c.Interrogate(context.Background()); !errors.Is(err, iec103.ErrClosed) {
		t.Fatalf("Interrogate after Close: %v, want ErrClosed", err)
	}
}

// L2: concurrent Close calls all return once the client stopped, the
// transport is closed once and no goroutine is left.
func TestConcurrentClose(t *testing.T) {
	t.Cleanup(leakcheck.Check(t)) // runs after the cleanups station registers
	c, end := station(t, Config{})
	const n = 8
	start := make(chan struct{})
	errs := make(chan error, n)
	var wg sync.WaitGroup
	for range n {
		wg.Go(func() { <-start; errs <- c.Close() })
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := end.closes.Load(); got != 1 {
		t.Fatalf("transport closed %d times, want 1", got)
	}
	if st := c.State(); st != iec103.StateClosed {
		t.Fatalf("state %v", st)
	}
}

// L10: the Handler stops the client by calling Close in a new goroutine;
// Close returns once the Handler has returned.
func TestCloseFromHandler(t *testing.T) {
	t.Cleanup(leakcheck.Check(t))
	closed := make(chan error, 1)
	var once sync.Once
	var c *Client
	ready := make(chan struct{})
	c, _ = station(t, Config{Handler: func(*iec103.ASDU) {
		<-ready
		once.Do(func() { go func() { closed <- c.Close() }() })
	}})
	close(ready) // c is set; the identification after the reset reaches the Handler
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Close from the Handler did not return")
	}
	if st := c.State(); st != iec103.StateClosed {
		t.Fatalf("state %v", st)
	}
}
