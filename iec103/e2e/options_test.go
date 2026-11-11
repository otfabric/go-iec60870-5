// SPDX-License-Identifier: MIT

package e2e

import (
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/otfabric/go-iec60870-5/iec103"
	"github.com/otfabric/go-iec60870-5/iec103/server"
)

// TestSyncDelay: the server corrects the received time by the transmission
// time of the ASDU 6, its length in bits times the bit time (clause
// 7.4.2): 21 characters of 11 bits at the configured bit rate.
func TestSyncDelay(t *testing.T) {
	for _, c := range []struct {
		rate  int
		delay time.Duration
		want  time.Duration
	}{
		{0, 0, 231 * time.Second / 9600},
		{19200, 0, 231 * time.Second / 19200},
		{0, -1, 0},
		{0, 5 * time.Millisecond, 5 * time.Millisecond},
	} {
		got := make(chan time.Time, 4)
		r := start(t, options{noInit: true, handler: true, server: func(sc *server.Config) {
			sc.BitRate, sc.SyncDelay = c.rate, c.delay
			sc.OnTimeSync = func(t time.Time, _ bool) { got <- t }
		}})
		want := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
		if _, err := r.c.ClockSync(ctx(t), want); err != nil {
			t.Fatal(err)
		}
		// The client sends want advanced by the time the request waited for
		// the link (it stamps at transmission), so the server's time is the
		// correction plus that wait: a few milliseconds, more on a loaded
		// runner.
		if d := (<-got).Sub(want); d < c.want || d > c.want+250*time.Millisecond {
			t.Errorf("bit rate %d, SyncDelay %v: correction %v, want %v plus the wait for the link", c.rate, c.delay, d, c.want)
		}
	}
}

// TestBroadcastSyncAnswer: figure 5 has no answer to a broadcast
// synchronization (send/no reply); the server sends the ASDU 6 only with
// Config.AnswerBroadcastSync.
func TestBroadcastSyncAnswer(t *testing.T) {
	for _, answer := range []bool{false, true} {
		var syncs atomic.Int32
		r := start(t, options{noInit: true, handler: true, server: func(sc *server.Config) {
			sc.AnswerBroadcastSync = answer
			sc.OnTimeSync = func(time.Time, bool) { syncs.Add(1) }
		}})
		if err := r.c.BroadcastClockSync(time.Now()); err != nil {
			t.Fatal(err)
		}
		eventually(t, "synchronization", func() bool { return syncs.Load() == 1 })
		// A GI afterwards: everything queued before it has been polled.
		if _, err := r.c.Interrogate(ctx(t)); err != nil {
			t.Fatal(err)
		}
		// The client hands received ASDUs to the handler on a goroutine of
		// its own: an ASDU 6 polled before the GI can still be on its way
		// when Interrogate returns. Wait for it when it is expected.
		got := false
		deadline := time.After(2 * time.Second)
	drain:
		for !got {
			select {
			case a := <-r.got:
				got = a.Type == iec103.TypeTimeSync
			case <-deadline:
				break drain
			default:
				if !answer {
					break drain
				}
				time.Sleep(5 * time.Millisecond)
			}
		}
		if got != answer {
			t.Errorf("AnswerBroadcastSync %v: answer sent %v", answer, got)
		}
	}
}

// TestGenericWriteExecuteSplit: a write with execution over several ASDUs
// sends INF 248 with CONT = 1 for all but the last, which has INF 250 and
// CONT = 0, all with one RII and COUNT toggling (clause 7.4.8.6.3, figure
// 17); the staged API does the same over several calls.
func TestGenericWriteExecuteSplit(t *testing.T) {
	r := start(t, options{noInit: true, handler: true})
	c := ctx(t)
	var many []iec103.DataSet
	for i := range 70 {
		many = append(many, iec103.DataSet{GIN: iec103.GIN{Group: 4, Entry: uint8(i)}, KOD: iec103.KODActualValue, Value: iec103.Float32s(1)})
	}
	writes := func() []writeASDU {
		r.dev.mu.Lock()
		defer r.dev.mu.Unlock()
		w := r.dev.writes
		r.dev.writes = nil
		return w
	}
	check := func(got []writeASDU, n int, last iec103.InfoNumber) {
		t.Helper()
		if len(got) != n {
			t.Fatalf("%d ASDUs: %+v", len(got), got)
		}
		for i, w := range got {
			want := iec103.Counted{Count: i%2 == 1, Cont: i < n-1}
			inf := iec103.InfWriteEntry
			if i == n-1 {
				inf = last
			}
			if w.inf != inf || w.rii != got[0].rii || w.ngd != want {
				t.Fatalf("ASDU %d: %+v, want INF %d %+v RII %d", i, w, inf, want, got[0].rii)
			}
		}
	}
	if res, err := r.c.GenericWrite(c, iec103.InfWriteExecute, many...); err != nil || res.Cause != iec103.CauseGenericWriteAck {
		t.Fatal(res, err)
	}
	check(writes(), 3, iec103.InfWriteExecute)

	w, err := r.c.BeginWrite()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.c.BeginWrite(); !errors.Is(err, iec103.ErrBusy) {
		t.Fatal(err)
	}
	if err := w.Stage(c, many[:2]...); err != nil {
		t.Fatal(err)
	}
	if err := w.Stage(c, many...); err != nil {
		t.Fatal(err)
	}
	if res, err := w.Execute(c, many[2]); err != nil || res.Cause != iec103.CauseGenericWriteAck {
		t.Fatal(res, err)
	}
	check(writes(), 5, iec103.InfWriteExecute)
	if _, err := w.Execute(c); !errors.Is(err, iec103.ErrClosed) {
		t.Fatal(err)
	}

	// Staged, then aborted (INF 251).
	w, err = r.c.BeginWrite()
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Stage(c, many[0]); err != nil {
		t.Fatal(err)
	}
	if res, err := w.Abort(c); err != nil || res.Cause != iec103.CauseGenericWriteAck {
		t.Fatal(res, err)
	}
	check(writes(), 2, iec103.InfWriteAbort)
	if err := w.Stage(c, many[0]); !errors.Is(err, iec103.ErrClosed) {
		t.Fatal(err)
	}
	if _, err := r.c.ReadEntry(c, many[0].GIN, iec103.KODActualValue); err != nil {
		t.Fatal(err)
	}
}

// TestFunRequired: the identification carries the main function type of
// the equipment (table 8, note 2), so the server needs one.
func TestFunRequired(t *testing.T) {
	a, b := net.Pipe()
	defer func() { _ = a.Close(); _ = b.Close() }()
	if _, err := server.New(b, server.Config{LinkAddr: 1}); !errors.Is(err, iec103.ErrInvalidConfig) {
		t.Fatal(err)
	}
	if _, err := server.New(b, server.Config{LinkAddr: 1, Fun: fun, BitRate: -1}); !errors.Is(err, iec103.ErrInvalidConfig) {
		t.Fatal(err)
	}
}
