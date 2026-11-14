// SPDX-License-Identifier: MIT

package server

import (
	"bytes"
	"context"
	"errors"
	"log"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	iec60870 "github.com/otfabric/go-iec60870-5"
	"github.com/otfabric/go-iec60870-5/ft12"
	"github.com/otfabric/go-iec60870-5/iec102"
	"github.com/otfabric/go-iec60870-5/internal/testutil/leakcheck"
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

// callbackSites are the application callbacks of the server, each with
// the configuration that installs f as it and the request of the
// controlling station that makes it run (nil: the initialization of the
// link does).
func callbackSites(f func(s *Server)) []struct {
	name    string
	cfg     func(s **Server) Config
	request func(t *testing.T) []byte
} {
	private := func(t *testing.T) []byte {
		priv, err := iec102.New(6, 0, 0, iec102.Raw{Data: []byte{1}}).Encode(iec102.Params{})
		if err != nil {
			t.Fatal(err)
		}
		priv[0] = 200 // a private type: the Handler's
		return priv
	}
	readTime := func(t *testing.T) []byte {
		raw, err := iec102.New(iec102.CauseRequest, 0, 0, iec102.Read{T: iec102.C_TI_NA_2}).Encode(iec102.Params{})
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	return []struct {
		name    string
		cfg     func(s **Server) Config
		request func(t *testing.T) []byte
	}{
		{"Handler", func(s **Server) Config {
			return Config{Handler: HandlerFunc(func(s *Server, _ *iec102.ASDU) { f(s) })}
		}, private},
		{"Clock", func(s **Server) Config {
			return Config{Clock: func() time.Time { f(*s); return time.Now() }}
		}, readTime},
		{"OnState", func(s **Server) Config {
			return Config{OnState: func(st iec102.State, _ error) {
				if st == iec102.StateUp {
					f(*s)
				}
			}}
		}, nil},
	}
}

// run starts a server with cfg, then a link primary that initializes the
// link and sends req, if any. The caller closes both.
func run(t *testing.T, cfg Config, set func(*Server), req []byte) (*Server, *link.Primary, net.Conn) {
	t.Helper()
	a, b := net.Pipe()
	cfg.LinkAddr = 1
	s, err := New(b, cfg)
	if err != nil {
		t.Fatal(err)
	}
	set(s)
	pri, err := link.NewPrimary(a, link.Config{FT12: ft12.Params{AddrSize: 2}, Timeout: 200 * time.Millisecond, Retries: 1})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// The server may stop before the link answers: errors are expected.
	if _, err := pri.Init(ctx, 1); err == nil && req != nil {
		_, _ = pri.SendConfirm(ctx, 1, req)
	}
	return s, pri, a
}

// L10: the Handler, the Clock and OnState call Close themselves. Close
// returns at once, without waiting for the goroutine it runs on, the
// station stops, and Err stays nil.
func TestCloseFromCallbacksSynchronously(t *testing.T) {
	for i, site := range callbackSites(nil) {
		t.Run(site.name, func(t *testing.T) {
			check := leakcheck.Check(t)
			closed := make(chan error, 1)
			var once sync.Once
			site := callbackSites(func(s *Server) { once.Do(func() { closed <- s.Close() }) })[i]
			var s *Server
			var req []byte
			if site.request != nil {
				req = site.request(t)
			}
			srv, pri, _ := run(t, site.cfg(&s), func(x *Server) { s = x }, req)
			if err := within(t, closed, "Close from the "+site.name); err != nil {
				t.Fatal(err)
			}
			within(t, srv.Done(), "the server to stop")
			if err := srv.Err(); err != nil {
				t.Errorf("Err after Close: %v", err)
			}
			_ = pri.Close()
			check()
		})
	}
}

// A Handler, Clock or OnState that does not return normally is logged
// with its stack and stops the server only: Done closes, and Err wraps
// ErrClosed and names the callback.
func TestCallbackThatDoesNotReturnNormally(t *testing.T) {
	for _, site := range callbackSites(func(*Server) { panic(stopValue) }) {
		t.Run(site.name, func(t *testing.T) {
			check := leakcheck.Check(t)
			var mu sync.Mutex
			var logs bytes.Buffer
			var s *Server
			cfg := site.cfg(&s)
			cfg.Logger = iec60870.NewStdLogger(log.New(writerFunc(func(p []byte) (int, error) {
				mu.Lock()
				defer mu.Unlock()
				return logs.Write(p)
			}), "", 0))
			var req []byte
			if site.request != nil {
				req = site.request(t)
			}
			srv, pri, _ := run(t, cfg, func(x *Server) { s = x }, req)
			within(t, srv.Done(), "the server to stop")
			if err := srv.Err(); !errors.Is(err, iec102.ErrClosed) || !strings.Contains(err.Error(), site.name) {
				t.Errorf("Err = %v, want ErrClosed naming %s", err, site.name)
			}
			_ = srv.Close()
			_ = pri.Close()
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

// Err is nil while the server runs and after Close, and names the cause
// when the transport fails under it.
func TestErr(t *testing.T) {
	check := leakcheck.Check(t)
	s, pri, a := run(t, Config{}, func(*Server) {}, nil)
	if err := s.Err(); err != nil {
		t.Fatalf("Err while the server runs: %v", err)
	}
	_ = pri.Close()
	_ = a.Close() // the line fails under the server
	within(t, s.Done(), "the server to stop")
	if err := s.Err(); err == nil {
		t.Error("Err is nil after the transport failed")
	}
	_ = s.Close()
	check()
}
