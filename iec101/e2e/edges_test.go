// SPDX-License-Identifier: MIT

package e2e

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"net"
	"sync"
	"testing"
	"time"

	iec60870 "github.com/otfabric/go-iec60870-5"
	"github.com/otfabric/go-iec60870-5/asdu"
	"github.com/otfabric/go-iec60870-5/iec101"
	"github.com/otfabric/go-iec60870-5/iec101/client"
	"github.com/otfabric/go-iec60870-5/iec101/server"
	"github.com/otfabric/go-iec60870-5/link"
	"github.com/otfabric/go-iec60870-5/telecontrol"
)

func TestStrings(t *testing.T) {
	for _, s := range []string{iec101.ModeBalanced.String(), iec101.ModeUnbalanced.String(),
		iec101.StateDown.String(), iec101.StateUp.String(), iec101.StateClosed.String(), iec101.State(9).String()} {
		if s == "" {
			t.Error("empty")
		}
	}
}

// A raw link primary drives the server: reset of user process, undecodable
// user data, a full request queue and the class queues.
func TestServerUnbalancedEdges(t *testing.T) {
	a, b := net.Pipe()
	p := params(iec101.ModeUnbalanced)
	release := make(chan struct{})
	var mu sync.Mutex
	handled := 0
	s, err := server.New(b, server.Config{Params: p, LinkAddr: 3, CommonAddr: ca, InitCause: asdu.InitLocalReset, QueueSize: 2,
		Handler: server.HandlerFunc(func(s *server.Server, a *asdu.ASDU) {
			<-release
			mu.Lock()
			handled++
			mu.Unlock()
		})})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if s.Params().ASDU.MaxSize != p.Link.FT12.MaxData() {
		t.Errorf("max size %d", s.Params().ASDU.MaxSize)
	}
	pr, _ := link.NewPrimary(a, p.Link)
	defer func() { _ = pr.Close() }()
	ctx := context.Background()
	if _, err := pr.Init(ctx, 3); err != nil {
		t.Fatal(err)
	}
	if _, err := pr.ResetLink(ctx, 3); err != nil { // second reset: no second end of initialization
		t.Fatal(err)
	}
	if n := s.Pending(server.Class1); n != 1 {
		t.Fatalf("class 1 pending %d, want one end of initialization", n)
	}
	d, _, err := pr.RequestData(ctx, 3, link.Class1)
	ei, derr := asdu.Decode(d, p.ASDU)
	if err != nil || derr != nil || ei.First().(asdu.EndOfInitialization).Cause != asdu.InitLocalReset {
		t.Fatalf("EI %v %v %v", ei, err, derr)
	}
	// Reset of user process empties the queues and reports a remote reset.
	_ = s.SendClass(asdu.New(asdu.CausePeriodic, ca, asdu.SinglePoint{IOA: 1}), server.Class2)
	if s.Pending(server.Class2) != 1 {
		t.Fatal("class 2 not queued")
	}
	if _, err := pr.ResetUser(ctx, 3); err != nil {
		t.Fatal(err)
	}
	if s.Pending(server.Class2) != 0 || s.Pending(server.Class1) != 1 {
		t.Fatalf("after reset user: %d / %d", s.Pending(server.Class1), s.Pending(server.Class2))
	}
	d, _, _ = pr.RequestData(ctx, 3, link.Class1)
	if ei, _ = asdu.Decode(d, p.ASDU); ei == nil || ei.First().(asdu.EndOfInitialization).Cause != asdu.InitRemoteReset {
		t.Fatalf("EI after reset user: %v", ei)
	}
	// The queue bound.
	for range 2 {
		if err := s.SendClass(asdu.New(asdu.CausePeriodic, ca, asdu.SinglePoint{IOA: 1}), server.Class2); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SendClass(asdu.New(asdu.CausePeriodic, ca, asdu.SinglePoint{IOA: 1}), server.Class2); !errors.Is(err, server.ErrQueueFull) {
		t.Fatalf("queue full: %v", err)
	}
	if err := s.Send(asdu.New(asdu.CauseSpontaneous, 300, asdu.SinglePoint{IOA: 1})); !errors.Is(err, asdu.ErrInvalidValue) {
		t.Error("unencodable ASDU queued")
	}
	// Undecodable user data is acknowledged and dropped.
	if _, err := pr.SendConfirm(ctx, 3, []byte{0xFF}); err != nil {
		t.Fatal(err)
	}
	// The handler is blocked: once its queue is full the station refuses
	// user data with NACK and DFC.
	req, _ := asdu.New(asdu.CauseActivation, ca, asdu.ClockSync{Time: asdu.Now()}).Encode(p.ASDU)
	var refused bool
	for range 100 {
		st, err := pr.SendConfirm(ctx, 3, req)
		if errors.Is(err, link.ErrNack) && st.DFC {
			refused = true
			break
		}
	}
	close(release)
	if !refused {
		t.Fatal("station never refused user data")
	}
}

func TestServerWithoutHandlerRejects(t *testing.T) {
	a, b := net.Pipe()
	p := params(iec101.ModeBalanced)
	s, _ := server.New(b, server.Config{Params: p, LinkAddr: 3, Remote: 1, NoEndOfInit: true})
	defer func() { _ = s.Close() }()
	var states []iec101.State
	var mu sync.Mutex
	var logs bytes.Buffer
	got := make(chan *asdu.ASDU, 8)
	c, _ := client.New(a, client.Config{Params: p, LinkAddr: 3, OwnAddr: 1, TestInterval: -1, RetryInterval: 20 * time.Millisecond,
		OnState: func(s iec101.State, _ error) { mu.Lock(); states = append(states, s); mu.Unlock() },
		Logger:  iec60870.NewStdLogger(log.New(&logs, "", 0)),
		Handler: func(a *asdu.ASDU) { got <- a }})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.WaitUp(ctx); err != nil {
		t.Fatal(err)
	}
	var neg *telecontrol.NegativeError
	if err := c.ClockSync(ctx, ca, time.Now()); !errors.As(err, &neg) || neg.Cause() != asdu.CauseUnknownType {
		t.Fatalf("no handler: %v", err)
	}
	// Send without waiting: the rejection reaches the handler.
	if err := c.Send(ctx, asdu.New(asdu.CauseActivation, ca, asdu.ResetProcess{Qualifier: 1})); err != nil {
		t.Fatal(err)
	}
	if a := <-got; a.Cause != asdu.CauseUnknownType {
		t.Errorf("handler got %v", a)
	}
	if s.Pending(server.Class1) != 0 {
		t.Error("pending")
	}
	_ = c.Close()
	_ = c.Close()
	mu.Lock()
	if len(states) < 2 || states[0] != iec101.StateUp || states[len(states)-1] != iec101.StateClosed {
		t.Errorf("states %v", states)
	}
	mu.Unlock()
	if logs.Len() == 0 {
		t.Error("nothing logged")
	}
	if err := c.Send(ctx, asdu.New(asdu.CauseActivation, ca, asdu.ResetProcess{})); !errors.Is(err, iec101.ErrLinkDown) {
		t.Errorf("send after close: %v", err)
	}
}

func TestWaitUpTimeout(t *testing.T) {
	a, b := net.Pipe()
	defer func() { _ = b.Close() }()
	c, _ := client.New(a, client.Config{Params: params(iec101.ModeUnbalanced), LinkAddr: 3, RetryInterval: 10 * time.Millisecond})
	defer func() { _ = c.Close() }()
	go func() { // a silent line: read and discard
		buf := make([]byte, 64)
		for {
			if _, err := b.Read(buf); err != nil {
				return
			}
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if err := c.WaitUp(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("WaitUp: %v", err)
	}
	if _, err := c.Read(ctx, ca, 1); !errors.Is(err, iec101.ErrLinkDown) {
		t.Fatalf("request while down: %v", err)
	}
}

type rawPeer struct{}

func (rawPeer) UserData([]byte) bool { return true }
func (rawPeer) Reset(bool)           {}

// A raw balanced peer sends undecodable user data, then goes silent: the
// client's link test detects the loss.
func TestClientBalancedSupervision(t *testing.T) {
	a, b := net.Pipe()
	p := params(iec101.ModeBalanced)
	line := &flaky{ReadWriteCloser: b}
	peer, err := link.NewBalanced(line, p.Link, link.BalancedConfig{Addr: 3, Remote: 1}, rawPeer{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = peer.Close() }()
	down := make(chan struct{}, 1)
	c, _ := client.New(a, client.Config{Params: p, LinkAddr: 3, OwnAddr: 1, TestInterval: 20 * time.Millisecond, RetryInterval: 20 * time.Millisecond,
		OnState: func(s iec101.State, _ error) {
			if s == iec101.StateDown {
				select {
				case down <- struct{}{}:
				default:
				}
			}
		}})
	defer func() { _ = c.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.WaitUp(ctx); err != nil {
		t.Fatal(err)
	}
	if err := peer.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if err := peer.Send(ctx, []byte{0xFF}); err != nil { // acknowledged, dropped as undecodable
		t.Fatal(err)
	}
	line.off.Store(true)
	select {
	case <-down:
	case <-ctx.Done():
		t.Fatal("link loss not detected by the link test")
	}
}

// Done and Err report the end of the transport, in both modes.
func TestDoneOnTransportLoss(t *testing.T) {
	for _, mode := range []iec101.Mode{iec101.ModeUnbalanced, iec101.ModeBalanced} {
		r := start(t, params(mode), nil)
		if r.c.Err() != nil || r.s.Err() != nil {
			t.Fatal("error while running")
		}
		_ = r.c.Close() // closes the client's end of the line
		select {
		case <-r.s.Done():
		case <-time.After(5 * time.Second):
			t.Fatalf("%v: server did not notice the line closing", mode)
		}
		if r.s.Err() == nil {
			t.Errorf("%v: server Err nil after transport loss", mode)
		}
		<-r.c.Done()
		if r.c.Err() != nil {
			t.Errorf("%v: client closed by the application reports %v", mode, r.c.Err())
		}
	}
}

// busySecondary is a raw unbalanced secondary whose station is busy: every
// response carries DFC (IEC 60870-5-101:2003 figures 6 and 9).
type busySecondary struct{}

func (busySecondary) UserData([]byte, bool) bool { return false }
func (busySecondary) Class1() []byte             { return nil }
func (busySecondary) Class2() []byte             { return nil }
func (busySecondary) HasClass1() bool            { return false }
func (busySecondary) Reset(bool)                 {}
func (busySecondary) Busy() bool                 { return true }

// While the station reports DFC the client holds user data back and gives
// it up with link.ErrDataFlow instead of sending it (figures 5 and 8).
func TestClientHoldsBackWhileDFC(t *testing.T) {
	a, b := net.Pipe()
	p := params(iec101.ModeUnbalanced)
	sec, err := link.NewSecondary(b, p.Link, 3, busySecondary{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sec.Close() }()
	// Init waits for DFC = 0 and gives up: the link never comes up, and a
	// request is refused at once.
	c, _ := client.New(a, client.Config{Params: p, LinkAddr: 3, RetryInterval: 20 * time.Millisecond, PollInterval: 5 * time.Millisecond})
	defer func() { _ = c.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.Send(ctx, asdu.New(asdu.CauseActivation, ca, asdu.ResetProcess{Qualifier: 1})); !errors.Is(err, iec101.ErrLinkDown) {
		t.Fatalf("send while the link cannot start: %v", err)
	}
	if c.State() == iec101.StateUp {
		t.Fatal("link up although the station reports DFC")
	}
}

// A send on a line that stopped answering fails with the link's error and
// takes the link down; the client initializes it again once the line is
// back (both the poll-wait and the send-first path of the poller).
func TestClientSendOnDeadLine(t *testing.T) {
	var line *flaky
	r := start(t, params(iec101.ModeUnbalanced), func(c io.ReadWriteCloser) io.ReadWriteCloser { line = &flaky{ReadWriteCloser: c}; return line })
	ctx := context.Background()
	line.off.Store(true)
	for range 3 {
		sctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		err := r.c.Send(sctx, asdu.New(asdu.CauseActivation, ca, asdu.ResetProcess{Qualifier: 1}))
		cancel()
		if err == nil {
			t.Fatal("send succeeded on a dead line")
		}
		if r.c.State() != iec101.StateUp {
			break
		}
	}
	line.off.Store(false)
	wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := r.c.WaitUp(wctx); err != nil {
		t.Fatal(err)
	}
}
