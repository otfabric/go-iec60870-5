// SPDX-License-Identifier: MIT

package client

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
	"github.com/otfabric/go-iec60870-5/asdu"
	iec104 "github.com/otfabric/go-iec60870-5/iec104"
	"github.com/otfabric/go-iec60870-5/iec104/apci"
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

type writerFunc func(p []byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }
