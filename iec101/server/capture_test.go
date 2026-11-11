// SPDX-License-Identifier: MIT

package server_test

import (
	"context"
	"encoding/hex"
	"fmt"
	"net"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/otfabric/go-iec60870-5/asdu"
	"github.com/otfabric/go-iec60870-5/ft12"
	"github.com/otfabric/go-iec60870-5/iec101"
	"github.com/otfabric/go-iec60870-5/iec101/internal/capture"
	"github.com/otfabric/go-iec60870-5/iec101/server"
	"github.com/otfabric/go-iec60870-5/internal/serialcap"
	"github.com/otfabric/go-iec60870-5/link"
)

// TestCaptureReplay plays the recorded controlling stations of the IEC 101
// corpus that send requests against the server: a link.Primary resets the
// link, sends each recorded request and polls until the server has nothing
// left. The handler answers each request the way the recorded station did,
// through the server's API: Confirm, Negative and Terminate for the
// confirmations and terminations (the server builds them from the
// request), Send for the process data (the application's).
//
// Comparison rule: the ASDUs the server transmits equal the recorded ones
// octet for octet, as a set (the server's class 1 and class 2 queues order
// them by class, the recording by time). End of initialization is switched
// off because the recordings start after the link was set up.
func TestCaptureReplay(t *testing.T) {
	played := 0
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
				steps := capture.Script(ds, r.Params, addr)
				if !slices.ContainsFunc(steps, func(s capture.Step) bool { return s.Request }) {
					continue // nothing for the server to answer
				}
				played++
				t.Run(fmt.Sprintf("%s/%d", r.Path, addr), func(t *testing.T) { replayServer(t, r.Params, addr, steps) })
			}
		}
	}
	if played == 0 {
		t.Fatal("no recording with requests")
	}
}

func replayServer(t *testing.T, p capture.Params, addr uint16, steps []capture.Step) {
	ap := p.ASDU()
	// answers[i] are the recorded station ASDUs after request i.
	answers := map[int][]*asdu.ASDU{}
	var reqs []*asdu.ASDU
	var before, want []string
	for _, s := range steps {
		if s.Request {
			reqs = append(reqs, s.ASDU)
			continue
		}
		want = append(want, hex.EncodeToString(s.Frame.Frame.Data))
		if len(reqs) == 0 {
			before = append(before, hex.EncodeToString(s.Frame.Frame.Data))
			continue
		}
		answers[len(reqs)-1] = append(answers[len(reqs)-1], s.ASDU)
	}
	seen := 0
	h := server.HandlerFunc(func(s *server.Server, a *asdu.ASDU) {
		i := seen
		seen++
		for _, x := range answers[i] {
			var err error
			switch {
			case x.Type == a.Type && x.Negative:
				err = s.Negative(a)
			case x.Type == a.Type && (x.Cause == asdu.CauseActivationCon || x.Cause == asdu.CauseDeactivationCon):
				err = s.Confirm(a)
			case x.Type == a.Type && x.Cause == asdu.CauseActivationTerm:
				err = s.Terminate(a)
			default:
				err = s.Send(x)
			}
			if err != nil {
				t.Errorf("answer: %v", err)
			}
		}
	})
	a, b := net.Pipe()
	srv, err := server.New(b, server.Config{
		Params:   iec101.Params{Mode: iec101.ModeUnbalanced, Link: link.Config{FT12: p.FT12(), Timeout: time.Second}, ASDU: ap},
		LinkAddr: addr, Handler: h, NoEndOfInit: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	pr, err := link.NewPrimary(a, link.Config{FT12: p.FT12(), Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pr.Close(); _ = srv.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := pr.Init(ctx, addr); err != nil {
		t.Fatal(err)
	}
	var got []string
	poll := func(st link.Status) {
		for {
			class := link.Class2
			if st.ACD {
				class = link.Class1
			}
			data, s, err := pr.RequestData(ctx, addr, class)
			if err != nil {
				t.Fatal(err)
			}
			st = s
			if data != nil {
				got = append(got, hex.EncodeToString(data))
				continue
			}
			if !st.ACD {
				return
			}
		}
	}
	if len(before) > 0 {
		t.Logf("%d ASDUs before the first request are not the server's to send", len(before))
	}
	for i, x := range reqs {
		raw, err := x.Encode(ap)
		if err != nil {
			t.Fatal(err)
		}
		st, err := pr.SendConfirm(ctx, addr, raw)
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		time.Sleep(10 * time.Millisecond) // the handler runs on its own goroutine
		poll(st)
	}
	want = slices.DeleteFunc(want, func(s string) bool { return slices.Contains(before, s) })
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("server sent\n  %v\nrecorded\n  %v", got, want)
	}
	t.Logf("%d requests, %d ASDUs answered as recorded", len(reqs), len(got))
}
