// SPDX-License-Identifier: MIT

package server_test

import (
	"context"
	"net"
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

// stillServes checks that the client c is served and that the server has
// exactly the sessions want.
func stillServes(t *testing.T, srv *server.Server, c *client.Client, want int) {
	t.Helper()
	testutil.Eventually(t, "sessions", func() bool { return len(srv.Sessions()) == want })
	if err := c.TestCommand(context.Background(), 1); err != nil {
		t.Fatalf("other session: %v", err)
	}
}

// A state handler that does not return normally ends its own session; the
// server and its other sessions go on.
func TestStateHandlerPanicClosesOnlyItsSession(t *testing.T) {
	srv, addr := serve(t, server.HandlerFunc(confirmAll), server.WithLogger(iec60870.NopLogger()),
		server.WithStateHandler(func(s *server.Session, state iec104.State, _ error) {
			if s.ID() == 2 && state == iec104.StateStarted {
				panic("boom")
			}
		}))
	c1 := connect(t, addr)
	stillServes(t, srv, c1, 1)
	// The session may end before its STARTDT con is written, so Dial may
	// fail; either way the second session does not serve.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if c2, err := client.Dial(ctx, addr, client.WithParams(testutil.Params())); err == nil {
		t.Cleanup(func() { _ = c2.Close() })
		testutil.Eventually(t, "second session closed", func() bool {
			return c2.TestCommand(context.Background(), 1) != nil
		})
	}
	stillServes(t, srv, c1, 1)
	if s := srv.Sessions(); s[0].ID() != 1 {
		t.Fatalf("session %d left, want 1", s[0].ID())
	}
}

// An accept filter that does not return normally rejects that connection.
func TestAcceptFilterPanicRejectsOnlyThatConnection(t *testing.T) {
	var n atomic.Int32
	srv, addr := serve(t, server.HandlerFunc(confirmAll), server.WithLogger(iec60870.NopLogger()),
		server.WithAccept(func(net.Addr) bool {
			if n.Add(1) == 2 {
				panic("boom")
			}
			return true
		}))
	c1 := connect(t, addr)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if c, err := client.Dial(ctx, addr, client.WithParams(testutil.Params())); err == nil {
		_ = c.Close()
		t.Fatal("connection accepted by a filter that did not return")
	}
	stillServes(t, srv, c1, 1)
	c3 := connect(t, addr)
	stillServes(t, srv, c3, 2)
}

// panickingMetrics fails on the frames and handler calls of one ASDU type
// each.
type panickingMetrics struct {
	iec104.NopMetrics
}

func (panickingMetrics) OnFrameReceived(_ net.Addr, f apci.Frame, _ int) {
	if f.Format == apci.FormatI && len(f.ASDU) > 0 && asdu.TypeID(f.ASDU[0]) == asdu.C_SC_NA_1 {
		panic("boom")
	}
}

func (panickingMetrics) OnHandled(_ net.Addr, typ asdu.TypeID, _ asdu.Cause, _ time.Duration) {
	if typ == asdu.C_DC_NA_1 {
		panic("boom")
	}
}

// Metrics hooks that do not return normally end the session they were
// called for, on the protocol goroutine as on the dispatcher.
func TestMetricsPanicClosesOnlyItsSession(t *testing.T) {
	srv, addr := serve(t, server.HandlerFunc(confirmAll), server.WithLogger(iec60870.NopLogger()),
		server.WithMetrics(panickingMetrics{}))
	c1 := connect(t, addr)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for _, obj := range []asdu.InformationObject{asdu.SingleCommand{IOA: 1}, asdu.DoubleCommand{IOA: 1}} {
		c := connect(t, addr)
		stillServes(t, srv, c1, 2)
		_ = c.Command(ctx, 1, obj)
		stillServes(t, srv, c1, 1)
	}
}

// After a Handler panic the session ends at once: what it received and
// had not delivered yet is not delivered to the Handler.
func TestHandlerPanicStopsDelivery(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var handled, received atomic.Int32
	m := &receivedCommands{n: &received}
	srv, addr := serve(t, server.HandlerFunc(func(s *server.Session, req *asdu.ASDU) {
		if handled.Add(1) == 1 {
			close(entered)
			<-release
			panic("boom")
		}
		_ = s.Confirm(req)
	}), server.WithLogger(iec60870.NopLogger()), server.WithMetrics(m))
	c := connect(t, addr)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan struct{}, 3)
	for ioa := range asdu.IOA(3) {
		go func() {
			_ = c.Command(ctx, 1, asdu.SingleCommand{IOA: 10 + ioa})
			done <- struct{}{}
		}()
		if ioa == 0 {
			<-entered
		}
	}
	testutil.Eventually(t, "commands received", func() bool { return received.Load() == 3 })
	close(release)
	testutil.Eventually(t, "session closed", func() bool { return len(srv.Sessions()) == 0 })
	for range 3 {
		<-done
	}
	if n := handled.Load(); n != 1 {
		t.Fatalf("handler called %d times, want 1: delivery went on after it panicked", n)
	}
}

// receivedCommands counts the single commands a server receives.
type receivedCommands struct {
	iec104.NopMetrics
	n *atomic.Int32
}

func (r *receivedCommands) OnFrameReceived(_ net.Addr, f apci.Frame, _ int) {
	if f.Format == apci.FormatI && len(f.ASDU) > 0 && asdu.TypeID(f.ASDU[0]) == asdu.C_SC_NA_1 {
		r.n.Add(1)
	}
}
