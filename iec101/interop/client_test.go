//go:build interop

// SPDX-License-Identifier: MIT

package interop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/otfabric/go-iec60870-5/asdu"
	"github.com/otfabric/go-iec60870-5/iec101/client"
	"github.com/otfabric/go-iec60870-5/telecontrol"
)

// This module's client against the reference server of every stack, in
// every link mode the image declares: link start-up, interrogation,
// counter interrogation, read, clock synchronization, test command, and
// process commands direct and select-before-operate with their reports.
func TestClientAgainstReference(t *testing.T) {
	for _, a := range adapters(t) {
		f := loadFixture(t, a)
		for _, mode := range modes {
			if !containsString(a.caps.LinkModes, mode) {
				continue
			}
			t.Run(a.name+"/"+mode, func(t *testing.T) { clientScenario(t, a, f, mode) })
		}
	}
}

func clientScenario(t *testing.T, a adapter, f *fixture, mode string) {
	port := startServer(t, a, mode)
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	reports := make(chan *asdu.ASDU, 32)
	c, err := client.New(conn, client.Config{Params: params(mode), LinkAddr: 1, OwnAddr: 1, PollInterval: 20 * time.Millisecond,
		Handler: func(x *asdu.ASDU) {
			if x.Cause == asdu.CauseReturnRemote || x.Cause == asdu.CauseSpontaneous {
				reports <- x
			}
		}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := c.WaitUp(ctx); err != nil {
		t.Fatalf("link start-up: %v", err)
	}
	ca := asdu.CommonAddr(f.Station.CommonAddress)

	t.Run("interrogation", func(t *testing.T) {
		data, err := c.Interrogate(ctx, ca, asdu.QOIStation)
		if err != nil {
			t.Fatal(err)
		}
		got := map[int]any{}
		for _, x := range data {
			if x.Cause != asdu.CauseInterrogatedStation || x.CommonAddr != ca {
				t.Errorf("answer %v", x)
			}
			for _, o := range x.Objects {
				got[int(o.Address())] = valueOf(o)
			}
		}
		for _, p := range f.Points {
			if p.Type == "M_IT_NA_1" {
				continue
			}
			var want any
			if err := json.Unmarshal(p.Value, &want); err != nil {
				t.Fatal(err)
			}
			if p.Type == "M_ME_NC_1" {
				want = float64(float32(want.(float64)))
			}
			if got[p.IOA] != want {
				t.Errorf("IOA %d (%s): %v, want %v", p.IOA, p.Type, got[p.IOA], want)
			}
		}
	})
	t.Run("counter interrogation", func(t *testing.T) {
		data, err := c.CounterInterrogate(ctx, ca, asdu.CounterGeneral, asdu.FreezeRead)
		if err != nil || len(data) != 1 || data[0].Cause != asdu.CauseCounterGeneral {
			t.Fatalf("%v %v", data, err)
		}
		if it, ok := data[0].First().(asdu.IntegratedTotal); !ok || it.IOA != 300 || it.Value != 123456 || it.Sequence != 1 {
			t.Errorf("%+v", data[0].First())
		}
	})
	t.Run("read", func(t *testing.T) {
		x, err := c.Read(ctx, ca, 202)
		if err != nil || valueOf(x.First()) != float64(float32(230.5)) {
			t.Fatalf("%v %v", x, err)
		}
		var neg *telecontrol.NegativeError
		if _, err := c.Read(ctx, ca, 999); !errors.As(err, &neg) || neg.Cause() != asdu.CauseUnknownIOA {
			t.Fatalf("unknown IOA: %v", err)
		}
		if _, err := c.Interrogate(ctx, 7, asdu.QOIStation); !errors.As(err, &neg) || neg.Cause() != asdu.CauseUnknownCommonAddr {
			t.Fatalf("unknown CA: %v", err)
		}
	})
	t.Run("clock and test", func(t *testing.T) {
		if err := c.ClockSync(ctx, ca, time.Now()); err != nil {
			t.Fatal(err)
		}
		if err := c.TestCommand(ctx, ca); err != nil {
			t.Fatal(err)
		}
	})
	report := func(t *testing.T, typ asdu.TypeID, ioa asdu.IOA) asdu.InformationObject {
		t.Helper()
		select {
		case x := <-reports:
			if x.Type != typ || x.First().Address() != ioa {
				t.Fatalf("report %v, want %s at %d", x, typ, ioa)
			}
			return x.First()
		case <-time.After(10 * time.Second):
			t.Fatalf("no %s report for %d", typ, ioa)
		}
		return nil
	}
	t.Run("commands", func(t *testing.T) {
		var neg *telecontrol.NegativeError
		if err := c.Command(ctx, ca, asdu.SingleCommand{IOA: 510, Value: true}); !errors.As(err, &neg) || neg.Cause() != asdu.CauseActivationCon {
			t.Fatalf("execute without select: %v", err)
		}
		if err := c.Command(ctx, ca, asdu.SingleCommand{IOA: 510, Value: false, Select: true}); err != nil {
			t.Fatal(err)
		}
		if err := c.Command(ctx, ca, asdu.SingleCommand{IOA: 510, Value: false}); err != nil {
			t.Fatal(err)
		}
		if p := report(t, asdu.M_SP_TB_1, 100).(asdu.SinglePoint); p.Value {
			t.Errorf("report %+v", p)
		}
		if err := c.Command(ctx, ca, asdu.SingleCommand{IOA: 510, Value: true, Select: true}); err != nil {
			t.Fatal(err)
		}
		if err := c.Deactivate(ctx, ca, asdu.SingleCommand{IOA: 510, Value: true, Select: true}); err != nil {
			t.Fatal(err)
		}
		if err := c.Command(ctx, ca, asdu.SingleCommand{IOA: 510, Value: true}); !errors.As(err, &neg) {
			t.Fatalf("execute after cancel: %v", err)
		}
		if err := c.Command(ctx, ca, asdu.DoubleCommand{IOA: 501, Value: asdu.DoubleOff, Select: true}); err != nil {
			t.Fatal(err)
		}
		if err := c.Command(ctx, ca, asdu.DoubleCommand{IOA: 501, Value: asdu.DoubleOff}); err != nil {
			t.Fatal(err)
		}
		if p := report(t, asdu.M_DP_TB_1, 101).(asdu.DoublePoint); p.Value != asdu.DoubleOff {
			t.Errorf("report %+v", p)
		}
		if err := c.Command(ctx, ca, asdu.SetpointFloat{IOA: 502, Value: 49.5}); err != nil {
			t.Fatal(err)
		}
		if p := report(t, asdu.M_ME_TF_1, 202).(asdu.MeasuredFloat); p.Value != 49.5 {
			t.Errorf("report %+v", p)
		}
		if err := c.Command(ctx, ca, asdu.StepCommand{IOA: 505, Value: asdu.StepHigher}); err != nil {
			t.Fatal(err)
		}
		if p := report(t, asdu.M_ST_TB_1, 102).(asdu.StepPosition); p.Value != 6 {
			t.Errorf("report %+v", p)
		}
		if err := c.Command(ctx, ca, asdu.SingleCommand{IOA: 999, Value: true}); !errors.As(err, &neg) || neg.Cause() != asdu.CauseUnknownIOA {
			t.Fatalf("unknown command: %v", err)
		}
		x, err := c.Read(ctx, ca, 202)
		if err != nil || valueOf(x.First()) != float64(49.5) {
			t.Fatalf("read after setpoint: %v %v", x, err)
		}
	})
}
