// SPDX-License-Identifier: MIT

package client

import (
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/otfabric/go-iec60870-5/ft12"
	"github.com/otfabric/go-iec60870-5/iec102"
	"github.com/otfabric/go-iec60870-5/internal/testutil/leakcheck"
	"github.com/otfabric/go-iec60870-5/link"
)

func TestMain(m *testing.M) { leakcheck.Main(m) }

// countingConn counts the Close calls on the client's end of the pipe.
type countingConn struct {
	net.Conn
	closes atomic.Int32
}

func (c *countingConn) Close() error { c.closes.Add(1); return c.Conn.Close() }

func counted(t *testing.T, st *station, mod func(*Config)) (*Client, *countingConn) {
	t.Helper()
	a, b := net.Pipe()
	sec, err := link.NewSecondary(b, link.Config{FT12: ft12.Params{AddrSize: 2}}, 1, st)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sec.Close() })
	conn := &countingConn{Conn: a}
	cfg := Config{LinkAddr: 1, CommonAddr: 1, PollInterval: time.Millisecond, Link: link.Config{Timeout: 100 * time.Millisecond, Retries: 1}}
	if mod != nil {
		mod(&cfg)
	}
	c, err := New(conn, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c, conn
}

// L1: a second Close returns nil and closes nothing again; a request after
// Close fails with ErrClosed.
func TestCloseTwice(t *testing.T) {
	c, conn := counted(t, &station{}, nil)
	if err := c.Init(tctx(t)); err != nil {
		t.Fatal(err)
	}
	for i := range 2 {
		if err := c.Close(); err != nil {
			t.Fatalf("Close #%d: %v", i+1, err)
		}
	}
	if n := conn.closes.Load(); n != 1 {
		t.Fatalf("transport closed %d times, want 1", n)
	}
	if _, err := c.ReadTime(tctx(t)); !errors.Is(err, iec102.ErrClosed) {
		t.Fatalf("ReadTime after Close: %v, want ErrClosed", err)
	}
}

// L2: concurrent Close calls all return once the client stopped, and the
// transport is closed once.
func TestConcurrentClose(t *testing.T) {
	t.Cleanup(leakcheck.Check(t)) // runs after the cleanups counted registers
	c, conn := counted(t, &station{}, nil)
	if err := c.Init(tctx(t)); err != nil {
		t.Fatal(err)
	}
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
	if got := conn.closes.Load(); got != 1 {
		t.Fatalf("transport closed %d times, want 1", got)
	}
	if st := c.State(); st != iec102.StateClosed {
		t.Fatalf("state %v", st)
	}
}

// L10: the Handler stops the client by calling Close in a new goroutine;
// Close returns once the Handler has returned, and the request in progress
// ends with ErrClosed.
func TestCloseFromHandler(t *testing.T) {
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
			once.Do(func() { go func() { closed <- c.Close() }() })
		}
	})
	close(ready)
	if _, err := c.ReadTime(tctx(t)); !errors.Is(err, iec102.ErrClosed) {
		t.Fatalf("ReadTime: %v, want ErrClosed", err)
	}
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Close from the Handler did not return")
	}
	if st := c.State(); st != iec102.StateClosed {
		t.Fatalf("state %v", st)
	}
}
