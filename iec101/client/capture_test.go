// SPDX-License-Identifier: MIT

package client_test

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/otfabric/go-iec60870-5/asdu"
	"github.com/otfabric/go-iec60870-5/ft12"
	"github.com/otfabric/go-iec60870-5/iec101"
	"github.com/otfabric/go-iec60870-5/iec101/client"
	"github.com/otfabric/go-iec60870-5/iec101/internal/capture"
	"github.com/otfabric/go-iec60870-5/internal/serialcap"
	"github.com/otfabric/go-iec60870-5/link"
)

// TestCaptureReplay plays every recorded controlled station of the IEC 101
// corpus (iec101/testdata/captures, evidence A to D) against the client:
// the station hands its recorded user data to the client's polls in
// recorded order, and the client makes the recorded requests through its
// API (Interrogate, Command, Send) when the recording reaches them.
//
// Comparison rules: every request the client sends equals the recorded
// one octet for octet; every recorded ASDU of the station reaches the
// application (the Handler) unchanged, except the confirmations and
// terminations the client's own requests consume. The link is dynamic: the
// replayed station sets ACD while recorded data is left, so the class each
// ASDU is fetched with is the client's choice, not the recording's.
//
// The client makes a recorded request when the recorded controlling
// station did, by events, not by time: once the station played every step
// before it, the Handler has every ASDU recorded for the application
// before it, and an identical earlier request (same type, common address
// and first object address, which the client refuses while pending) has
// returned.
func TestCaptureReplay(t *testing.T) {
	for _, r := range capture.Replays {
		recs, err := serialcap.ReadFile(filepath.Join("..", "testdata", "captures", filepath.FromSlash(r.Path)), r.Params.FT12(), r.Ports...)
		if err != nil {
			t.Fatal(err)
		}
		for _, rec := range recs {
			ds := capture.Decode(rec.Line, r.Params)
			var addrs []uint16
			for _, d := range ds {
				if d.Kind == ft12.KindVariable && !d.Control().PRM() && !slices.Contains(addrs, d.Addr) {
					addrs = append(addrs, d.Addr)
				}
			}
			for _, addr := range addrs {
				t.Run(fmt.Sprintf("%s/%s/%d", r.Path, rec.Name, addr), func(t *testing.T) {
					replay(t, r.Params, addr, capture.Script(ds, r.Params, addr))
				})
			}
		}
	}
}

// consumes reports whether the client's request req consumes the station's
// answer a instead of delivering it to the Handler: the confirmation, and
// for an interrogation the termination it waits for.
func consumes(req, a *asdu.ASDU) bool {
	if req.Type != a.Type {
		return false
	}
	switch {
	case a.Cause == asdu.CauseActivationCon || a.Cause == asdu.CauseDeactivationCon || a.Negative:
		return true
	case a.Cause == asdu.CauseActivationTerm:
		return req.Type == asdu.C_IC_NA_1 || req.Type == asdu.C_CI_NA_1
	}
	return false
}

// identical reports whether the client refuses request b while request a
// is pending (telecontrol.ErrBusy): the same type, common address and
// address of the first object.
func identical(a, b *asdu.ASDU) bool {
	fa, fb := a.First(), b.First()
	return a.Type == b.Type && a.CommonAddr == b.CommonAddr && fa != nil && fb != nil && fa.Address() == fb.Address()
}

func closed(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

func replay(t *testing.T, p capture.Params, addr uint16, steps []capture.Step) {
	ap := p.ASDU()
	a, b := net.Pipe()
	st := &capture.Station{RW: b, Params: p, Addr: addr, Steps: steps}
	go func() { _ = st.Run() }()
	var mu sync.Mutex
	var got []*asdu.ASDU
	// wake is signalled at every delivery to the Handler and every return
	// of a request; with st.Changed it is what until waits on.
	wake := make(chan struct{}, 1)
	signal := func() {
		select {
		case wake <- struct{}{}:
		default:
		}
	}
	delivered := func() int {
		mu.Lock()
		defer mu.Unlock()
		return len(got)
	}
	c, err := client.New(a, client.Config{
		Params:       iec101.Params{Mode: iec101.ModeUnbalanced, Link: link.Config{FT12: p.FT12(), Timeout: time.Second}, ASDU: ap},
		LinkAddr:     addr,
		PollInterval: time.Millisecond,
		Handler: func(x *asdu.ASDU) {
			mu.Lock()
			got = append(got, x)
			mu.Unlock()
			signal()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close(); _ = b.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := c.WaitUp(ctx); err != nil {
		t.Fatal(err)
	}
	// until waits for cond, checked again at each step the station plays,
	// each request it receives, each delivery and each request returned.
	until := func(ctx context.Context, cond func() bool) bool {
		for {
			ch := st.Changed()
			if cond() {
				return true
			}
			select {
			case <-ch:
			case <-wake:
			case <-ctx.Done():
				return false
			}
		}
	}
	// The expected deliveries, and the requests in order: for each, how
	// many deliveries come before it and the identical request before it.
	var want []capture.Step
	var reqs, before, prev []int
	var last *asdu.ASDU
	for i, s := range steps {
		if s.Request {
			k := -1
			for j := len(reqs) - 1; j >= 0; j-- {
				if identical(steps[reqs[j]].ASDU, s.ASDU) {
					k = j
					break
				}
			}
			reqs, before, prev = append(reqs, i), append(before, len(want)), append(prev, k)
			last = s.ASDU
			continue
		}
		if last == nil || !consumes(last, s.ASDU) {
			want = append(want, s)
		}
	}
	done := make([]chan struct{}, len(reqs))
	errs := make([]error, len(reqs))
	for k := range done {
		done[k] = make(chan struct{})
	}
	var started sync.WaitGroup // the requests made
	anyFailed := func() bool {
		for k := range done {
			if closed(done[k]) && errs[k] != nil {
				return true
			}
		}
		return false
	}
	for k, i := range reqs {
		if !until(ctx, func() bool {
			return anyFailed() || st.Next() >= i && delivered() >= before[k] && (prev[k] < 0 || closed(done[prev[k]]))
		}) || anyFailed() {
			break
		}
		x := steps[i].ASDU
		started.Go(func() {
			errs[k] = request(ctx, c, x)
			close(done[k])
			signal()
		})
		// The station took the request, or the request failed.
		until(ctx, func() bool { return st.Next() != i || closed(done[k]) && errs[k] != nil })
	}
	until(ctx, func() bool { return st.Done() || anyFailed() })
	started.Wait()
	for k := range reqs {
		if closed(done[k]) && errs[k] != nil {
			t.Errorf("request %d: %v", k, errs[k])
		}
	}
	// Every request as recorded.
	sent := st.Received()
	if len(sent) != len(reqs) {
		t.Fatalf("%d requests sent, %d recorded", len(sent), len(reqs))
	}
	for k, i := range reqs {
		if !bytes.Equal(sent[k], steps[i].Data) {
			t.Errorf("request %d:\n  recorded % x\n  sent     % x", k, steps[i].Data, sent[k])
		}
	}
	// Every recorded ASDU delivered, unchanged and in order.
	dctx, dcancel := context.WithTimeout(ctx, 5*time.Second)
	defer dcancel()
	until(dctx, func() bool { return delivered() >= len(want) })
	mu.Lock()
	defer mu.Unlock()
	if len(got) != len(want) {
		t.Errorf("%d ASDUs delivered, %d recorded for the application", len(got), len(want))
	}
	for i := range min(len(got), len(want)) {
		enc, err := got[i].Encode(ap)
		if err != nil || !bytes.Equal(enc, want[i].Data) {
			t.Errorf("ASDU %d (frame %d): delivered %s\n  recorded % x\n  delivered % x (%v)", i, want[i].Last, got[i], want[i].Data, enc, err)
		}
	}
	t.Logf("%d station ASDUs, %d requests, %d delivered", len(steps)-len(reqs), len(reqs), len(got))
}

// request makes the recorded request x through the client's API.
func request(ctx context.Context, c *client.Client, x *asdu.ASDU) error {
	switch o := x.First().(type) {
	case asdu.Interrogation:
		_, err := c.Interrogate(ctx, x.CommonAddr, o.Qualifier)
		return err
	case asdu.SingleCommand, asdu.DoubleCommand, asdu.StepCommand:
		return c.Command(ctx, x.CommonAddr, o)
	}
	return c.Send(ctx, x)
}
