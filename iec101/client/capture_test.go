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

func replay(t *testing.T, p capture.Params, addr uint16, steps []capture.Step) {
	ap := p.ASDU()
	a, b := net.Pipe()
	st := &capture.Station{RW: b, Params: p, Addr: addr, Steps: steps}
	go func() { _ = st.Run() }()
	var mu sync.Mutex
	var got []*asdu.ASDU
	c, err := client.New(a, client.Config{
		Params:       iec101.Params{Mode: iec101.ModeUnbalanced, Link: link.Config{FT12: p.FT12(), Timeout: time.Second}, ASDU: ap},
		LinkAddr:     addr,
		PollInterval: time.Millisecond,
		Handler: func(x *asdu.ASDU) {
			mu.Lock()
			got = append(got, x)
			mu.Unlock()
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
	// The expected deliveries, and the requests in order.
	var want []capture.Step
	var reqs []int
	var last *asdu.ASDU
	for i, s := range steps {
		if s.Request {
			reqs = append(reqs, i)
			last = s.ASDU
			continue
		}
		if last == nil || !consumes(last, s.ASDU) {
			want = append(want, s)
		}
	}
	errs := make(chan error, len(reqs))
	for _, i := range reqs {
		for st.Next() < i && ctx.Err() == nil {
			time.Sleep(time.Millisecond)
		}
		x := steps[i].ASDU
		go func() { errs <- request(ctx, c, x) }()
		for st.Next() == i && ctx.Err() == nil {
			time.Sleep(time.Millisecond)
		}
	}
	for !st.Done() && ctx.Err() == nil {
		time.Sleep(time.Millisecond)
	}
	for range reqs {
		if err := <-errs; err != nil {
			t.Errorf("request: %v", err)
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
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n >= len(want) || time.Now().After(deadline) {
			break
		}
		time.Sleep(time.Millisecond)
	}
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
