// SPDX-License-Identifier: MIT

package server

import (
	"bytes"
	"context"
	"errors"
	"log"
	"strings"
	"sync"
	"testing"
	"time"

	iec60870 "github.com/otfabric/go-iec60870-5"
	"github.com/otfabric/go-iec60870-5/cp"
	"github.com/otfabric/go-iec60870-5/ft12"
	"github.com/otfabric/go-iec60870-5/iec103"
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

// source is a DisturbanceSource whose List calls f.
type source struct{ f func() }

func (s source) List() []iec103.RecordedFault            { s.f(); return nil }
func (source) Record(uint16) (*iec103.Disturbance, bool) { return nil, false }
func (source) Transmitted(uint16, bool)                  {}

// site is an application callback of the server: the configuration that
// installs f as it, and what makes it run: an ASDU handed to the handler
// goroutine, or (nil) the reset of the link by a primary.
type site struct {
	name string
	cfg  func(s **Server, f func(*Server)) Config
	in   *iec103.ASDU
}

func sites() []site {
	return []site{
		{"Handler", func(_ **Server, f func(*Server)) Config {
			return Config{Handler: HandlerFunc(func(s *Server, _ *iec103.ASDU) { f(s) })}
		}, iec103.New(iec103.CauseGI, 3, iec103.FunGlobal, iec103.InfGIEnd, iec103.GIInit{SCN: 1})},
		{"OnTimeSync", func(s **Server, f func(*Server)) Config {
			return Config{OnTimeSync: func(time.Time, bool) { f(*s) }}
		}, iec103.New(iec103.CauseTimeSync, 3, iec103.FunGlobal, iec103.InfTimeSync, iec103.TimeSync{Time: cp.At(time.Now())})},
		{"DisturbanceSource", func(s **Server, f func(*Server)) Config {
			return Config{Disturbances: source{f: func() { f(*s) }}}
		}, iec103.New(iec103.CauseDisturbanceData, 3, 128, 0, iec103.DisturbanceOrder{TOO: iec103.TOORequestList})},
		{"OnState", func(s **Server, f func(*Server)) Config {
			return Config{OnState: func(st iec103.State, _ error) {
				if st == iec103.StateUp {
					f(*s)
				}
			}}
		}, nil},
	}
}

// startSite starts a server with cfg and makes the callback of x run.
func startSite(t *testing.T, cfg Config, set func(*Server), x site) (*Server, *link.Primary) {
	t.Helper()
	a, b := serialline.Pair()
	t.Cleanup(func() { _ = b.Close() })
	cfg.LinkAddr, cfg.Fun = 3, 128
	s, err := New(a, cfg)
	if err != nil {
		t.Fatal(err)
	}
	set(s)
	if x.in != nil {
		s.in <- inbound{a: x.in, at: time.Now()}
		return s, nil
	}
	pri, err := link.NewPrimary(b, link.Config{FT12: ft12.Params{AddrSize: 1}, Timeout: 200 * time.Millisecond, Retries: 1})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = pri.Init(ctx, 3) // the server may stop before it answers
	return s, pri
}

// L10: the Handler, OnTimeSync, the DisturbanceSource and OnState call
// Close themselves. Close returns at once, without waiting for the
// goroutine it runs on, the station stops, and Err stays nil.
func TestCloseFromCallbacksSynchronously(t *testing.T) {
	for _, x := range sites() {
		t.Run(x.name, func(t *testing.T) {
			check := leakcheck.Check(t)
			closed := make(chan error, 1)
			var once sync.Once
			var s *Server
			cfg := x.cfg(&s, func(s *Server) { once.Do(func() { closed <- s.Close() }) })
			srv, pri := startSite(t, cfg, func(v *Server) { s = v }, x)
			if err := within(t, closed, "Close from "+x.name); err != nil {
				t.Fatal(err)
			}
			within(t, srv.Done(), "the server to stop")
			if err := srv.Err(); err != nil {
				t.Errorf("Err after Close: %v", err)
			}
			if pri != nil {
				_ = pri.Close()
			}
			check()
		})
	}
}

// A Handler, OnTimeSync, DisturbanceSource or OnState that does not return
// normally is logged with its stack and stops the server only: Done
// closes, and Err wraps ErrClosed and names the callback.
func TestCallbackThatDoesNotReturnNormally(t *testing.T) {
	for _, x := range sites() {
		t.Run(x.name, func(t *testing.T) {
			check := leakcheck.Check(t)
			var mu sync.Mutex
			var logs bytes.Buffer
			var s *Server
			cfg := x.cfg(&s, func(*Server) { panic(stopValue) })
			cfg.Logger = iec60870.NewStdLogger(log.New(writerFunc(func(p []byte) (int, error) {
				mu.Lock()
				defer mu.Unlock()
				return logs.Write(p)
			}), "", 0))
			srv, pri := startSite(t, cfg, func(v *Server) { s = v }, x)
			within(t, srv.Done(), "the server to stop")
			if err := srv.Err(); !errors.Is(err, iec103.ErrClosed) || !strings.Contains(err.Error(), x.name) {
				t.Errorf("Err = %v, want ErrClosed naming %s", err, x.name)
			}
			_ = srv.Close()
			if pri != nil {
				_ = pri.Close()
			}
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

// Done closes when the transport fails under the server, and Err names
// the cause; Err is nil while it runs.
func TestDoneAndErr(t *testing.T) {
	check := leakcheck.Check(t)
	a, b := serialline.Pair()
	s, err := New(a, Config{LinkAddr: 3, Fun: 128})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.Done():
		t.Fatal("Done closed while the server runs")
	default:
	}
	if err := s.Err(); err != nil {
		t.Fatalf("Err while the server runs: %v", err)
	}
	_ = b.Close() // the line fails under the server
	within(t, s.Done(), "the server to stop")
	if err := s.Err(); err == nil {
		t.Error("Err is nil after the transport failed")
	}
	_ = s.Close()
	check()
}
