// SPDX-License-Identifier: MIT

package client

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	iec60870 "github.com/otfabric/go-iec60870-5"
	"github.com/otfabric/go-iec60870-5/asdu"
	iec104 "github.com/otfabric/go-iec60870-5/iec104"
	"github.com/otfabric/go-iec60870-5/iec104/apci"
	"github.com/otfabric/go-iec60870-5/iec104/internal/testutil"
	"github.com/otfabric/go-iec60870-5/internal/testutil/leakcheck"
)

// hookStop is what a test hook ends with when it does not return normally.
const hookStop = "test hook stopped"

// stoppingMetrics is a Metrics and RequestMetrics whose method stop does
// not return normally.
type stoppingMetrics struct {
	iec104.NopMetrics
	stop string
}

func (m stoppingMetrics) check(method string) {
	if method == m.stop {
		panic(hookStop)
	}
}

func (m stoppingMetrics) OnConnect(net.Addr)                               { m.check("OnConnect") }
func (m stoppingMetrics) OnDisconnect(net.Addr, error)                     { m.check("OnDisconnect") }
func (m stoppingMetrics) OnFrameSent(net.Addr, apci.Frame, int)            { m.check("OnFrameSent") }
func (m stoppingMetrics) OnFrameReceived(net.Addr, apci.Frame, int)        { m.check("OnFrameReceived") }
func (m stoppingMetrics) OnDecodeError(net.Addr, error)                    { m.check("OnDecodeError") }
func (m stoppingMetrics) OnRequest(net.Addr, asdu.TypeID, asdu.CommonAddr) { m.check("OnRequest") }
func (m stoppingMetrics) OnRequestDone(net.Addr, asdu.TypeID, asdu.CommonAddr, time.Duration, error) {
	m.check("OnRequestDone")
}
func (m stoppingMetrics) OnRetry(net.Addr, asdu.TypeID, asdu.CommonAddr, int, error) {
	m.check("OnRetry")
}

// Every metrics callback, of RequestMetrics included, that does not return
// normally is logged with its stack and closes the client without waiting
// for anything: the call returns, the close completes, and the failure
// wraps ErrClosed and names the callback. A request whose metrics stop the
// client fails or keeps its result; the retry pause ends at once.
func TestMetricsThatDoNotReturnNormally(t *testing.T) {
	ctx := context.Background()
	req := asdu.New(asdu.CauseActivation, 1, asdu.Interrogation{Qualifier: asdu.QOIStation})
	timeout := func(context.Context) error { return iec104.ErrTimeout }
	done := func(context.Context) error { return nil }
	for _, tc := range []struct {
		method string
		call   func(c *Client) error
		want   error // what the request returns
	}{
		{"Metrics.OnConnect", func(c *Client) error { c.metrics.OnConnect(nil); return nil }, nil},
		{"Metrics.OnDisconnect", func(c *Client) error { c.metrics.OnDisconnect(nil, nil); return nil }, nil},
		{"Metrics.OnFrameSent", func(c *Client) error { c.metrics.OnFrameSent(nil, apci.Frame{}, 6); return nil }, nil},
		{"Metrics.OnFrameReceived", func(c *Client) error { c.metrics.OnFrameReceived(nil, apci.Frame{}, 6); return nil }, nil},
		{"Metrics.OnDecodeError", func(c *Client) error { c.metrics.OnDecodeError(nil, errors.New("x")); return nil }, nil},
		{"RequestMetrics.OnRequest", func(c *Client) error { return c.run(ctx, req, true, done) }, nil},
		{"RequestMetrics.OnRetry", func(c *Client) error { return c.run(ctx, req, true, timeout) }, iec104.ErrTimeout},
		{"RequestMetrics.OnRequestDone", func(c *Client) error { return c.run(ctx, req, true, done) }, nil},
	} {
		t.Run(tc.method, func(t *testing.T) {
			var mu sync.Mutex
			var logs bytes.Buffer
			logger := iec60870.NewStdLogger(log.New(writerFunc(func(p []byte) (int, error) {
				mu.Lock()
				defer mu.Unlock()
				return logs.Write(p)
			}), "", 0))
			_, method, _ := strings.Cut(tc.method, ".")
			c, err := New("127.0.0.1:1", WithLogger(logger), WithMetrics(stoppingMetrics{stop: method}),
				WithRetry(Retry{Attempts: 3, Backoff: time.Hour}))
			if err != nil {
				t.Fatal(err)
			}
			returned := make(chan error, 1)
			go func() { returned <- tc.call(c) }()
			select {
			case err := <-returned:
				if !errors.Is(err, tc.want) {
					t.Errorf("the call returned %v, want %v", err, tc.want)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("the call did not return")
			}
			select {
			case <-c.closeDone:
			case <-time.After(10 * time.Second):
				t.Fatal("the client did not close")
			}
			if err := c.failure(); !errors.Is(err, iec104.ErrClosed) || !strings.Contains(err.Error(), tc.method) {
				t.Errorf("failure %v, want ErrClosed naming %s", err, tc.method)
			}
			if err := c.Connect(ctx); !errors.Is(err, iec104.ErrClosed) {
				t.Errorf("Connect after: %v, want ErrClosed", err)
			}
			mu.Lock()
			text := logs.String()
			mu.Unlock()
			if !strings.Contains(text, "did not return normally") || !strings.Contains(text, "goroutine ") {
				t.Errorf("no log entry with the stack:\n%s", text)
			}
		})
	}
}

// A callback stops the client while Connect runs, and the link confirms
// STARTDT before the cancellation of Connect's context reaches StartDT: a
// metrics hook that stops on the STARTDT con runs on the link's goroutine,
// which then handles that frame, and StartDT can return before the
// goroutine that cancels its context has run. Connect must fail with the
// failure, which wraps ErrClosed, and the application must be told
// "disconnected" once and nothing about the transfer that was confirmed.
//
// The failpoint makes that order certain: it does what abort does first
// (the client is closed, the failure recorded, the Handler muted) and
// leaves the rest (closing closeCh, which cancels Connect's context, and
// the link) to after Connect has returned.
func TestConnectClosedWhileSTARTDTIsConfirmed(t *testing.T) {
	leaks := leakcheck.Check(t)
	srv, addr := station(t)
	type note struct {
		s   iec104.State
		err error
	}
	var mu sync.Mutex
	var notes []note
	var c *Client
	c, err := New(addr, WithParams(testutil.Params()),
		WithStateHandler(func(s iec104.State, err error) {
			mu.Lock()
			notes = append(notes, note{s, err})
			mu.Unlock()
		}),
		withFailpoint(func(name string) {
			if name != "link-published" {
				return
			}
			c.mu.Lock()
			c.closed = true
			c.mu.Unlock()
			c.failMu.Lock()
			c.failed = fmt.Errorf("%w: %w", iec104.ErrClosed, errors.New(hookStop))
			c.failMu.Unlock()
			c.quiet.Store(true)
		}))
	if err != nil {
		t.Fatal(err)
	}
	connected := make(chan error, 1)
	go func() { connected <- c.Connect(context.Background()) }()
	select {
	case err = <-connected:
	case <-time.After(10 * time.Second):
		t.Fatal("Connect did not return")
	}
	if !errors.Is(err, iec104.ErrClosed) || !strings.Contains(err.Error(), hookStop) {
		t.Errorf("Connect: %v, want the failure, which wraps ErrClosed", err)
	}

	// The rest of abort.
	c.mu.Lock()
	close(c.closeCh)
	l := c.last
	c.mu.Unlock()
	if l != nil {
		l.Shutdown()
	}
	go c.finishClose(l)
	closed := make(chan struct{})
	go func() { _ = c.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(10 * time.Second):
		t.Fatal("Close did not return")
	}
	// With the station closed too, once every goroutine the test started
	// has ended nothing is left that could tell the application more.
	_ = srv.Close()
	leaks()

	mu.Lock()
	defer mu.Unlock()
	var got []string
	for _, n := range notes {
		got = append(got, n.s.String())
	}
	if want := "connecting stopped disconnected"; strings.Join(got, " ") != want {
		t.Errorf("the state handler was told %q, want %q", strings.Join(got, " "), want)
	}
	if len(notes) > 0 {
		if n := notes[len(notes)-1]; !errors.Is(n.err, iec104.ErrClosed) || !strings.Contains(fmt.Sprint(n.err), hookStop) {
			t.Errorf("%s with %v, want the failure", n.s, n.err)
		}
	}
}

type writerFunc func(p []byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

// A Dialer that does not return normally after Close has closed the
// client: the close came first, so no failure of the Dialer is recorded,
// and Connect must still fail with ErrClosed, without going on to use a
// connection it does not have. The Dialer waits for closeCh, which Close
// closes with the client closed, so the order is the same every time.
func TestConnectWhenTheDialerStopsDuringClose(t *testing.T) {
	leaks := leakcheck.Check(t)
	var c *Client
	entered := make(chan struct{})
	c, err := New("127.0.0.1:1", WithParams(testutil.Params()),
		WithDialer(dialerFunc(func(context.Context, string, string) (net.Conn, error) {
			close(entered)
			<-c.closeCh
			panic(hookStop)
		})))
	if err != nil {
		t.Fatal(err)
	}
	connected := make(chan error, 1)
	go func() { connected <- c.Connect(context.Background()) }()
	<-entered
	closed := make(chan error, 1)
	go func() { closed <- c.Close() }()
	select {
	case err = <-connected:
	case <-time.After(10 * time.Second):
		t.Fatal("Connect did not return")
	}
	if !errors.Is(err, iec104.ErrClosed) {
		t.Errorf("Connect: %v, want ErrClosed", err)
	}
	select {
	case err = <-closed:
		if err != nil {
			t.Errorf("Close: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Close did not return")
	}
	leaks()
}
