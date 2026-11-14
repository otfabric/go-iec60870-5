// SPDX-License-Identifier: MIT

package link

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	iec60870 "github.com/otfabric/go-iec60870-5"
	"github.com/otfabric/go-iec60870-5/ft12"
	"github.com/otfabric/go-iec60870-5/internal/testutil/leakcheck"
)

// stopValue is what a test callback ends with when it does not return
// normally.
const stopValue = "test callback stopped"

// TestL10CallbackClosesSynchronously: a callback calls Close itself, on the
// goroutine of the station that calls it. Close returns without waiting
// for that goroutine, Done closes, and nothing is left running.
func TestL10CallbackClosesSynchronously(t *testing.T) {
	t.Run("secondary handler", func(t *testing.T) {
		check := leakcheck.Check(t)
		a, b := net.Pipe()
		closed := make(chan error, 1)
		var se *Secondary
		var once sync.Once
		ready := make(chan struct{})
		h := &closingStation{onData: func() { <-ready; once.Do(func() { closed <- se.Close() }) }}
		var err error
		if se, err = NewSecondary(b, fast, 1, h); err != nil {
			t.Fatal(err)
		}
		close(ready)
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
		var once sync.Once
		ready := make(chan struct{})
		h := &balancedStation{onData: func() { <-ready; once.Do(func() { closed <- peer.Close() }) }}
		var err error
		if peer, err = NewBalanced(b, fast, BalancedConfig{Addr: 2, Remote: 1}, h); err != nil {
			t.Fatal(err)
		}
		close(ready)
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
	for _, tx := range []bool{true, false} {
		name := "primary OnFrame received"
		if tx {
			name = "primary OnFrame sent"
		}
		t.Run(name, func(t *testing.T) {
			check := leakcheck.Check(t)
			a, b := net.Pipe()
			defer func() { _ = b.Close() }()
			go func() {
				// The peer acknowledges every frame with E5H.
				buf := make([]byte, 64)
				for {
					if _, err := b.Read(buf); err != nil {
						return
					}
					if _, err := b.Write([]byte{ft12.SingleChar}); err != nil {
						return
					}
				}
			}()
			closed := make(chan error, 1)
			var pr *Primary
			var once sync.Once
			ready := make(chan struct{})
			cfg := fast
			cfg.OnFrame = func(sent bool, _ ft12.Frame) {
				if sent == tx {
					<-ready
					once.Do(func() { closed <- pr.Close() })
				}
			}
			var err error
			if pr, err = NewPrimary(a, cfg); err != nil {
				t.Fatal(err)
			}
			close(ready)
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
}

// TestCallbackThatDoesNotReturnNormally: a callback that does not return
// normally is logged with its stack and stops its station only: Done
// closes, and Err wraps ErrClosed and names the callback.
func TestCallbackThatDoesNotReturnNormally(t *testing.T) {
	type stopping interface {
		closer
		Err() error
	}
	cases := []struct {
		name, callback string
		// start starts the station under test on b and makes the callback
		// run; a is the peer's end.
		start func(t *testing.T, cfg Config, a, b net.Conn) stopping
	}{
		{"primary OnFrame sent", "OnFrame", func(t *testing.T, cfg Config, a, b net.Conn) stopping {
			go func() { _, _ = io.Copy(io.Discard, a) }()
			cfg.OnFrame = func(sent bool, _ ft12.Frame) {
				if sent {
					panic(stopValue)
				}
			}
			pr, err := NewPrimary(b, cfg)
			if err != nil {
				t.Fatal(err)
			}
			if err := pr.SendNoReply(1, []byte{1}); err != nil {
				t.Fatal(err)
			}
			return pr
		}},
		{"primary OnFrame received", "OnFrame", func(t *testing.T, cfg Config, a, b net.Conn) stopping {
			cfg.OnFrame = func(sent bool, _ ft12.Frame) {
				if !sent {
					panic(stopValue)
				}
			}
			pr, err := NewPrimary(b, cfg)
			if err != nil {
				t.Fatal(err)
			}
			go func() { _, _ = a.Write([]byte{ft12.SingleChar}) }()
			return pr
		}},
		{"primary OnDiscard", "OnDiscard", func(t *testing.T, cfg Config, a, b net.Conn) stopping {
			cfg.OnDiscard = func([]byte, error) { panic(stopValue) }
			pr, err := NewPrimary(b, cfg)
			if err != nil {
				t.Fatal(err)
			}
			// Octets that start no frame, then a frame that ends them.
			go func() { _, _ = a.Write([]byte{0x00, 0x01, 0x02, ft12.SingleChar}) }()
			return pr
		}},
		{"secondary handler", "SecondaryHandler", func(t *testing.T, cfg Config, a, b net.Conn) stopping {
			h := &closingStation{onData: func() { panic(stopValue) }}
			se, err := NewSecondary(b, cfg, 1, h)
			if err != nil {
				t.Fatal(err)
			}
			go func() {
				pr, err := NewPrimary(a, fast)
				if err != nil {
					return
				}
				defer func() { _ = pr.Close() }()
				ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
				defer cancel()
				if _, err := pr.Init(ctx, 1); err == nil {
					_, _ = pr.SendConfirm(ctx, 1, []byte{1})
				}
			}()
			return se
		}},
		{"balanced handler", "BalancedHandler", func(t *testing.T, cfg Config, a, b net.Conn) stopping {
			h := &balancedStation{onData: func() { panic(stopValue) }}
			peer, err := NewBalanced(b, cfg, BalancedConfig{Addr: 2, Remote: 1}, h)
			if err != nil {
				t.Fatal(err)
			}
			go func() {
				own, err := NewBalanced(a, fast, BalancedConfig{Addr: 1, Remote: 2, DIR: true}, &balancedStation{})
				if err != nil {
					return
				}
				defer func() { _ = own.Close() }()
				ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
				defer cancel()
				if err := own.ResetLink(ctx); err == nil {
					_ = own.Send(ctx, []byte{1})
				}
			}()
			return peer
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			check := leakcheck.Check(t)
			var mu sync.Mutex
			var logs bytes.Buffer
			cfg := fast
			cfg.Logger = iec60870.NewStdLogger(log.New(writerFunc(func(p []byte) (int, error) {
				mu.Lock()
				defer mu.Unlock()
				return logs.Write(p)
			}), "", 0))
			a, b := net.Pipe()
			st := tc.start(t, cfg, a, b)
			recv(t, st.Done(), "the station to stop")
			if err := st.Err(); !errors.Is(err, ErrClosed) || !strings.Contains(err.Error(), tc.callback) {
				t.Errorf("Err = %v, want ErrClosed naming %s", err, tc.callback)
			}
			if err := st.Close(); err != nil {
				t.Errorf("Close after the stop: %v", err)
			}
			_ = a.Close()
			check()
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

// plainLine is a transport that is not an io.Closer.
type plainLine struct{ rw io.ReadWriter }

func (l plainLine) Read(p []byte) (int, error)  { return l.rw.Read(p) }
func (l plainLine) Write(p []byte) (int, error) { return l.rw.Write(p) }

// TestCloseWithoutCloser: on a transport that is not an io.Closer, Close
// returns without waiting for the reader's pending Read, and a frame that
// Read returns afterwards reaches no handler. Done closes when that Read
// has returned and the reader has ended.
func TestCloseWithoutCloser(t *testing.T) {
	check := leakcheck.Check(t)
	a, b := net.Pipe()
	var calls atomic.Int32
	h := &closingStation{onData: func() { calls.Add(1) }}
	se, err := NewSecondary(plainLine{b}, fast, 1, h)
	if err != nil {
		t.Fatal(err)
	}
	closed := make(chan error, 1)
	go func() { closed <- se.Close() }()
	if err := recv(t, closed, "Close on a transport without Close"); err != nil {
		t.Fatal(err)
	}
	// The reader's pending Read returns this frame: user data, send/no
	// reply to address 1.
	f := ft12.Frame{Kind: ft12.KindVariable, Control: byte(PrimaryControl(FuncUserDataNoRepl, false, false, false)), Addr: 1, Data: []byte{1}}
	raw, err := f.Encode(fast.FT12)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Write(raw); err != nil {
		t.Fatal(err)
	}
	recv(t, se.Done(), "the reader to end")
	_ = a.Close()
	check()
	if n := calls.Load(); n != 0 {
		t.Errorf("the handler was called %d times after Close", n)
	}
}
