// SPDX-License-Identifier: MIT

package e2e

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/otfabric/go-iec60870-5/ft12"
	"github.com/otfabric/go-iec60870-5/iec103"
	"github.com/otfabric/go-iec60870-5/iec103/client"
	"github.com/otfabric/go-iec60870-5/internal/testutil/serialline"
	"github.com/otfabric/go-iec60870-5/link"
)

// TestRequestDeadlineWhileTheLinkRepeats is the regression test of a
// request that outlived its deadline: the link loop repeats user data the
// equipment does not acknowledge for Timeout x (Retries + 1), and a
// request waited for the loop instead of its context.
func TestRequestDeadlineWhileTheLinkRepeats(t *testing.T) {
	check := serialline.Goroutines(t)
	a, b := serialline.Pair()
	rs := rawEquipment(t, b)
	var mu sync.Mutex
	mute := false
	rs.Hook = misbehave(&mu, &mute, func(f ft12.Frame, a [][]byte) [][]byte {
		if serialline.Func(f) == int(link.FuncUserDataConf) {
			return nil
		}
		return a
	})
	rs.Start()
	c, err := client.New(a, client.Config{Link: link.Config{Timeout: 300 * time.Millisecond, Retries: 3}, LinkAddr: 3,
		PollInterval: 10 * time.Millisecond, NoInitGI: true, NoInitTimeSync: true, RequestTimeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.WaitUp(ctx); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	mute = true
	mu.Unlock()
	rctx, rcancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer rcancel()
	start := time.Now()
	err = c.Command(rctx, fun, 16, iec103.DPIOn)
	if d := time.Since(start); d > 600*time.Millisecond || !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("command with a 150 ms deadline: %v after %v", err, d)
	}
	if err := serialline.Within(t, closeBound, "client Close", c.Close); err != nil {
		t.Error(err)
	}
	_ = b.Close()
	check()
}
