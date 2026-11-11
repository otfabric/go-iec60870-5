// SPDX-License-Identifier: MIT

package telecontrol

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/otfabric/go-iec60870-5/asdu"
)

var p = asdu.Params{CauseSize: 1, CommonAddrSize: 1, IOASize: 2}

// scripted answers every request with the ASDUs answer returns for it.
func scripted(answer func(req *asdu.ASDU) []*asdu.ASDU) *Requester {
	r := &Requester{Params: p, Timeout: time.Second}
	r.Send = func(_ context.Context, req *asdu.ASDU) error {
		go func() {
			for _, a := range answer(req) {
				r.Dispatch(a)
			}
		}()
		return nil
	}
	return r
}

func TestNegativeError(t *testing.T) {
	e := &NegativeError{ASDU: asdu.New(asdu.CauseUnknownIOA, 7, asdu.Read{IOA: 1})}
	if !strings.Contains(e.Error(), "unknown-ioa") || e.Cause() != asdu.CauseUnknownIOA {
		t.Errorf("%v %v", e, e.Cause())
	}
	empty := &NegativeError{}
	if empty.Error() == "" || empty.Cause() != 0 {
		t.Error("empty")
	}
	if Rejected(asdu.New(asdu.CauseActivationCon, 1, asdu.Read{})) != nil {
		t.Error("positive rejected")
	}
	if Confirmation(asdu.New(asdu.CauseDeactivation, 1, asdu.SingleCommand{})) != asdu.CauseDeactivationCon {
		t.Error("deactivation")
	}
}

func TestMatching(t *testing.T) {
	req := asdu.New(asdu.CauseActivation, 1, asdu.SingleCommand{IOA: 5})
	deact := asdu.New(asdu.CauseDeactivation, 1, asdu.SingleCommand{IOA: 5})
	for _, tc := range []struct {
		req, a *asdu.ASDU
		want   bool
	}{
		{req, req.Reply(asdu.CauseActivationCon, false), true},
		{req, req.Reply(asdu.CauseActivationTerm, false), true},
		{req, req.Reply(asdu.CauseUnknownIOA, true), true},
		{req, req.Reply(asdu.CauseDeactivationCon, false), false},
		{deact, deact.Reply(asdu.CauseActivationCon, false), false},
		{deact, deact.Reply(asdu.CauseDeactivationCon, false), true},
		{req, req.Reply(asdu.CauseSpontaneous, false), false},
		{req, asdu.New(asdu.CauseActivationCon, 2, asdu.SingleCommand{IOA: 5}), false},
		{req, asdu.New(asdu.CauseActivationCon, 1, asdu.SingleCommand{IOA: 6}), false},
		{req, asdu.New(asdu.CauseActivationCon, 1, asdu.DoubleCommand{IOA: 5}), false},
		{asdu.New(asdu.CauseActivation, 0xFF, asdu.SingleCommand{IOA: 5}), asdu.New(asdu.CauseActivationCon, 3, asdu.SingleCommand{IOA: 5}), true},
	} {
		if got := Mirrored(p, tc.req, tc.a); got != tc.want {
			t.Errorf("Mirrored(%v, %v) = %v", tc.req, tc.a, got)
		}
	}
	gi := asdu.New(asdu.CauseActivation, 1, asdu.Interrogation{Qualifier: asdu.QOIStation})
	if !FromStation(p, gi, asdu.New(asdu.CauseInterrogatedStation, 1, asdu.ParameterFloat{IOA: 1})) {
		t.Error("interrogated parameter")
	}
	if FromStation(p, gi, asdu.New(asdu.CauseActivation, 1, asdu.SingleCommand{IOA: 1})) {
		t.Error("control direction")
	}
}

func TestRequests(t *testing.T) {
	r := scripted(func(req *asdu.ASDU) []*asdu.ASDU {
		switch o := req.First().(type) {
		case asdu.Interrogation:
			return []*asdu.ASDU{
				req.Reply(asdu.CauseActivationTerm, false), // of an earlier one: skipped
				req.Reply(asdu.CauseActivationCon, false),
				asdu.New(o.Qualifier.Cause(), 1, asdu.SinglePoint{IOA: 1}),
				asdu.New(asdu.CauseSpontaneous, 1, asdu.SinglePoint{IOA: 2}), // not collected
				req.Reply(asdu.CauseActivationTerm, false),
			}
		case asdu.CounterInterrogation:
			if o.Freeze != asdu.FreezeRead { // a freeze command: confirmation only
				return []*asdu.ASDU{req.Reply(asdu.CauseActivationCon, false)}
			}
			return []*asdu.ASDU{req.Reply(asdu.CauseActivationCon, true)}
		case asdu.Read:
			if o.IOA == 9 {
				return []*asdu.ASDU{req.Reply(asdu.CauseUnknownIOA, true)}
			}
			return []*asdu.ASDU{
				req.Reply(asdu.CauseRequest, false), // positive mirror: skipped
				asdu.New(asdu.CauseRequest, 1, asdu.SinglePoint{IOA: 8}),
				asdu.New(asdu.CauseRequest, 1, asdu.SinglePoint{IOA: o.IOA}),
			}
		case asdu.TestCommand:
			if o.Time.IsZero() != (o.Counter == asdu.TestPattern) {
				return []*asdu.ASDU{req.Reply(asdu.CauseActivationCon, true)}
			}
		}
		return []*asdu.ASDU{req.Reply(asdu.CauseActivationTerm, false), req.Reply(Confirmation(req), false)}
	})
	ctx := context.Background()
	data, err := r.Interrogate(ctx, 1, asdu.QOIStation)
	if err != nil || len(data) != 1 {
		t.Fatalf("interrogate %v %v", data, err)
	}
	var neg *NegativeError
	if _, err := r.CounterInterrogate(ctx, 1, asdu.CounterGeneral, asdu.FreezeRead); !errors.As(err, &neg) {
		t.Fatalf("counter: %v", err)
	}
	if data, err := r.CounterInterrogate(ctx, 1, asdu.CounterGeneral, asdu.FreezeWithReset); err != nil || data != nil {
		t.Fatalf("freeze: %v %v", data, err)
	}
	a, err := r.Read(ctx, 1, 4)
	if err != nil || a.First().Address() != 4 {
		t.Fatalf("read %v %v", a, err)
	}
	if _, err := r.Read(ctx, 1, 9); !errors.As(err, &neg) {
		t.Fatalf("read unknown: %v", err)
	}
	for _, f := range []func() error{
		func() error { return r.Command(ctx, 1, asdu.SingleCommand{IOA: 1}, false) },
		func() error { return r.Command(ctx, 1, asdu.SingleCommand{IOA: 1}, true) },
		func() error { return r.ClockSync(ctx, 1, time.Now()) },
		func() error { return r.TestCommand(ctx, 1, false) },
		func() error { return r.TestCommand(ctx, 1, true) },
		func() error { return r.ResetProcess(ctx, 1, asdu.ResetProcessGeneral) },
		func() error { return r.SetDelay(ctx, 1, time.Hour) },
		func() error {
			d, err := r.MeasureDelay(ctx, 1)
			if err == nil && (d < 0 || d > time.Second) {
				err = errors.New("implausible delay")
			}
			return err
		},
	} {
		if err := f(); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.Command(ctx, 1, nil, false); !errors.Is(err, asdu.ErrInvalidValue) {
		t.Error(err)
	}
	if err := r.Command(ctx, 1, asdu.SinglePoint{IOA: 1}, false); !errors.Is(err, asdu.ErrTypeMismatch) {
		t.Error(err)
	}
}

func TestFailures(t *testing.T) {
	silent := &Requester{Params: p, Timeout: 50 * time.Millisecond, Send: func(context.Context, *asdu.ASDU) error { return nil }}
	if err := silent.ClockSync(context.Background(), 1, time.Now()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout: %v", err)
	}
	boom := errors.New("boom")
	broken := &Requester{Params: p, Send: func(context.Context, *asdu.ASDU) error { return boom }}
	if _, err := broken.Interrogate(context.Background(), 1, asdu.QOIStation); !errors.Is(err, boom) {
		t.Fatalf("send error: %v", err)
	}
	if _, err := broken.Read(context.Background(), 1, 1); !errors.Is(err, boom) {
		t.Fatal(err)
	}
	if err := broken.Confirmed(context.Background(), &asdu.ASDU{Type: asdu.C_SC_NA_1}); !errors.Is(err, asdu.ErrInvalidValue) {
		t.Fatalf("no object: %v", err)
	}

	// An identical request is refused while one is pending; FailAll ends it.
	sent := make(chan struct{}, 1)
	hold := &Requester{Params: p, Send: func(context.Context, *asdu.ASDU) error { sent <- struct{}{}; return nil }}
	done := make(chan error, 1)
	go func() { _, err := hold.Interrogate(context.Background(), 1, asdu.QOIStation); done <- err }()
	<-sent
	if _, err := hold.Interrogate(context.Background(), 1, asdu.QOIStation); !errors.Is(err, ErrBusy) {
		t.Fatalf("busy: %v", err)
	}
	lost := errors.New("lost")
	hold.FailAll(lost)
	if err := <-done; !errors.Is(err, lost) {
		t.Fatalf("fail all: %v", err)
	}
	go func() { _, err := hold.Read(context.Background(), 1, 1); done <- err }()
	<-sent
	hold.FailAll(lost)
	if err := <-done; !errors.Is(err, lost) {
		t.Fatalf("read fail: %v", err)
	}
}

// TestTerminationAfterConfirmation: the activation termination of a
// command that arrives right after its confirmation, before the waiting
// Command has unregistered, is not consumed by the finished exchange: it
// reaches the application. The IEC 101 replay of the recorded SEL RTAC
// select-before-operate (iec101/client/capture_test.go) lost it whenever
// the poll fetched it before Command returned.
func TestTerminationAfterConfirmation(t *testing.T) {
	var consumed []bool
	r := &Requester{Params: p, Timeout: time.Second}
	r.Send = func(_ context.Context, req *asdu.ASDU) error {
		// Both answers are dispatched before Send returns, so before the
		// request reads the first.
		consumed = append(consumed, r.Dispatch(req.Reply(asdu.CauseActivationCon, false)))
		consumed = append(consumed, r.Dispatch(req.Reply(asdu.CauseActivationTerm, false)))
		return nil
	}
	if err := r.Command(context.Background(), 1, asdu.DoubleCommand{IOA: 22003, Value: asdu.DoubleOn}, false); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(consumed, []bool{true, false}) {
		t.Errorf("consumed %v, want the confirmation only", consumed)
	}
	// An interrogation ends at its termination, which it consumes; what
	// comes after is the application's only.
	r.Send = func(_ context.Context, req *asdu.ASDU) error {
		consumed = consumed[:0]
		for _, a := range []*asdu.ASDU{
			req.Reply(asdu.CauseActivationTerm, false), // an earlier one's
			req.Reply(asdu.CauseActivationCon, false),
			asdu.New(asdu.CauseInterrogatedStation, 1, asdu.SinglePoint{IOA: 1}),
			req.Reply(asdu.CauseActivationTerm, false),
			req.Reply(asdu.CauseActivationTerm, false), // a duplicate
		} {
			consumed = append(consumed, r.Dispatch(a))
		}
		return nil
	}
	data, err := r.Interrogate(context.Background(), 1, asdu.QOIStation)
	if err != nil || len(data) != 1 {
		t.Fatalf("%v %v", data, err)
	}
	if want := []bool{true, true, false, true, false}; !slices.Equal(consumed, want) {
		t.Errorf("interrogation: consumed %v, want %v", consumed, want)
	}
	// A rejected interrogation ends at the rejection.
	r.Send = func(_ context.Context, req *asdu.ASDU) error {
		consumed = consumed[:0]
		consumed = append(consumed, r.Dispatch(req.Reply(asdu.CauseActivationCon, true)))
		consumed = append(consumed, r.Dispatch(req.Reply(asdu.CauseActivationTerm, false)))
		return nil
	}
	if _, err := r.Interrogate(context.Background(), 1, asdu.QOIStation); err == nil {
		t.Error("negative confirmation accepted")
	}
	if !slices.Equal(consumed, []bool{true, false}) {
		t.Errorf("rejected interrogation: consumed %v", consumed)
	}
	// A read ends at its data.
	r.Send = func(_ context.Context, req *asdu.ASDU) error {
		consumed = consumed[:0]
		consumed = append(consumed, r.Dispatch(asdu.New(asdu.CauseRequest, 1, asdu.SinglePoint{IOA: 9})))
		consumed = append(consumed, r.Dispatch(req.Reply(asdu.CauseUnknownIOA, true)))
		return nil
	}
	if _, err := r.Read(context.Background(), 1, 9); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(consumed, []bool{false, false}) {
		t.Errorf("read: consumed %v", consumed)
	}
}
