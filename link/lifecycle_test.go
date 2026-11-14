// SPDX-License-Identifier: MIT

package link

import (
	"context"
	"errors"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/otfabric/go-iec60870-5/ft12"
	"github.com/otfabric/go-iec60870-5/internal/testutil/leakcheck"
)

// Contract rows L1, L2 and L10 of the lifecycle matrix (TESTING.md) for
// Primary, Secondary and Balanced. Every wait is on an event; testTimeout
// only bounds a wait that would otherwise hang.

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

// countedConn counts the calls of Close.
type countedConn struct {
	net.Conn
	closes atomic.Int32
}

func (c *countedConn) Close() error { c.closes.Add(1); return c.Conn.Close() }

// failingConn fails every write.
type failingConn struct{ countedConn }

func (c *failingConn) Write([]byte) (int, error) { return 0, errors.New("line failed") }

type closer interface {
	Close() error
	Done() <-chan struct{}
}

// kinds start each kind of station on rw.
var kinds = []struct {
	name  string
	start func(t *testing.T, rw io.ReadWriter) closer
}{
	{"primary", func(t *testing.T, rw io.ReadWriter) closer {
		pr, err := NewPrimary(rw, fast)
		if err != nil {
			t.Fatal(err)
		}
		return pr
	}},
	{"secondary", func(t *testing.T, rw io.ReadWriter) closer {
		s, err := NewSecondary(rw, fast, 1, &station{})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}},
	{"balanced", func(t *testing.T, rw io.ReadWriter) closer {
		b, err := NewBalanced(rw, fast, BalancedConfig{Addr: 1, Remote: 2}, &balancedStation{})
		if err != nil {
			t.Fatal(err)
		}
		return b
	}},
}

type balancedStation struct{ onData func() }

func (b *balancedStation) UserData([]byte) bool {
	if b.onData != nil {
		b.onData()
	}
	return true
}
func (b *balancedStation) Reset(bool) {}

// TestL01CloseTwice: the second Close returns nil and does nothing; the
// transport is closed once.
func TestL01CloseTwice(t *testing.T) {
	for _, k := range kinds {
		t.Run(k.name, func(t *testing.T) {
			check := leakcheck.Check(t)
			a, b := net.Pipe()
			defer func() { _ = b.Close() }()
			conn := &countedConn{Conn: a}
			st := k.start(t, conn)
			for i := range 2 {
				if err := st.Close(); err != nil {
					t.Fatalf("Close #%d: %v", i+1, err)
				}
			}
			if n := conn.closes.Load(); n != 1 {
				t.Fatalf("transport closed %d times, want 1", n)
			}
			check()
		})
	}
}

// TestL01CloseAfterAWriteError: the writer closes the transport when a
// write fails; Close afterwards returns nil and does not close it again.
func TestL01CloseAfterAWriteError(t *testing.T) {
	check := leakcheck.Check(t)
	a, b := net.Pipe()
	defer func() { _ = b.Close() }()
	conn := &failingConn{countedConn{Conn: a}}
	pr, err := NewPrimary(conn, fast)
	if err != nil {
		t.Fatal(err)
	}
	if err := pr.SendNoReply(1, []byte{1}); err != nil {
		t.Fatal(err)
	}
	recv(t, pr.Done(), "the station to stop after the write error")
	if err := pr.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if n := conn.closes.Load(); n != 1 {
		t.Fatalf("transport closed %d times, want 1", n)
	}
	if err := pr.SendNoReply(1, []byte{1}); !errors.Is(err, ErrClosed) {
		t.Fatalf("SendNoReply after Close: %v, want ErrClosed", err)
	}
	check()
}

// TestL02ConcurrentClose: Close from several goroutines at once; all
// return nil, the transport is closed once, and no goroutine is left.
func TestL02ConcurrentClose(t *testing.T) {
	for _, k := range kinds {
		t.Run(k.name, func(t *testing.T) {
			check := leakcheck.Check(t)
			a, b := net.Pipe()
			defer func() { _ = b.Close() }()
			conn := &countedConn{Conn: a}
			st := k.start(t, conn)
			const n = 8
			start := make(chan struct{})
			errs := make(chan error, n)
			for range n {
				go func() { <-start; errs <- st.Close() }()
			}
			close(start) // a barrier: all goroutines call Close at once
			for range n {
				if err := recv(t, errs, "Close to return"); err != nil {
					t.Fatal(err)
				}
			}
			if got := conn.closes.Load(); got != 1 {
				t.Fatalf("transport closed %d times, want 1", got)
			}
			check()
		})
	}
}

// TestL10CallbackClosesAsynchronously: a callback closes the station in a
// new goroutine, as the handler documents say; Close returns once the
// callback has returned, and nothing is left running.
func TestL10CallbackClosesAsynchronously(t *testing.T) {
	t.Run("secondary handler", func(t *testing.T) {
		check := leakcheck.Check(t)
		a, b := net.Pipe()
		closed := make(chan error, 1)
		var se *Secondary
		h := &closingStation{onData: func() { go func() { closed <- se.Close() }() }}
		var err error
		if se, err = NewSecondary(b, fast, 1, h); err != nil {
			t.Fatal(err)
		}
		pr, err := NewPrimary(a, fast)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()
		if _, err := pr.Init(ctx, 1); err != nil {
			t.Fatal(err)
		}
		if err := pr.SendNoReply(1, []byte{1}); err != nil {
			t.Fatal(err)
		}
		if err := recv(t, closed, "Close from the handler"); err != nil {
			t.Fatal(err)
		}
		recv(t, se.Done(), "the secondary to stop")
		if err := pr.Close(); err != nil {
			t.Fatal(err)
		}
		check()
	})
	t.Run("balanced handler", func(t *testing.T) {
		check := leakcheck.Check(t)
		a, b := net.Pipe()
		closed := make(chan error, 1)
		var peer *Balanced
		h := &balancedStation{onData: func() { go func() { closed <- peer.Close() }() }}
		var err error
		if peer, err = NewBalanced(b, fast, BalancedConfig{Addr: 2, Remote: 1}, h); err != nil {
			t.Fatal(err)
		}
		own, err := NewBalanced(a, fast, BalancedConfig{Addr: 1, Remote: 2, DIR: true}, &balancedStation{})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()
		if err := own.ResetLink(ctx); err != nil {
			t.Fatal(err)
		}
		_ = own.Send(ctx, []byte{1}) // the peer may close before it answers
		if err := recv(t, closed, "Close from the handler"); err != nil {
			t.Fatal(err)
		}
		recv(t, peer.Done(), "the peer to stop")
		if err := own.Close(); err != nil {
			t.Fatal(err)
		}
		check()
	})
	t.Run("primary OnFrame", func(t *testing.T) {
		check := leakcheck.Check(t)
		a, b := net.Pipe()
		defer func() { _ = b.Close() }()
		go func() { _, _ = io.Copy(io.Discard, b) }()
		closed := make(chan error, 1)
		var pr *Primary
		cfg := fast
		cfg.OnFrame = func(tx bool, _ ft12.Frame) {
			if tx {
				go func() { closed <- pr.Close() }()
			}
		}
		var err error
		if pr, err = NewPrimary(a, cfg); err != nil {
			t.Fatal(err)
		}
		if err := pr.SendNoReply(1, []byte{1}); err != nil {
			t.Fatal(err)
		}
		if err := recv(t, closed, "Close from OnFrame"); err != nil {
			t.Fatal(err)
		}
		recv(t, pr.Done(), "the primary to stop")
		check()
	})
}

// closingStation is a secondary handler that calls onData for user data.
type closingStation struct {
	station
	onData func()
}

func (s *closingStation) UserData(d []byte, broadcast bool) bool {
	s.onData()
	return s.station.UserData(d, broadcast)
}
