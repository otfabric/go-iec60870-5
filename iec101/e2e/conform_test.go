// SPDX-License-Identifier: MIT

package e2e

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/otfabric/go-iec60870-5/asdu"
	"github.com/otfabric/go-iec60870-5/iec101"
	"github.com/otfabric/go-iec60870-5/iec101/client"
	"github.com/otfabric/go-iec60870-5/iec101/server"
	"github.com/otfabric/go-iec60870-5/link"
)

// TestStrictProfile: with Params.Strict the client refuses to send what the
// IEC 60870-5-101 profile does not allow, the server mirrors a refused
// control-direction ASDU with the "unknown ..." cause (IEC 60870-5-101:2003
// 7.2.3), and the client drops monitor-direction ASDUs that break the
// profile (a time tag on an interrogation answer, 7.3.1).
func TestStrictProfile(t *testing.T) {
	p := params(iec101.ModeUnbalanced)
	p.Strict = true
	r := start(t, p, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	now := asdu.Now()
	if err := r.c.Send(ctx, asdu.New(asdu.CauseActivation, ca, asdu.SingleCommand{IOA: 1, Time: now})); !errors.Is(err, iec101.ErrProfile) {
		t.Fatalf("client sent C_SC_TA_1: %v", err)
	}
	if err := r.s.Send(asdu.New(asdu.CauseInterrogatedStation, ca, asdu.SinglePoint{IOA: 1, Time: now})); !errors.Is(err, iec101.ErrProfile) {
		t.Fatalf("server queued a time-tagged interrogation answer: %v", err)
	}

	// A lenient server sends what a strict client drops; a lenient client
	// sends what a strict server mirrors as unknown type.
	lp := params(iec101.ModeUnbalanced)
	a, b := net.Pipe()
	s, err := server.New(b, server.Config{Params: lp, LinkAddr: 3, CommonAddr: ca, NoEndOfInit: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	got := make(chan *asdu.ASDU, 8)
	c, err := client.New(a, client.Config{Params: p, LinkAddr: 3, PollInterval: 10 * time.Millisecond,
		Handler: func(x *asdu.ASDU) { got <- x }})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	if err := c.WaitUp(ctx); err != nil {
		t.Fatal(err)
	}
	_ = s.Send(asdu.New(asdu.CauseInterrogatedStation, ca, asdu.SinglePoint{IOA: 1, Time: now}))
	_ = s.Send(asdu.New(asdu.CauseSpontaneous, ca, asdu.SinglePoint{IOA: 2, Time: now}))
	if x := <-got; x.First().Address() != 2 {
		t.Fatalf("strict client delivered %v", x)
	}

	a2, b2 := net.Pipe()
	strict, err := server.New(b2, server.Config{Params: p, LinkAddr: 3, CommonAddr: ca, NoEndOfInit: true,
		Handler: server.HandlerFunc(func(s *server.Server, a *asdu.ASDU) { _ = s.Confirm(a) })})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = strict.Close() }()
	got2 := make(chan *asdu.ASDU, 8)
	c2, err := client.New(a2, client.Config{Params: lp, LinkAddr: 3, PollInterval: 10 * time.Millisecond,
		Handler: func(x *asdu.ASDU) { got2 <- x }})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c2.Close() }()
	if err := c2.WaitUp(ctx); err != nil {
		t.Fatal(err)
	}
	if err := c2.Send(ctx, asdu.New(asdu.CauseActivation, ca, asdu.SingleCommand{IOA: 1, Time: now})); err != nil {
		t.Fatal(err)
	}
	if x := <-got2; x.Cause != asdu.CauseUnknownType || !x.Negative {
		t.Fatalf("strict server answered %v", x)
	}
}

// TestServerBusySetsDFC: while the handler queue is more than half full the
// controlled station reports DFC in every response, before refusing user
// data (IEC 60870-5-101:2003 figure 6, "secondary link busy").
func TestServerBusySetsDFC(t *testing.T) {
	a, b := net.Pipe()
	p := params(iec101.ModeUnbalanced)
	release := make(chan struct{})
	s, err := server.New(b, server.Config{Params: p, LinkAddr: 3, CommonAddr: ca, NoEndOfInit: true,
		Handler: server.HandlerFunc(func(*server.Server, *asdu.ASDU) { <-release })})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	defer close(release)
	pr, _ := link.NewPrimary(a, p.Link)
	defer func() { _ = pr.Close() }()
	ctx := context.Background()
	if _, err := pr.Init(ctx, 3); err != nil {
		t.Fatal(err)
	}
	req, _ := asdu.New(asdu.CauseActivation, ca, asdu.TestCommand{}).Encode(p.ASDU)
	for i := range 40 {
		st, err := pr.SendConfirm(ctx, 3, req)
		if err != nil {
			t.Fatalf("send %d: %v", i, err)
		}
		if st.DFC {
			if st, err := pr.RequestStatus(ctx, 3); err != nil || !st.DFC {
				t.Fatalf("status while busy: %+v %v", st, err)
			}
			return
		}
	}
	t.Fatal("DFC never set before the queue was full")
}

// countingPeer refuses the first n user data.
type countingPeer struct{ refuse, got atomic.Int32 }

func (p *countingPeer) UserData([]byte) bool {
	if p.refuse.Add(-1) >= 0 {
		return false
	}
	p.got.Add(1)
	return true
}
func (*countingPeer) Reset(bool) {}

// TestServerBalancedResendsAfterNack: a balanced controlled station whose
// user data is refused (NACK, DFC set) waits for DFC to clear and sends the
// same ASDU again instead of dropping it (IEC 60870-5-101:2003 figure 8).
func TestServerBalancedResendsAfterNack(t *testing.T) {
	a, b := net.Pipe()
	p := params(iec101.ModeBalanced)
	s, err := server.New(b, server.Config{Params: p, LinkAddr: 3, Remote: 1, CommonAddr: ca, NoEndOfInit: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	cp := &countingPeer{}
	cp.refuse.Store(1)
	peer, err := link.NewBalanced(a, p.Link, link.BalancedConfig{Addr: 1, Remote: 3, DIR: true}, cp)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = peer.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := peer.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Send(asdu.New(asdu.CauseSpontaneous, ca, asdu.SinglePoint{IOA: 1})); err != nil {
		t.Fatal(err)
	}
	for cp.got.Load() == 0 {
		select {
		case <-ctx.Done():
			t.Fatal("the refused ASDU was not sent again")
		case <-time.After(10 * time.Millisecond):
		}
	}
}
