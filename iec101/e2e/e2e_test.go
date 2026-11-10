// SPDX-License-Identifier: MIT

package e2e

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/otfabric/go-iec60870-5/asdu"
	"github.com/otfabric/go-iec60870-5/ft12"
	"github.com/otfabric/go-iec60870-5/iec101"
	"github.com/otfabric/go-iec60870-5/iec101/client"
	"github.com/otfabric/go-iec60870-5/iec101/server"
	"github.com/otfabric/go-iec60870-5/link"
	"github.com/otfabric/go-iec60870-5/telecontrol"
)

const ca asdu.CommonAddr = 5

// station is a small controlled station: three points, a counter and a
// select-before-operate single command at 500 that drives point 100.
type station struct {
	mu       sync.Mutex
	point    bool
	selected bool
	clock    time.Time
	resets   int
}

func (st *station) HandleASDU(s *server.Server, a *asdu.ASDU) {
	if a.CommonAddr != ca && a.CommonAddr != 0xFF {
		_ = s.Reject(a, asdu.CauseUnknownCommonAddr)
		return
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	switch o := a.First().(type) {
	case asdu.Interrogation:
		_ = s.Confirm(a)
		cause := o.Qualifier.Cause()
		_ = s.Send(asdu.New(cause, ca,
			asdu.SinglePoint{IOA: 100, Value: st.point},
			asdu.SinglePoint{IOA: 101, Value: true}))
		_ = s.Send(asdu.New(cause, ca, asdu.MeasuredFloat{IOA: 200, Value: 49.5}))
		_ = s.Terminate(a)
	case asdu.CounterInterrogation:
		_ = s.Confirm(a)
		_ = s.Send(asdu.New(o.Cause(), ca, asdu.IntegratedTotal{IOA: 300, Value: 123456, Sequence: 1}))
		_ = s.Terminate(a)
	case asdu.Read:
		if o.IOA != 200 {
			_ = s.Reject(a, asdu.CauseUnknownIOA)
			return
		}
		_ = s.Send(asdu.New(asdu.CauseRequest, ca, asdu.MeasuredFloat{IOA: 200, Value: 49.5}))
	case asdu.SingleCommand:
		if o.IOA != 500 {
			_ = s.Reject(a, asdu.CauseUnknownIOA)
			return
		}
		switch {
		case a.Cause == asdu.CauseDeactivation:
			st.selected = false
			_ = s.Confirm(a)
		case o.Select:
			st.selected = true
			_ = s.Confirm(a)
		case !st.selected:
			_ = s.Negative(a)
		default:
			st.selected = false
			st.point = o.Value
			_ = s.Confirm(a)
			_ = s.Send(asdu.New(asdu.CauseReturnRemote, ca, asdu.SinglePoint{IOA: 100, Value: o.Value}))
			_ = s.Terminate(a)
		}
	case asdu.ClockSync:
		st.clock = o.Time.Time
		_ = s.Confirm(a)
	case asdu.TestCommand, asdu.DelayAcquisition:
		_ = s.Confirm(a)
	case asdu.ResetProcess:
		st.resets++
		_ = s.Confirm(a)
	default:
		_ = s.Reject(a, asdu.CauseUnknownType)
	}
}

func params(mode iec101.Mode) iec101.Params {
	p := iec101.DefaultParams()
	p.Mode = mode
	p.Link.Timeout = 100 * time.Millisecond
	return p
}

type rig struct {
	c   *client.Client
	s   *server.Server
	st  *station
	got chan *asdu.ASDU
}

func start(t *testing.T, p iec101.Params, wrap func(io.ReadWriteCloser) io.ReadWriteCloser) *rig {
	t.Helper()
	a, b := net.Pipe()
	var sb io.ReadWriteCloser = b
	if wrap != nil {
		sb = wrap(b)
	}
	r := &rig{st: &station{}, got: make(chan *asdu.ASDU, 64)}
	var err error
	r.s, err = server.New(sb, server.Config{Params: p, LinkAddr: 3, Remote: 1, CommonAddr: ca, Handler: r.st, RetryInterval: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	r.c, err = client.New(a, client.Config{
		Params: p, LinkAddr: 3, OwnAddr: 1,
		PollInterval: 10 * time.Millisecond, RetryInterval: 50 * time.Millisecond, TestInterval: 50 * time.Millisecond,
		Handler: func(a *asdu.ASDU) { r.got <- a },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.c.Close(); _ = r.s.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r.c.WaitUp(ctx); err != nil {
		t.Fatal(err)
	}
	return r
}

func (r *rig) expect(t *testing.T, typ asdu.TypeID, cause asdu.Cause) *asdu.ASDU {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case a := <-r.got:
			if a.Type == typ && a.Cause == cause {
				return a
			}
		case <-timeout:
			t.Fatalf("no %s/%s", typ, cause)
		}
	}
}

func TestServices(t *testing.T) {
	for _, mode := range []iec101.Mode{iec101.ModeUnbalanced, iec101.ModeBalanced} {
		t.Run(mode.String(), func(t *testing.T) {
			r := start(t, params(mode), nil)
			ctx := context.Background()
			ei := r.expect(t, asdu.M_EI_NA_1, asdu.CauseInitialized)
			if ei.CommonAddr != ca {
				t.Errorf("end of initialization CA %d", ei.CommonAddr)
			}

			data, err := r.c.Interrogate(ctx, ca, asdu.QOIStation)
			if err != nil || len(data) != 2 || data[0].Len() != 2 {
				t.Fatalf("interrogation: %v %v", data, err)
			}
			tot, err := r.c.CounterInterrogate(ctx, ca, asdu.CounterGeneral, asdu.FreezeRead)
			if err != nil || len(tot) != 1 || tot[0].First().(asdu.IntegratedTotal).Value != 123456 {
				t.Fatalf("counters: %v %v", tot, err)
			}
			rd, err := r.c.Read(ctx, ca, 200)
			if err != nil || rd.First().(asdu.MeasuredFloat).Value != 49.5 {
				t.Fatalf("read: %v %v", rd, err)
			}
			var neg *telecontrol.NegativeError
			if _, err := r.c.Read(ctx, ca, 999); !errors.As(err, &neg) || neg.Cause() != asdu.CauseUnknownIOA {
				t.Fatalf("read unknown: %v", err)
			}

			// Execute without select is refused; select then execute works.
			if err := r.c.Command(ctx, ca, asdu.SingleCommand{IOA: 500, Value: true}); !errors.As(err, &neg) {
				t.Fatalf("execute without select: %v", err)
			}
			if err := r.c.Command(ctx, ca, asdu.SingleCommand{IOA: 500, Value: true, Select: true}); err != nil {
				t.Fatal(err)
			}
			if err := r.c.Command(ctx, ca, asdu.SingleCommand{IOA: 500, Value: true}); err != nil {
				t.Fatal(err)
			}
			if a := r.expect(t, asdu.M_SP_NA_1, asdu.CauseReturnRemote); !a.First().(asdu.SinglePoint).Value {
				t.Error("return information")
			}
			if err := r.c.Command(ctx, ca, asdu.SingleCommand{IOA: 500, Select: true}); err != nil {
				t.Fatal(err)
			}
			if err := r.c.Deactivate(ctx, ca, asdu.SingleCommand{IOA: 500, Select: true}); err != nil {
				t.Fatal(err)
			}

			when := time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)
			if err := r.c.ClockSync(ctx, ca, when); err != nil {
				t.Fatal(err)
			}
			r.st.mu.Lock()
			if !r.st.clock.Equal(when) {
				t.Errorf("clock %v", r.st.clock)
			}
			r.st.mu.Unlock()
			for _, f := range []func() error{
				func() error { return r.c.TestCommand(ctx, ca) },
				func() error { return r.c.DelayAcquisition(ctx, ca, 1500*time.Millisecond) },
				func() error { return r.c.ResetProcess(ctx, ca, asdu.ResetProcessGeneral) },
			} {
				if err := f(); err != nil {
					t.Fatal(err)
				}
			}
			if err := r.c.ResetProcess(ctx, 9, asdu.ResetProcessGeneral); !errors.As(err, &neg) || neg.Cause() != asdu.CauseUnknownCommonAddr {
				t.Fatalf("unknown CA: %v", err)
			}

			// Spontaneous and class 2 data reach the handler.
			if err := r.s.Send(asdu.New(asdu.CauseSpontaneous, ca, asdu.SinglePoint{IOA: 101})); err != nil {
				t.Fatal(err)
			}
			if err := r.s.SendClass(asdu.New(asdu.CausePeriodic, ca, asdu.MeasuredFloat{IOA: 200, Value: 1}), server.Class2); err != nil {
				t.Fatal(err)
			}
			// Class 1 and class 2 are polled independently: either may come first.
			seen := map[asdu.Cause]bool{}
			timeout := time.After(5 * time.Second)
			for !seen[asdu.CauseSpontaneous] || !seen[asdu.CausePeriodic] {
				select {
				case a := <-r.got:
					seen[a.Cause] = true
				case <-timeout:
					t.Fatalf("spontaneous/periodic not both delivered: %v", seen)
				}
			}
			if r.c.State() != iec101.StateUp || r.s.State() != iec101.StateUp {
				t.Errorf("states %v %v", r.c.State(), r.s.State())
			}
		})
	}
}

// flaky drops written frames while off is set, as a broken line would.
type flaky struct {
	io.ReadWriteCloser
	off atomic.Bool
}

func (f *flaky) Write(b []byte) (int, error) {
	if f.off.Load() {
		return len(b), nil
	}
	return f.ReadWriteCloser.Write(b)
}

func TestLinkRecovery(t *testing.T) {
	for _, mode := range []iec101.Mode{iec101.ModeUnbalanced, iec101.ModeBalanced} {
		t.Run(mode.String(), func(t *testing.T) {
			var line *flaky
			p := params(mode)
			r := start(t, p, func(c io.ReadWriteCloser) io.ReadWriteCloser { line = &flaky{ReadWriteCloser: c}; return line })
			ctx := context.Background()
			line.off.Store(true)
			deadline := time.Now().Add(5 * time.Second)
			for r.c.State() == iec101.StateUp {
				if time.Now().After(deadline) {
					t.Fatal("link loss not detected")
				}
				_ = r.c.TestCommand(ctx, ca)
			}
			if err := r.c.TestCommand(ctx, ca); !errors.Is(err, iec101.ErrLinkDown) {
				t.Fatalf("request while down: %v", err)
			}
			line.off.Store(false)
			wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			if err := r.c.WaitUp(wctx); err != nil {
				t.Fatal(err)
			}
			if _, err := r.c.Interrogate(ctx, ca, asdu.QOIStation); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestConfigErrors(t *testing.T) {
	a, b := net.Pipe()
	defer func() { _ = a.Close() }()
	defer func() { _ = b.Close() }()
	p := iec101.DefaultParams()
	p.Link.FT12.AddrSize = 0
	if _, err := client.New(a, client.Config{Params: p}); !errors.Is(err, iec101.ErrInvalidConfig) {
		t.Errorf("unbalanced without address: %v", err)
	}
	p = iec101.DefaultParams()
	p.ASDU.MaxSize = 300
	if _, err := server.New(a, server.Config{Params: p}); !errors.Is(err, iec101.ErrInvalidConfig) {
		t.Errorf("max size: %v", err)
	}
	p = iec101.DefaultParams()
	if _, err := client.New(a, client.Config{Params: p, LinkAddr: 256}); !errors.Is(err, iec101.ErrInvalidConfig) {
		t.Errorf("address: %v", err)
	}
	p.ASDU.IOASize = 4
	if _, err := server.New(a, server.Config{Params: p}); !errors.Is(err, iec101.ErrInvalidConfig) {
		t.Errorf("ioa size: %v", err)
	}
	p = iec101.DefaultParams()
	p.Link.FT12 = ft12.Params{AddrSize: 7}
	if _, err := server.New(a, server.Config{Params: p}); !errors.Is(err, iec101.ErrInvalidConfig) {
		t.Errorf("ft12: %v", err)
	}
	_ = link.Class1
}

func TestCloseFailsRequests(t *testing.T) {
	r := start(t, params(iec101.ModeUnbalanced), nil)
	_ = r.c.Close()
	if err := r.c.WaitUp(context.Background()); !errors.Is(err, iec101.ErrClosed) {
		t.Fatalf("WaitUp after close: %v", err)
	}
	if _, err := r.c.Interrogate(context.Background(), ca, asdu.QOIStation); err == nil {
		t.Fatal("request after close succeeded")
	}
	if r.c.State() != iec101.StateClosed {
		t.Errorf("state %v", r.c.State())
	}
	_ = r.s.Close()
	if err := r.s.Send(asdu.New(asdu.CauseSpontaneous, ca, asdu.SinglePoint{IOA: 1})); !errors.Is(err, iec101.ErrClosed) {
		t.Errorf("send after close: %v", err)
	}
}
