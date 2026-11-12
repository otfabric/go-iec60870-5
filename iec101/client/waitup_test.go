// SPDX-License-Identifier: MIT

package client

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/otfabric/go-iec60870-5/iec101"
)

// A transport that is closed before the link ever came up: WaitUp returns the transport
// error at once instead of waiting for its context, Done is closed and Err reports the
// error; the client stops trying to initialize the link. WaitUp used to block until its
// context ended.
func TestWaitUpTransportClosedBeforeLinkUp(t *testing.T) {
	a, b := net.Pipe()
	_ = b.Close()
	c, err := New(a, Config{Params: iec101.DefaultParams(), LinkAddr: 3, RetryInterval: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	begin := time.Now()
	err = c.WaitUp(ctx)
	if err == nil || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, iec101.ErrClosed) {
		t.Fatalf("WaitUp: %v, want the transport error", err)
	}
	if d := time.Since(begin); d > 2*time.Second {
		t.Errorf("WaitUp took %s", d)
	}
	select {
	case <-c.Done():
	default:
		t.Error("Done not closed")
	}
	if c.Err() == nil {
		t.Error("Err is nil")
	}
	if err := c.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		t.Logf("Close: %v", err)
	}
	if err := c.WaitUp(ctx); !errors.Is(err, iec101.ErrClosed) {
		t.Errorf("WaitUp after Close: %v, want ErrClosed", err)
	}
}

// Close while WaitUp waits: WaitUp returns ErrClosed, not the transport error Close
// itself causes.
func TestWaitUpCloseWhileWaiting(t *testing.T) {
	a, b := net.Pipe()
	defer func() { _ = b.Close() }()
	go func() { _, _ = discard(b) }()
	c, err := New(a, Config{Params: iec101.DefaultParams(), LinkAddr: 3, RetryInterval: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- c.WaitUp(context.Background()) }()
	time.Sleep(50 * time.Millisecond)
	_ = c.Close()
	select {
	case err := <-done:
		if !errors.Is(err, iec101.ErrClosed) {
			t.Errorf("WaitUp: %v, want ErrClosed", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("WaitUp did not return after Close")
	}
}

// discard reads and drops what the client sends: a silent station.
func discard(c net.Conn) (int64, error) {
	buf := make([]byte, 256)
	var n int64
	for {
		k, err := c.Read(buf)
		n += int64(k)
		if err != nil {
			return n, err
		}
	}
}
