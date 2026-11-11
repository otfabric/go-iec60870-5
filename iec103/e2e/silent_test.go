// SPDX-License-Identifier: MIT

package e2e

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/otfabric/go-iec60870-5/iec103"
	"github.com/otfabric/go-iec60870-5/iec103/client"
	"github.com/otfabric/go-iec60870-5/iec103/server"
)

// silent answers nothing but time synchronization (which the server
// handles itself), so requests stay pending.
var silent = server.HandlerFunc(func(*server.Server, *iec103.ASDU) {})

// TestBusy checks that each request kind runs one at a time, and that a
// request without answer ends at its deadline.
func TestBusy(t *testing.T) {
	var states atomic.Int32
	r := start(t, options{custom: silent, noDist: true, timeout: 300 * time.Millisecond,
		onState: func(iec103.State, error) { states.Add(1) }})
	if states.Load() == 0 {
		t.Fatal("OnState not called")
	}
	kinds := map[string]func() error{
		"GI": func() error { _, err := r.c.Interrogate(context.Background()); return err },
		"read": func() error {
			_, err := r.c.ReadEntry(context.Background(), iec103.GIN{}, iec103.KODActualValue)
			return err
		},
		"write": func() error {
			_, err := r.c.GenericWrite(context.Background(), iec103.InfWriteConfirm)
			return err
		},
		"GGI":         func() error { _, err := r.c.GenericInterrogation(context.Background()); return err },
		"disturbance": func() error { _, err := r.c.Disturbance(context.Background(), fun, 1); return err },
	}
	for name, f := range kinds {
		done := make(chan error, 1)
		go func() { done <- f() }()
		busy := false
	loop:
		for {
			select {
			case err := <-done:
				switch {
				case errors.Is(err, iec103.ErrBusy):
					busy = true
				case !errors.Is(err, context.DeadlineExceeded):
					t.Fatalf("%s: %v", name, err)
				}
				break loop
			default:
			}
			if err := f(); errors.Is(err, iec103.ErrBusy) {
				busy = true
			}
			time.Sleep(5 * time.Millisecond)
		}
		if !busy {
			t.Errorf("%s: never busy", name)
		}
	}
	if _, err := r.c.DisturbanceList(context.Background(), fun); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
}

// TestInitRequestsFail lets the general interrogation after the
// initialization time out; the link stays up.
func TestInitRequestsFail(t *testing.T) {
	r := start(t, options{custom: silent, noDist: true, timeout: 100 * time.Millisecond})
	r.expect(t, "time synchronization answer", is(iec103.TypeTimeSync, iec103.CauseTimeSync))
	time.Sleep(200 * time.Millisecond)
	if r.c.State() != iec103.StateUp {
		t.Fatal(r.c.State())
	}
}

// TestStaleGIAnswers sends answers of another scan number and an
// undecodable ASDU in between: the client leaves them out of the result.
func TestStaleGIAnswers(t *testing.T) {
	h := server.HandlerFunc(func(s *server.Server, a *iec103.ASDU) {
		gi, ok := a.Body.(iec103.GIInit)
		if !ok {
			return
		}
		other := gi.SCN + 100
		_ = s.Send(&iec103.ASDU{Type: 1, VSQ: 0x81, Cause: 1, CommonAddr: 3, Fun: fun, Body: iec103.Raw{Data: []byte{1}}}, server.Class1)
		_ = s.Send(iec103.New(iec103.CauseGI, 3, fun, 16, iec103.TimeTagged{DPI: iec103.DPIOn, SIN: other}), server.Class1)
		_ = s.Send(iec103.New(iec103.CauseGI, 3, fun, 64, iec103.RelTimeTagged{DPI: iec103.DPIOn, SIN: other}), server.Class1)
		_ = s.Send(iec103.New(iec103.CauseGITermination, 3, iec103.FunGlobal, 0, iec103.GITermination{SCN: other}), server.Class1)
		_ = s.AnswerGI(a, iec103.New(0, 3, fun, 18, iec103.TimeTagged{DPI: iec103.DPIOn}))
	})
	r := start(t, options{custom: h, noInit: true})
	data, err := r.c.Interrogate(ctx(t))
	if err != nil || len(data) != 1 || data[0].Inf != 18 {
		t.Fatal(data, err)
	}
}

// TestRequestsWhileDown checks every request kind while the link is down.
func TestRequestsWhileDown(t *testing.T) {
	r := start(t, options{noInit: true, handler: true})
	r.line.mute(true)
	deadline := time.Now().Add(5 * time.Second)
	for r.c.State() != iec103.StateDown {
		if time.Now().After(deadline) {
			t.Fatal("still up")
		}
		time.Sleep(10 * time.Millisecond)
	}
	c := ctx(t)
	for name, err := range map[string]error{
		"send":    r.c.Send(c, iec103.New(iec103.CauseGI, 3, iec103.FunGlobal, 0, iec103.GIInit{})),
		"command": r.c.Command(c, fun, 19, iec103.DPIOn),
		"clock":   func() error { _, err := r.c.ClockSync(c, time.Now()); return err }(),
		"read":    func() error { _, err := r.c.ReadHeadings(c); return err }(),
		"write":   func() error { _, err := r.c.GenericWrite(c, iec103.InfWriteExecute); return err }(),
		"write0":  func() error { _, err := r.c.GenericWrite(c, iec103.InfWriteEntry); return err }(),
		"GGI":     func() error { _, err := r.c.GenericInterrogation(c); return err }(),
		"list":    func() error { _, err := r.c.DisturbanceList(c, fun); return err }(),
		"dist":    func() error { _, err := r.c.Disturbance(c, fun, 7); return err }(),
	} {
		if !errors.Is(err, iec103.ErrLinkDown) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// TestWaitUpCancelled waits for a link that never comes up.
func TestWaitUpCancelled(t *testing.T) {
	a, b := net.Pipe()
	defer func() { _ = b.Close() }()
	go func() {
		buf := make([]byte, 256)
		for {
			if _, err := b.Read(buf); err != nil {
				return
			}
		}
	}()
	c, err := client.New(a, client.Config{LinkAddr: 1, RetryInterval: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	cx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := c.WaitUp(cx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if c.Identification() != nil {
		t.Fatal("identification without link")
	}
	_ = c.Close()
}
