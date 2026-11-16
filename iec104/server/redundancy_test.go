// SPDX-License-Identifier: MIT

package server_test

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	iec60870 "github.com/otfabric/go-iec60870-5"

	"github.com/otfabric/go-iec60870-5/asdu"
	iec104 "github.com/otfabric/go-iec60870-5/iec104"
	"github.com/otfabric/go-iec60870-5/iec104/apci"
	"github.com/otfabric/go-iec60870-5/iec104/client"
	"github.com/otfabric/go-iec60870-5/iec104/internal/testutil"
	"github.com/otfabric/go-iec60870-5/iec104/server"
)

// events collects the scaled values a client receives, in order.
type events struct {
	mu      sync.Mutex
	seen    []int16
	changed chan struct{} // signalled after every value, made on first use
}

func (e *events) HandleASDU(a *asdu.ASDU) {
	if v, ok := a.First().(asdu.MeasuredScaled); ok && a.Cause == asdu.CauseSpontaneous {
		e.mu.Lock()
		e.seen = append(e.seen, v.Value)
		e.mu.Unlock()
		select {
		case e.signal() <- struct{}{}:
		default:
		}
	}
}

func (e *events) signal() chan struct{} {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.changed == nil {
		e.changed = make(chan struct{}, 1)
	}
	return e.changed
}

// waitFor waits until cond holds for the values received so far.
func (e *events) waitFor(t *testing.T, what string, cond func(got []int16) bool) {
	t.Helper()
	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	for !cond(e.values()) {
		select {
		case <-e.signal():
		case <-deadline.C:
			t.Fatalf("timed out waiting for %s, got %v", what, e.values())
		}
	}
}

func (e *events) values() []int16 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]int16(nil), e.seen...)
}

func event(i int) *asdu.ASDU {
	return asdu.New(asdu.CauseSpontaneous, 1, asdu.MeasuredScaled{IOA: 1, Value: int16(i)})
}

func enqueue(t *testing.T, srv *server.Server, from, to int) {
	t.Helper()
	for i := from; i <= to; i++ {
		if err := srv.Enqueue(event(i)); err != nil {
			t.Fatalf("Enqueue %d: %v", i, err)
		}
	}
}

func isRange(got []int16, from, to int) bool {
	if len(got) != to-from+1 {
		return false
	}
	for i, v := range got {
		if int(v) != from+i {
			return false
		}
	}
	return true
}

// Events enqueued while no controlling station is connected are delivered,
// in order, to the first one that starts data transfer.
func TestEventQueueBuffersWithoutClient(t *testing.T) {
	srv, addr := serve(t, server.HandlerFunc(confirmAll))
	enqueue(t, srv, 1, 50)
	if n := srv.Pending("default"); n != 50 {
		t.Fatalf("Pending = %d, want 50", n)
	}

	// A connection that is not started gets nothing.
	standby := &events{}
	// What it may receive later is checked once the started connection has
	// been given everything.
	connect(t, addr, client.WithHandler(standby), client.WithAutoStart(false))
	if len(standby.values()) != 0 || srv.Pending("default") != 50 {
		t.Fatalf("a connection in STOPDT received %d events", len(standby.values()))
	}

	rx := &events{}
	c := connect(t, addr, client.WithHandler(rx))
	testutil.Eventually(t, "delivery", func() bool { return len(rx.values()) == 50 })
	if !isRange(rx.values(), 1, 50) {
		t.Errorf("events out of order: %v", rx.values())
	}
	// Acknowledged events leave the queue.
	testutil.Eventually(t, "acknowledgement", func() bool { return srv.Pending("default") == 0 })

	// Live events follow, and a handler's replies are not held up by them.
	enqueue(t, srv, 51, 60)
	if err := c.TestCommand(context.Background(), 1); err != nil {
		t.Errorf("TestCommand: %v", err)
	}
	testutil.Eventually(t, "live events", func() bool { return isRange(rx.values(), 1, 60) })
	if len(standby.values()) != 0 {
		t.Error("the standby connection received queued events")
	}
	if srv.Pending("no-such-group") != 0 || srv.Dropped("no-such-group") != 0 || srv.Dropped("default") != 0 {
		t.Error("Pending/Dropped bookkeeping")
	}
	if err := srv.Enqueue(&asdu.ASDU{Type: asdu.M_SP_NA_1, Cause: 99}); !errors.Is(err, asdu.ErrInvalidValue) {
		t.Errorf("Enqueue of an unencodable ASDU: %v", err)
	}
}

// An event that was sent but not acknowledged when its connection went away
// is sent again on the next one.
func TestEventQueueResendsUnacknowledged(t *testing.T) {
	srv, addr := serve(t, server.HandlerFunc(confirmAll))
	enqueue(t, srv, 1, 5)

	// A controlling station that starts data transfer, takes the I frames
	// and disappears without acknowledging any of them.
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	start, _ := apci.NewU(apci.StartDTAct).MarshalBinary()
	if _, err := conn.Write(start); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	got := 0
	for got < 5 {
		f, err := apci.ReadFrame(conn)
		if err != nil {
			t.Fatalf("reading: %v", err)
		}
		if f.Format == apci.FormatI {
			got++
		}
	}
	if n := srv.Pending("default"); n != 5 {
		t.Fatalf("Pending = %d after sending without acknowledgement, want 5", n)
	}
	_ = conn.Close()
	testutil.Eventually(t, "session gone", func() bool { return len(srv.Sessions()) == 0 })

	rx := &events{}
	connect(t, addr, client.WithHandler(rx))
	testutil.Eventually(t, "redelivery", func() bool { return len(rx.values()) == 5 })
	if !isRange(rx.values(), 1, 5) {
		t.Errorf("redelivered %v, want 1..5 in order", rx.values())
	}
	testutil.Eventually(t, "acknowledgement", func() bool { return srv.Pending("default") == 0 })
}

// Within a redundancy group events go to the one started connection and
// follow a switchover.
func TestEventQueueFollowsSwitchover(t *testing.T) {
	srv, addr := serve(t, server.HandlerFunc(confirmAll))
	rx := &events{}
	// A client-side redundancy group over two connections to this server.
	g, err := client.NewGroup([]string{addr, addr},
		client.WithParams(testutil.Params()), client.WithHandler(rx),
		client.WithReconnect(client.Reconnect{MinDelay: 20 * time.Millisecond}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = g.Close() }()
	ctx := context.Background()
	if err := g.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	testutil.Eventually(t, "both connections", func() bool { return len(srv.Sessions()) == 2 })
	for _, s := range srv.Sessions() {
		if s.Group() != "default" {
			t.Errorf("session in group %q", s.Group())
		}
	}

	enqueue(t, srv, 1, 20)
	testutil.Eventually(t, "events on the first connection", func() bool { return isRange(rx.values(), 1, 20) })

	testutil.Eventually(t, "standby", func() bool {
		for _, c := range g.Clients() {
			if c != g.Active() && c.State() == iec104.StateStopped {
				return true
			}
		}
		return false
	})
	if err := g.Switchover(ctx); err != nil {
		t.Fatalf("Switchover: %v", err)
	}
	enqueue(t, srv, 21, 40)
	testutil.Eventually(t, "events after switchover", func() bool { return len(rx.values()) >= 40 })
	// Nothing is lost and nothing arrives out of order; an event that was
	// in flight during the switchover may arrive twice.
	last := int16(0)
	seen := map[int16]bool{}
	for _, v := range rx.values() {
		if v < last {
			t.Fatalf("event %d after %d", v, last)
		}
		last = v
		seen[v] = true
	}
	if len(seen) != 40 {
		t.Errorf("%d distinct events, want 40", len(seen))
	}
	testutil.Eventually(t, "acknowledgement", func() bool { return srv.Pending("default") == 0 })
}

// A STARTDT on another connection of a declared group takes over: the
// connection that was started gets nothing more, answers included, and the
// queue does not go back to it when the new one stops.
func TestTakeoverEndsThePreviousConnection(t *testing.T) {
	srv, addr := serve(t, server.HandlerFunc(confirmAll), server.WithLogger(iec60870.NopLogger()),
		server.WithRedundancyGroups(server.RedundancyGroup{Name: "scada"}))
	ctx := context.Background()
	rxOld, rxNew := &events{}, &events{}
	oldGone := make(chan struct{})
	var gone sync.Once
	old := connect(t, addr, client.WithHandler(rxOld), client.WithStateHandler(func(s iec104.State, _ error) {
		if s == iec104.StateDisconnected {
			gone.Do(func() { close(oldGone) })
		}
	}))
	standby := connect(t, addr, client.WithAutoStart(false))
	next := connect(t, addr, client.WithHandler(rxNew), client.WithAutoStart(false))
	enqueue(t, srv, 1, 1)
	rxOld.waitFor(t, "the event on the started connection", func(got []int16) bool { return isRange(got, 1, 1) })

	if err := next.StartDT(ctx); err != nil {
		t.Fatal(err)
	}
	// When STARTDT con has arrived, the old connection is already closed on
	// the station's side, so exactly one is started.
	started := 0
	for _, s := range srv.Sessions() {
		if s.Started() {
			started++
		}
	}
	if started != 1 {
		t.Errorf("%d sessions started after the takeover, want 1", started)
	}
	select {
	case <-oldGone:
	case <-time.After(30 * time.Second):
		t.Fatal("the old connection was not closed")
	}
	if err := old.TestCommand(ctx, 1); err == nil {
		t.Error("a request on the connection taken over was answered")
	}
	if st := standby.State(); st != iec104.StateStopped {
		t.Errorf("the connection in STOPDT is %v, want it up and stopped", st)
	}

	enqueue(t, srv, 2, 2)
	// Event 1 comes again on the new connection when the old one had not
	// acknowledged it yet.
	rxNew.waitFor(t, "the event on the new connection", func(got []int16) bool {
		return len(got) > 0 && got[len(got)-1] == 2
	})
	if err := next.StopDT(ctx); err != nil {
		t.Fatal(err)
	}
	enqueue(t, srv, 3, 3)
	// The old connection is disconnected, and its Handler was called for
	// the last time before it said so: what it received is final.
	if got := rxOld.values(); !isRange(got, 1, 1) {
		t.Errorf("the connection taken over received %v after the takeover", got)
	}
	if p := srv.Pending("scada"); p != 1 {
		t.Errorf("Pending = %d with no connection started, want 1", p)
	}
}

// holdFrame holds the protocol goroutine of the session that receives the
// first I frame after arm, until release.
type holdFrame struct {
	iec104.NopMetrics
	armed            atomic.Bool
	once             sync.Once
	entered, release chan struct{}
}

func (h *holdFrame) OnFrameReceived(_ net.Addr, f apci.Frame, _ int) {
	if f.Format == apci.FormatI && h.armed.CompareAndSwap(true, false) {
		close(h.entered)
		<-h.release
	}
}

func (h *holdFrame) free() { h.once.Do(func() { close(h.release) }) }

// A takeover in a declared group ends the previous session at once: from
// the STARTDT con of the new connection on, the previous session no longer
// reports data transfer started, also while its protocol goroutine is busy
// (here in a metrics callback) and has not yet seen its connection closed.
func TestTakeoverStopsThePreviousSessionAtOnce(t *testing.T) {
	hold := &holdFrame{entered: make(chan struct{}), release: make(chan struct{})}
	srv, addr := serve(t, server.HandlerFunc(confirmAll), server.WithLogger(iec60870.NopLogger()), server.WithMetrics(hold),
		server.WithRedundancyGroups(server.RedundancyGroup{Name: "scada"}))
	t.Cleanup(hold.free) // before the server closes
	ctx := context.Background()
	old := connect(t, addr)
	next := connect(t, addr, client.WithAutoStart(false))
	// Only the old connection is started: the I frame of its request holds
	// its session's protocol goroutine.
	hold.armed.Store(true)
	go func() { _ = old.TestCommand(ctx, 1) }()
	select {
	case <-hold.entered:
	case <-time.After(30 * time.Second):
		t.Fatal("the request of the old connection did not arrive")
	}
	if err := next.StartDT(ctx); err != nil {
		t.Fatal(err)
	}
	started := 0
	for _, s := range srv.Sessions() {
		if s.Started() {
			started++
		}
	}
	hold.free()
	if started != 1 {
		t.Errorf("%d sessions started after the takeover, want 1", started)
	}
}

// The default group is only a queue: its connections are independent sessions.
func TestDefaultGroupKeepsIndependentSessions(t *testing.T) {
	srv, addr := serve(t, server.HandlerFunc(confirmAll))
	first := connect(t, addr)
	second := connect(t, addr)
	testutil.Eventually(t, "both sessions", func() bool { return len(srv.Sessions()) == 2 })
	for _, c := range []*client.Client{first, second} {
		if err := c.TestCommand(context.Background(), 1); err != nil {
			t.Errorf("TestCommand: %v", err)
		}
	}
	for _, s := range srv.Sessions() {
		if !s.Started() {
			t.Errorf("session %d is not started", s.ID())
		}
	}
}

func TestEventQueueOverflow(t *testing.T) {
	srv, addr := serve(t, server.HandlerFunc(confirmAll), server.WithEventQueue(3),
		server.WithLogger(iec60870.NopLogger()))
	enqueue(t, srv, 1, 5)
	if p, d := srv.Pending("default"), srv.Dropped("default"); p != 3 || d != 2 {
		t.Fatalf("Pending %d, Dropped %d; want 3 and 2", p, d)
	}
	rx := &events{}
	connect(t, addr, client.WithHandler(rx))
	testutil.Eventually(t, "delivery", func() bool { return len(rx.values()) == 3 })
	if !isRange(rx.values(), 3, 5) {
		t.Errorf("received %v, want the three newest", rx.values())
	}
}

func TestRedundancyGroups(t *testing.T) {
	for _, bad := range [][]server.Option{
		{server.WithEventQueue(-1)},
		{server.WithRedundancyGroups(server.RedundancyGroup{Name: ""})},
		{server.WithRedundancyGroups(server.RedundancyGroup{Name: "a"}, server.RedundancyGroup{Name: "a"})},
		{server.WithRedundancyGroups(server.RedundancyGroup{Name: "a", Allow: []string{"not-an-address"}})},
	} {
		if _, err := server.New(nil, bad...); !errors.Is(err, iec104.ErrInvalidOption) {
			t.Errorf("error = %v, want ErrInvalidOption", err)
		}
	}

	// The first group that allows the client's address takes it; each
	// group has its own queue.
	srv, addr := serve(t, server.HandlerFunc(confirmAll), server.WithRedundancyGroups(
		server.RedundancyGroup{Name: "elsewhere", Allow: []string{"192.0.2.0/24", "2001:db8::1"}},
		server.RedundancyGroup{Name: "local", Allow: []string{"127.0.0.1"}},
		server.RedundancyGroup{Name: "rest"},
	))
	enqueue(t, srv, 1, 4)
	rx := &events{}
	connect(t, addr, client.WithHandler(rx))
	testutil.Eventually(t, "session", func() bool { return len(srv.Sessions()) == 1 })
	if g := srv.Sessions()[0].Group(); g != "local" {
		t.Errorf("session in group %q, want local", g)
	}
	testutil.Eventually(t, "delivery", func() bool { return isRange(rx.values(), 1, 4) })
	testutil.Eventually(t, "acknowledgement", func() bool { return srv.Pending("local") == 0 })
	// The other groups keep their copy for their own controlling stations.
	if srv.Pending("elsewhere") != 4 || srv.Pending("rest") != 4 {
		t.Errorf("Pending elsewhere=%d rest=%d, want 4 each", srv.Pending("elsewhere"), srv.Pending("rest"))
	}

	// A client no group allows is turned away.
	strict, saddr := serve(t, server.HandlerFunc(confirmAll), server.WithLogger(iec60870.NopLogger()),
		server.WithRedundancyGroups(server.RedundancyGroup{Name: "elsewhere", Allow: []string{"192.0.2.7"}}))
	conn, err := net.Dial("tcp", saddr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := conn.Read(make([]byte, 1)); err == nil {
		t.Error("a client outside every redundancy group was served")
	}
	if len(strict.Sessions()) != 0 {
		t.Error("a client outside every redundancy group became a session")
	}

	closed, _ := server.New(nil)
	_ = closed.Close()
	if err := closed.Enqueue(event(1)); !errors.Is(err, server.ErrServerClosed) {
		t.Errorf("Enqueue after Close: %v", err)
	}
}
