// SPDX-License-Identifier: MIT

package server

import (
	"context"
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

// countingConn counts the Close calls on the server's end of the pipe.
type countingConn struct {
	net.Conn
	closes atomic.Int32
}

func (c *countingConn) Close() error { c.closes.Add(1); return c.Conn.Close() }

// counted starts a server and a plain link primary that initialized the
// link to it.
func counted(t *testing.T, cfg Config) (*Server, *countingConn, *link.Primary) {
	t.Helper()
	a, b := net.Pipe()
	conn := &countingConn{Conn: b}
	cfg.LinkAddr = 1
	s, err := New(conn, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	pri, err := link.NewPrimary(a, link.Config{FT12: ft12.Params{AddrSize: 2}, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pri.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := pri.Init(ctx, 1); err != nil {
		t.Fatal(err)
	}
	return s, conn, pri
}

// L1: a second Close returns nil and closes nothing again.
func TestCloseTwice(t *testing.T) {
	s, conn, _ := counted(t, Config{})
	for i := range 2 {
		if err := s.Close(); err != nil {
			t.Fatalf("Close #%d: %v", i+1, err)
		}
	}
	if n := conn.closes.Load(); n != 1 {
		t.Fatalf("transport closed %d times, want 1", n)
	}
	if err := s.Send(iec102.New(iec102.CauseRequest, 0, 0, iec102.SystemTime{})); !errors.Is(err, iec102.ErrClosed) {
		t.Fatalf("Send after Close: %v, want ErrClosed", err)
	}
}

// L2: concurrent Close calls all return once the station stopped, and the
// transport is closed once.
func TestConcurrentClose(t *testing.T) {
	t.Cleanup(leakcheck.Check(t)) // runs after the cleanups counted registers
	s, conn, _ := counted(t, Config{})
	const n = 8
	start := make(chan struct{})
	errs := make(chan error, n)
	var wg sync.WaitGroup
	for range n {
		wg.Go(func() { <-start; errs <- s.Close() })
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
	if st := s.State(); st != iec102.StateClosed {
		t.Fatalf("state %v", st)
	}
}

// L10: the Handler stops the station by calling Close in a new goroutine;
// Close returns once the Handler has returned.
func TestCloseFromHandler(t *testing.T) {
	t.Cleanup(leakcheck.Check(t))
	closed := make(chan error, 1)
	s, _, pri := counted(t, Config{Handler: HandlerFunc(func(s *Server, _ *iec102.ASDU) {
		go func() { closed <- s.Close() }()
	})})
	priv, err := iec102.New(6, 0, 0, iec102.Raw{Data: []byte{1}}).Encode(iec102.Params{})
	if err != nil {
		t.Fatal(err)
	}
	priv[0] = 200 // a private type: the Handler's
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// The Handler may close the line before the link confirmation reaches
	// the primary, so SendConfirm can end with the line closed.
	_, _ = pri.SendConfirm(ctx, 1, priv)
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Close from the Handler did not return")
	}
	if st := s.State(); st != iec102.StateClosed {
		t.Fatalf("state %v", st)
	}
}
