// SPDX-License-Identifier: MIT

package server

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/otfabric/go-iec60870-5/cp"
	"github.com/otfabric/go-iec60870-5/iec103"
	"github.com/otfabric/go-iec60870-5/internal/testutil/leakcheck"
	"github.com/otfabric/go-iec60870-5/internal/testutil/serialline"
)

func TestMain(m *testing.M) { leakcheck.Main(m) }

// newServer starts a server on one end of an in-memory line; the other end
// is closed when the test ends.
func newServer(t *testing.T, cfg Config) *Server {
	t.Helper()
	a, b := serialline.Pair()
	t.Cleanup(func() { _ = b.Close() })
	if cfg.LinkAddr == 0 {
		cfg.LinkAddr = 3
	}
	if cfg.Fun == 0 {
		cfg.Fun = 128
	}
	s, err := New(a, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// private returns a with a cause of transmission of the private range, as
// the station decodes it: a Raw body whatever the type identification.
func private(t *testing.T, a *iec103.ASDU) *iec103.ASDU {
	t.Helper()
	b, err := a.Encode()
	if err != nil {
		t.Fatal(err)
	}
	b[2] = 200
	d, err := iec103.Decode(b)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := d.Body.(iec103.Raw); !ok {
		t.Fatalf("private cause decodes as %T", d.Body)
	}
	return d
}

// AnswerGI and AckCommand take only a request whose body is the one they
// answer; any other returns ErrInvalid and queues nothing.
func TestAnswersNeedTheirRequestBody(t *testing.T) {
	s := newServer(t, Config{})
	gi := iec103.New(iec103.CauseGI, 3, iec103.FunGlobal, iec103.InfGIEnd, iec103.GIInit{SCN: 1})
	cmd := iec103.New(iec103.CauseGeneralCommand, 3, 128, 16, iec103.GeneralCommand{DCO: iec103.DPIOn, RII: 2})
	for _, c := range []struct {
		name string
		call func() error
	}{
		{"AnswerGI with a private cause", func() error { return s.AnswerGI(private(t, gi)) }},
		{"AnswerGI with a general command", func() error { return s.AnswerGI(cmd) }},
		{"AnswerGI with nil", func() error { return s.AnswerGI(nil) }},
		{"AckCommand with a private cause", func() error { return s.AckCommand(private(t, cmd), true) }},
		{"AckCommand with a general interrogation", func() error { return s.AckCommand(gi, false) }},
		{"AckCommand with nil", func() error { return s.AckCommand(nil, true) }},
	} {
		if err := c.call(); !errors.Is(err, iec103.ErrInvalid) {
			t.Errorf("%s: %v, want ErrInvalid", c.name, err)
		}
		if n := s.Pending(Class1); n != 0 {
			t.Fatalf("%s: %d ASDUs queued", c.name, n)
		}
	}
	if err := s.AnswerGI(gi); err != nil || s.Pending(Class1) != 1 {
		t.Fatalf("AnswerGI: %v, %d queued", err, s.Pending(Class1))
	}
	if err := s.AckCommand(cmd, true); err != nil || s.Pending(Class1) != 2 {
		t.Fatalf("AckCommand: %v, %d queued", err, s.Pending(Class1))
	}
}

// popASDU takes the next class 1 ASDU, as the controlling system polls it.
func popASDU(t *testing.T, s *Server) *iec103.ASDU {
	t.Helper()
	raw := secondaryUser{s}.Class1()
	if raw == nil {
		t.Fatal("nothing queued")
	}
	a, err := iec103.Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// syncASDU decodes an addressed time synchronization carrying the CP56Time2a
// octets cp56, as the link delivers it.
func syncASDU(t *testing.T, cp56 []byte) *iec103.ASDU {
	t.Helper()
	b, err := iec103.New(iec103.CauseTimeSync, 3, iec103.FunGlobal, iec103.InfTimeSync, iec103.TimeSync{Time: cp.At(time.Now())}).Encode()
	if err != nil {
		t.Fatal(err)
	}
	copy(b[len(b)-cp.Size56:], cp56)
	a, err := iec103.Decode(b)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// A time synchronization is applied only when its time is valid and within
// the CP56Time2a range; any other leaves the clock as it was and is
// answered with ASDU 6 carrying the station's time with IV set.
func TestTimeSyncValidity(t *testing.T) {
	at := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	valid := cp.AppendCP56(nil, cp.At(at), nil)
	flagged := cp.AppendCP56(nil, cp.Timestamp{Time: at, Invalid: true}, nil)
	outOfRange := append([]byte(nil), valid...)
	outOfRange[2] = 60 // minute 60: decodes to the zero time with IV set
	for _, c := range []struct {
		name    string
		cp56    []byte
		applied bool
	}{
		{"valid", valid, true},
		{"flagged invalid", flagged, false},
		{"field out of range", outOfRange, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			var told atomic.Int32
			s := newServer(t, Config{SyncDelay: -1, OnTimeSync: func(time.Time, bool) { told.Add(1) }})
			before := s.Now()
			s.dispatch(inbound{a: syncASDU(t, c.cp56), at: time.Now()})
			after := s.Now()
			answer := popASDU(t, s)
			ts, ok := answer.Body.(iec103.TimeSync)
			if !ok || answer.Cause != iec103.CauseTimeSync {
				t.Fatalf("answer %v", answer)
			}
			if c.applied {
				if after.Invalid || after.Sub(at).Abs() > time.Minute || told.Load() != 1 || ts.Time.Invalid {
					t.Fatalf("Now %v, answer %v, OnTimeSync %d calls", after, ts.Time, told.Load())
				}
				return
			}
			if !after.Invalid || after.Sub(before.Time).Abs() > time.Minute || told.Load() != 0 {
				t.Fatalf("Now %v (before %v), OnTimeSync %d calls: the clock changed", after, before, told.Load())
			}
			if !ts.Time.Invalid {
				t.Fatalf("answer %v without IV", ts.Time)
			}
		})
	}
}

// A refused synchronization keeps the clock of an earlier valid one, and a
// broadcast one is not answered.
func TestTimeSyncRefusedKeepsTheClock(t *testing.T) {
	s := newServer(t, Config{SyncDelay: -1})
	at := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	s.dispatch(inbound{a: syncASDU(t, cp.AppendCP56(nil, cp.At(at), nil)), at: time.Now()})
	_ = popASDU(t, s)
	synced := s.Now()
	if synced.Invalid {
		t.Fatalf("Now %v after a valid synchronization", synced)
	}
	s.dispatch(inbound{a: syncASDU(t, cp.AppendCP56(nil, cp.Timestamp{Time: at.AddDate(5, 0, 0), Invalid: true}, nil)), broadcast: true, at: time.Now()})
	if n := s.Pending(Class1); n != 0 {
		t.Fatalf("refused broadcast answered: %d queued", n)
	}
	if now := s.Now(); now.Invalid || now.Sub(synced.Time).Abs() > time.Minute {
		t.Fatalf("Now %v after a refused synchronization, %v before", now, synced)
	}
}

// The offset a synchronization sets stays within the CP56Time2a range,
// whatever the local clock said when it arrived.
func TestTimeSyncOffsetClamped(t *testing.T) {
	for _, c := range []struct {
		name string
		at   time.Time
	}{
		{"local clock far behind", time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC)},
		{"local clock far ahead", time.Date(2500, 1, 1, 0, 0, 0, 0, time.UTC)},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := newServer(t, Config{SyncDelay: -1})
			s.dispatch(inbound{a: syncASDU(t, cp.AppendCP56(nil, cp.At(time.Date(2069, 12, 31, 0, 0, 0, 0, time.UTC)), nil)), at: c.at})
			s.mu.Lock()
			off := s.offset
			s.mu.Unlock()
			// The 100 years (36525 days) of CP56Time2a, 1970 to 2069.
			const span = 36525 * 24 * time.Hour
			if off < -span || off > span {
				t.Fatalf("offset %v beyond ±%v", off, span)
			}
		})
	}
}

// --- lifecycle: L1, L2, L10 ---------------------------------------------

// countingEnd counts the Close calls on the server's end of the line.
type countingEnd struct {
	*serialline.End
	closes atomic.Int32
}

func (c *countingEnd) Close() error { c.closes.Add(1); return c.End.Close() }

func countedServer(t *testing.T, cfg Config) (*Server, *countingEnd) {
	t.Helper()
	a, b := serialline.Pair()
	t.Cleanup(func() { _ = b.Close() })
	end := &countingEnd{End: a}
	cfg.LinkAddr, cfg.Fun = 3, 128
	s, err := New(end, cfg)
	if err != nil {
		t.Fatal(err)
	}
	return s, end
}

// L1: a second Close returns nil and closes nothing again.
func TestCloseTwice(t *testing.T) {
	s, end := countedServer(t, Config{})
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if n := end.closes.Load(); n != 1 {
		t.Fatalf("transport closed %d times, want 1", n)
	}
	if err := s.Send(iec103.New(iec103.CauseSpontaneous, 3, 128, 16, iec103.TimeTagged{}), Class1); !errors.Is(err, iec103.ErrClosed) {
		t.Fatalf("Send after Close: %v, want ErrClosed", err)
	}
}

// L2: concurrent Close calls all return once the station stopped, and the
// transport is closed once.
func TestConcurrentClose(t *testing.T) {
	check := leakcheck.Check(t)
	s, end := countedServer(t, Config{})
	const n = 8
	start := make(chan struct{})
	errs := make(chan error, n)
	var wg sync.WaitGroup
	for range n {
		wg.Go(func() { <-start; errs <- s.Close() })
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := end.closes.Load(); got != 1 {
		t.Fatalf("transport closed %d times, want 1", got)
	}
	if st := s.State(); st != iec103.StateClosed {
		t.Fatalf("state %v", st)
	}
	check()
}

// L10: a handler stops the station by calling Close in a new goroutine; the
// Close returns once the handler has returned.
func TestCloseFromHandler(t *testing.T) {
	check := leakcheck.Check(t)
	closed := make(chan error, 1)
	returned := make(chan struct{})
	h := HandlerFunc(func(s *Server, _ *iec103.ASDU) {
		go func() { closed <- s.Close() }()
		close(returned)
	})
	s, _ := countedServer(t, Config{Handler: h})
	s.in <- inbound{a: iec103.New(iec103.CauseGI, 3, iec103.FunGlobal, iec103.InfGIEnd, iec103.GIInit{SCN: 1}), at: time.Now()}
	<-returned
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Close from the handler did not return")
	}
	if st := s.State(); st != iec103.StateClosed {
		t.Fatalf("state %v", st)
	}
	check()
}
