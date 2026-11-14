// SPDX-License-Identifier: MIT

package client

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"testing/synctest"

	iec104 "github.com/otfabric/go-iec60870-5/iec104"
	"github.com/otfabric/go-iec60870-5/iec104/internal/testutil"
)

type dialerFunc func(ctx context.Context, network, address string) (net.Conn, error)

func (f dialerFunc) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return f(ctx, network, address)
}

// A dialer that returns neither a connection nor an error fails the
// connect.
func TestDialerWithoutConnection(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, err := New("station:2404", WithParams(testutil.Params()),
			WithDialer(dialerFunc(func(context.Context, string, string) (net.Conn, error) { return nil, nil })))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = c.Close() }()
		if err := c.Connect(context.Background()); !errors.Is(err, iec104.ErrConnectFailed) {
			t.Fatalf("Connect: %v, want ErrConnectFailed", err)
		}
		if s := c.State(); s != iec104.StateDisconnected {
			t.Errorf("state %s, want disconnected", s)
		}
	})
}

// Close cancels the start handler's context and returns after the start
// handler has returned.
func TestCloseWaitsForTheStartHandler(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		var returned atomic.Bool
		d := &failingDialer{ok: 1, drop: make(chan struct{}), call: make(chan struct{}, 1)}
		defer close(d.drop)
		c, err := New("station:2404", WithDialer(d), WithParams(testutil.Params()),
			WithStartHandler(func(ctx context.Context, _ *Client) {
				<-ctx.Done()
				<-release
				returned.Store(true)
			}))
		if err != nil {
			t.Fatal(err)
		}
		if err := c.Connect(context.Background()); err != nil {
			t.Fatal(err)
		}
		closed := make(chan error, 1)
		go func() { closed <- c.Close() }()
		synctest.Wait() // Close has done everything it can; the start handler waits for release
		select {
		case err := <-closed:
			close(release)
			t.Fatalf("Close returned (%v) while the start handler was still running", err)
		default:
		}
		close(release)
		if err := <-closed; err != nil {
			t.Fatal(err)
		}
		if !returned.Load() {
			t.Error("Close returned before the start handler")
		}
	})
}

// A start handler may close the client: Close returns, and a second Close
// from elsewhere returns too.
func TestCloseFromTheStartHandler(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		closed := make(chan error, 1)
		d := &failingDialer{ok: 1, drop: make(chan struct{}), call: make(chan struct{}, 1)}
		defer close(d.drop)
		c, err := New("station:2404", WithDialer(d), WithParams(testutil.Params()),
			WithStartHandler(func(_ context.Context, c *Client) { closed <- c.Close() }))
		if err != nil {
			t.Fatal(err)
		}
		if err := c.Connect(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := <-closed; err != nil {
			t.Fatalf("Close from the start handler: %v", err)
		}
		if s := c.State(); s != iec104.StateDisconnected {
			t.Errorf("state after Close: %s, want disconnected", s)
		}
		if err := c.Close(); err != nil {
			t.Fatalf("second Close: %v", err)
		}
		if err := c.Connect(context.Background()); !errors.Is(err, iec104.ErrClosed) {
			t.Errorf("Connect after Close: %v, want ErrClosed", err)
		}
	})
}
