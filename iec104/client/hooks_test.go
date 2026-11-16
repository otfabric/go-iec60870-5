// SPDX-License-Identifier: MIT

package client_test

import (
	"bytes"
	"context"
	"errors"
	"log"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	iec60870 "github.com/otfabric/go-iec60870-5"
	"github.com/otfabric/go-iec60870-5/asdu"
	iec104 "github.com/otfabric/go-iec60870-5/iec104"
	"github.com/otfabric/go-iec60870-5/iec104/apci"
	"github.com/otfabric/go-iec60870-5/iec104/client"
	"github.com/otfabric/go-iec60870-5/iec104/internal/testutil"
	"github.com/otfabric/go-iec60870-5/iec104/server"
	"github.com/otfabric/go-iec60870-5/internal/testutil/leakcheck"
)

type dialFn func(ctx context.Context, network, address string) (net.Conn, error)

func (f dialFn) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return f(ctx, network, address)
}

// stopMetrics is a Metrics and RequestMetrics whose method stop does not
// return normally.
type stopMetrics struct {
	iec104.NopMetrics
	stop string
}

func (m stopMetrics) check(method string) {
	if method == m.stop {
		panic(stopValue)
	}
}

func (m stopMetrics) OnConnect(net.Addr)                        { m.check("OnConnect") }
func (m stopMetrics) OnDisconnect(net.Addr, error)              { m.check("OnDisconnect") }
func (m stopMetrics) OnFrameSent(net.Addr, apci.Frame, int)     { m.check("OnFrameSent") }
func (m stopMetrics) OnFrameReceived(net.Addr, apci.Frame, int) { m.check("OnFrameReceived") }
func (m stopMetrics) OnRequest(net.Addr, asdu.TypeID, asdu.CommonAddr) {
	m.check("OnRequest")
}
func (m stopMetrics) OnRequestDone(net.Addr, asdu.TypeID, asdu.CommonAddr, time.Duration, error) {
	m.check("OnRequestDone")
}
func (m stopMetrics) OnRetry(net.Addr, asdu.TypeID, asdu.CommonAddr, int, error) {
	m.check("OnRetry")
}

// A Dialer or a metrics callback that does not return normally is logged
// with its stack and closes the client, from whichever goroutine called
// it: Connect, the reconnect loop, the link's, the dispatcher or the
// application's own. Connect or the request fails with ErrClosed, the
// state handler is told "disconnected" with an error that wraps ErrClosed
// and names the callback, Close returns, no goroutine is left, and the
// test process goes on.
func TestHookThatDoesNotReturnNormally(t *testing.T) {
	stopDialer := dialFn(func(context.Context, string, string) (net.Conn, error) { panic(stopValue) })
	// secondStops dials the first time and does not return normally after.
	secondStops := func() client.Dialer {
		var n atomic.Int32
		return dialFn(func(ctx context.Context, network, address string) (net.Conn, error) {
			if n.Add(1) > 1 {
				panic(stopValue)
			}
			return (&net.Dialer{}).DialContext(ctx, network, address)
		})
	}
	for _, tc := range []struct {
		name, callback string
		opts           func() []client.Option
		after          func(t *testing.T, c *client.Client, st *station) // once connected
	}{
		{"Dialer at Connect", "Dialer", func() []client.Option { return []client.Option{client.WithDialer(stopDialer)} }, nil},
		{"Dialer at a reconnect", "Dialer", func() []client.Option {
			return []client.Option{client.WithDialer(secondStops()), client.WithReconnect(client.Reconnect{MinDelay: time.Millisecond})}
		}, func(_ *testing.T, _ *client.Client, st *station) { _ = st.Close() }},
		{"OnConnect", "Metrics.OnConnect", func() []client.Option {
			return []client.Option{client.WithMetrics(stopMetrics{stop: "OnConnect"})}
		}, nil},
		{"OnFrameSent", "Metrics.OnFrameSent", func() []client.Option {
			return []client.Option{client.WithMetrics(stopMetrics{stop: "OnFrameSent"})}
		}, nil},
		{"OnFrameReceived", "Metrics.OnFrameReceived", func() []client.Option {
			return []client.Option{client.WithMetrics(stopMetrics{stop: "OnFrameReceived"})}
		}, nil},
		{"OnDisconnect", "Metrics.OnDisconnect", func() []client.Option {
			return []client.Option{client.WithMetrics(stopMetrics{stop: "OnDisconnect"})}
		}, func(_ *testing.T, _ *client.Client, st *station) { _ = st.Close() }},
		{"OnRequest", "RequestMetrics.OnRequest", func() []client.Option {
			return []client.Option{client.WithMetrics(stopMetrics{stop: "OnRequest"})}
		}, func(t *testing.T, c *client.Client, _ *station) {
			if _, err := c.Interrogate(context.Background(), stationCA, asdu.QOIStation); !errors.Is(err, iec104.ErrClosed) {
				t.Errorf("Interrogate: %v, want ErrClosed", err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Cleanup(leakcheck.Check(t))
			st := newStation(t)
			var mu sync.Mutex
			var logs bytes.Buffer
			notices := make(chan notice, 64)
			opts := append([]client.Option{
				client.WithParams(testutil.Params()), client.WithRequestTimeout(3 * time.Second),
				client.WithStateHandler(func(s iec104.State, err error) { notices <- notice{s, err} }),
				client.WithLogger(iec60870.NewStdLogger(log.New(writerFunc(func(p []byte) (int, error) {
					mu.Lock()
					defer mu.Unlock()
					return logs.Write(p)
				}), "", 0))),
			}, tc.opts()...)
			c, err := client.New(st.addr, opts...)
			if err != nil {
				t.Fatal(err)
			}
			connected := make(chan error, 1)
			go func() { connected <- c.Connect(context.Background()) }()
			var cerr error
			select {
			case cerr = <-connected:
			case <-time.After(10 * time.Second):
				t.Fatal("Connect did not return")
			}
			switch {
			case tc.after != nil && cerr != nil:
				t.Fatalf("Connect: %v", cerr)
			case tc.after != nil:
				tc.after(t, c, st)
			case !errors.Is(cerr, iec104.ErrClosed):
				t.Errorf("Connect: %v, want ErrClosed", cerr)
			}
			for {
				var n notice
				select {
				case n = <-notices:
				case <-time.After(10 * time.Second):
					t.Fatal("the client was not closed")
				}
				if n.state != iec104.StateDisconnected {
					continue
				}
				if !errors.Is(n.err, iec104.ErrClosed) || !strings.Contains(n.err.Error(), tc.callback) {
					t.Errorf("disconnected with %v, want ErrClosed naming %s", n.err, tc.callback)
				}
				break
			}
			within(t, "Close", func() { _ = c.Close() })
			if s := c.State(); s != iec104.StateDisconnected {
				t.Errorf("state %s", s)
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

// stopLogger is an application logger none of whose calls returns
// normally.
type stopLogger struct{ calls atomic.Int64 }

func (l *stopLogger) Debugf(string, ...any) { l.calls.Add(1); panic(stopValue) }
func (l *stopLogger) Infof(string, ...any)  { l.calls.Add(1); panic(stopValue) }
func (l *stopLogger) Warnf(string, ...any)  { l.calls.Add(1); panic(stopValue) }
func (l *stopLogger) Errorf(string, ...any) { l.calls.Add(1); panic(stopValue) }

// A logger none of whose calls returns normally, Errorf included, loses
// every entry and nothing else: the client and the station connect,
// answer an interrogation and close as without it.
func TestLoggerThatDoesNotReturnNormally(t *testing.T) {
	t.Cleanup(leakcheck.Check(t))
	var cl, sl stopLogger
	st := newStation(t, server.WithLogger(&sl))
	c := dial(t, st.addr, client.WithLogger(&cl))
	if _, err := c.Interrogate(context.Background(), stationCA, asdu.QOIStation); err != nil {
		t.Errorf("Interrogate: %v", err)
	}
	within(t, "Close", func() { _ = c.Close() })
	within(t, "the station's Close", func() { _ = st.Close() })
	if cl.calls.Load() == 0 || sl.calls.Load() == 0 {
		t.Errorf("the loggers were called %d (client) and %d (station) times, want both", cl.calls.Load(), sl.calls.Load())
	}
}
