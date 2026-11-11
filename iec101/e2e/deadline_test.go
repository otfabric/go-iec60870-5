// SPDX-License-Identifier: MIT

package e2e

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/otfabric/go-iec60870-5/asdu"
	"github.com/otfabric/go-iec60870-5/ft12"
	"github.com/otfabric/go-iec60870-5/iec101"
	"github.com/otfabric/go-iec60870-5/iec101/client"
	"github.com/otfabric/go-iec60870-5/internal/testutil/serialline"
	"github.com/otfabric/go-iec60870-5/link"
)

// TestRequestDeadlineWhileTheLinkRepeats is the regression test of a
// request that outlived its deadline: the poller repeats user data the
// station does not acknowledge for Timeout x (Retries + 1), and a request
// waited for the poller instead of its context.
func TestRequestDeadlineWhileTheLinkRepeats(t *testing.T) {
	check := serialline.Goroutines(t)
	a, b := serialline.Pair()
	peer := serialline.NewPeer(t, b, ft12.Params{AddrSize: 1})
	rs := serialline.NewRawSecondary(peer, 3)
	var mu sync.Mutex
	mute := false
	rs.Hook = misbehave(&mu, &mute, func(f ft12.Frame, a [][]byte) [][]byte {
		if serialline.Func(f) == int(link.FuncUserDataConf) {
			return nil
		}
		return a
	})
	rs.Start()
	p := params(iec101.ModeUnbalanced)
	p.Link.Timeout = 300 * time.Millisecond
	c, err := client.New(a, client.Config{Params: p, LinkAddr: 3, PollInterval: 10 * time.Millisecond,
		RetryInterval: 50 * time.Millisecond, RequestTimeout: 5 * time.Second})
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
	err = c.Command(rctx, ca, asdu.SingleCommand{IOA: 500, Select: true})
	if d := time.Since(start); d > 600*time.Millisecond || !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("command with a 150 ms deadline: %v after %v", err, d)
	}
	if err := serialline.Within(t, closeBound, "client Close", c.Close); err != nil {
		t.Error(err)
	}
	_ = b.Close()
	check()
}
