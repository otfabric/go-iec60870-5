// SPDX-License-Identifier: MIT

package e2e

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/otfabric/go-iec60870-5/ft12"
	"github.com/otfabric/go-iec60870-5/iec102"
	"github.com/otfabric/go-iec60870-5/iec102/client"
	"github.com/otfabric/go-iec60870-5/iec102/server"
)

// cutter ends a request's context once the client has received a number of
// frames with user data: in the middle of the station's answer.
type cutter struct {
	mu     sync.Mutex
	left   int
	cancel context.CancelFunc
}

func (c *cutter) arm(after int, cancel context.CancelFunc) {
	c.mu.Lock()
	c.left, c.cancel = after, cancel
	c.mu.Unlock()
}

func (c *cutter) frame(tx bool, f ft12.Frame) {
	if tx || f.Kind != ft12.KindVariable {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cancel == nil {
		return
	}
	if c.left--; c.left == 0 {
		c.cancel()
		c.cancel = nil
	}
}

// TestUnfinishedAnswerNotTaken: a read that ends at its context in the
// middle of the station's answer leaves the rest of that answer at the
// station. The next request drains it first: a read of another time range
// returns only its own periods, and a time read its time.
func TestUnfinishedAnswerNotTaken(t *testing.T) {
	cut := &cutter{}
	r := newRig(t, func(c *client.Config, _ *server.Config) { c.Link.OnFrame = cut.frame })
	whole := iec102.Read{T: iec102.C_CI_NU_2, First: 1, Last: 8, From: hour(1, 0), To: hour(3, 0)}
	unfinished := func() {
		t.Helper()
		c, cancel := context.WithCancel(ctx(t))
		defer cancel()
		cut.arm(5, cancel) // the confirmation and four of 48 periods
		got, err := r.c.ReadTotals(c, 11, whole)
		if !errors.Is(err, context.Canceled) || len(got) >= 48 {
			t.Fatalf("read of the whole profile: %d periods, %v; want it cut short", len(got), err)
		}
	}

	unfinished()
	got, err := r.c.ReadTotals(ctx(t), 11, iec102.Read{T: iec102.C_CI_NU_2, First: 1, Last: 1, From: hour(2, 10), To: hour(2, 11)})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Time != hour(2, 10) || got[1].Time != hour(2, 11) {
		var times []string
		for _, p := range got {
			times = append(times, p.Time.String())
		}
		t.Fatalf("read of 2024-03-02 10:00..11:00: %d periods %v", len(got), times)
	}

	unfinished()
	now, err := r.c.ReadTime(ctx(t))
	if err != nil {
		t.Fatalf("ReadTime after an unfinished read: %v", err)
	}
	if now.String() != iec102.TimeBOf(r.s.Now(), nil).String() {
		t.Fatalf("ReadTime %v", now)
	}
}
