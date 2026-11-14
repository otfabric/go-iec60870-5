// SPDX-License-Identifier: MIT

package client

import (
	"context"
	"errors"
	"testing"

	"github.com/otfabric/go-iec60870-5/asdu"
	"github.com/otfabric/go-iec60870-5/telecontrol"
)

// An exchange queues no more than its bound: what arrives beyond it is
// not kept, and the request ends with the bound's error once it has taken
// what was queued. A collecting request is bounded by MaxResult and a
// small margin, and by nothing when MaxResult is not positive.
func TestExchangeQueueBound(t *testing.T) {
	req := asdu.New(asdu.CauseActivation, 1, asdu.SingleCommand{IOA: 5})
	x := &exchange{req: req, bound: answering(req), sig: make(chan struct{}, 1)}
	term := req.Reply(asdu.CauseActivationTerm, false)
	for range 10 * answerQueue {
		x.deliver(term)
	}
	if len(x.items) != answerQueue {
		t.Fatalf("%d ASDUs queued, want %d", len(x.items), answerQueue)
	}
	for range answerQueue {
		if _, err := x.next(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := x.next(context.Background()); !errors.Is(err, telecontrol.ErrResultLimit) {
		t.Fatalf("after the queue: %v, want ErrResultLimit", err)
	}

	ic := asdu.New(asdu.CauseActivation, 1, asdu.Interrogation{Qualifier: asdu.QOIStation})
	for _, tc := range []struct {
		opt  Option
		want int
	}{
		{WithMaxResult(4), 4 + queueMargin},
		{WithMaxResult(0), DefaultMaxResult + queueMargin},
		{WithMaxResult(-1), 0},
	} {
		c := &Client{opts: defaultOptions()}
		tc.opt(&c.opts)
		b := c.collecting(ic)
		if b.n != tc.want || (b.n > 0) != errors.Is(b.err, telecontrol.ErrResultLimit) {
			t.Errorf("collecting bound %d, %v; want %d", b.n, b.err, tc.want)
		}
	}
}
