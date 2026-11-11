// SPDX-License-Identifier: MIT

package client

import (
	"errors"
	"testing"
	"time"

	"github.com/otfabric/go-iec60870-5/iec103"
	"github.com/otfabric/go-iec60870-5/internal/testutil/serialline"
	"github.com/otfabric/go-iec60870-5/link"
)

// TestBroadcastAfterClose: a broadcast on a closed client returns
// ErrClosed, not ErrLinkDown.
func TestBroadcastAfterClose(t *testing.T) {
	a, b := serialline.Pair()
	defer func() { _ = b.Close() }()
	c, err := New(a, Config{Link: link.Config{Timeout: 50 * time.Millisecond}, LinkAddr: 3, NoInitGI: true, NoInitTimeSync: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if err := c.BroadcastClockSync(time.Now()); !errors.Is(err, iec103.ErrClosed) {
		t.Fatalf("BroadcastClockSync after Close: %v, want ErrClosed", err)
	}
}
