// SPDX-License-Identifier: MIT

package server

import (
	"errors"
	"net"
	"testing"
	"testing/synctest"
	"time"

	"github.com/otfabric/go-iec60870-5/iec102"
)

// longProfile is a store with one record (11) of n hourly periods of one
// total each: the answer to reading all of them is n+2 ASDUs (the
// confirmation, a period per ASDU, the termination).
func longProfile(n int) *Store {
	st := NewStore()
	for i := range n {
		st.AddPeriod(11, iec102.IntegratedTotals{T: iec102.M_IT_TK_2, Totals: []iec102.IntegratedTotal{{Addr: 1, Value: uint32(i)}},
			Time: iec102.TimeAOf(time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(i+1)*time.Hour), nil)})
	}
	return st
}

// readAll reads every period of record 11 of longProfile.
var readAll = iec102.New(iec102.CauseActivation, 0, 11, iec102.Read{T: iec102.C_CI_NU_2, First: 1, Last: 1,
	From: iec102.TimeAOf(time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC), nil),
	To:   iec102.TimeAOf(time.Date(2024, 4, 1, 0, 0, 0, 0, time.UTC), nil)})

// newPacedServer starts a server whose link nobody polls: the test takes
// the class 2 queue itself, as the polls of a controlling station do.
func newPacedServer(t *testing.T, pace bool, periods int) *Server {
	t.Helper()
	a, b := net.Pipe()
	s, err := New(b, Config{QueueSize: 4, Store: longProfile(periods), PaceAnswers: pace})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close(); _ = a.Close() })
	return s
}

// poll takes what the class 2 queue holds, as polls do.
func poll(t *testing.T, s *Server) []*iec102.ASDU {
	t.Helper()
	var out []*iec102.ASDU
	for {
		raw := (*linkUser)(s).Class2()
		if raw == nil {
			return out
		}
		a, err := iec102.Decode(raw, s.cfg.Params)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, a)
	}
}

func (s *Server) queued() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.queue)
}

// By default an answer longer than the class 2 queue is cut where the
// queue is full: the rest of it, the termination included, is not queued
// (the v0.7.0 bound, kept).
func TestAnswerLongerThanTheQueue(t *testing.T) {
	s := newPacedServer(t, false, 10)
	if err := s.serve(readAll); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("serve: %v, want ErrQueueFull", err)
	}
	got := poll(t, s)
	if len(got) != 4 || got[0].Cause != iec102.CauseActivationCon || got[3].Cause == iec102.CauseActivationTerm {
		t.Fatalf("queued %v, want the confirmation and 3 periods", got)
	}
}

// With PaceAnswers an answer longer than the queue goes out whole, in
// order, as polls take the queue: the server queues while there is room
// and waits for a poll while the queue is full. It runs on the fake clock
// of testing/synctest, whose Wait lets the server get as far as it can
// before the test polls.
func TestPacedAnswer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const periods = 10
		s := newPacedServer(t, true, periods)
		done := make(chan error, 1)
		go func() { done <- s.serve(readAll) }()
		var got []*iec102.ASDU
		var err error
		for finished := false; !finished; {
			synctest.Wait()
			select {
			case err = <-done:
				finished = true
			default:
				if n := s.queued(); n != 4 {
					t.Fatalf("the answer waits with %d ASDUs queued, want the queue full (4)", n)
				}
			}
			got = append(got, poll(t, s)...)
		}
		if err != nil {
			t.Errorf("serve: %v", err)
		}
		if len(got) != periods+2 || got[0].Cause != iec102.CauseActivationCon || got[len(got)-1].Cause != iec102.CauseActivationTerm {
			t.Fatalf("polled %d ASDUs, want the confirmation, %d periods and the termination: %v", len(got), periods, got)
		}
		for i, a := range got[1 : periods+1] {
			if b, ok := a.Body.(iec102.IntegratedTotals); !ok || b.Totals[0].Value != uint32(i) {
				t.Fatalf("ASDU %d: %v, want period %d", i+1, a, i)
			}
		}
	})
}

// A paced answer that waits for room ends at a reset of remote link, which
// empties the queue: the rest of it is dropped, nothing of it is queued
// after the reset. Close ends the wait too.
func TestPacedAnswerEnds(t *testing.T) {
	t.Run("reset", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			s := newPacedServer(t, true, 10)
			done := make(chan error, 1)
			go func() { done <- s.serve(readAll) }()
			synctest.Wait()
			(*linkUser)(s).Reset(false)
			if err := <-done; !errors.Is(err, errAbandoned) {
				t.Errorf("serve: %v, want errAbandoned", err)
			}
			if got := poll(t, s); len(got) != 0 {
				t.Errorf("queued after the reset: %v", got)
			}
		})
	})
	t.Run("Close", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			s := newPacedServer(t, true, 10)
			done := make(chan error, 1)
			go func() { done <- s.serve(readAll) }()
			synctest.Wait()
			if err := s.Close(); err != nil {
				t.Errorf("Close: %v", err)
			}
			if err := <-done; !errors.Is(err, iec102.ErrClosed) {
				t.Errorf("serve: %v, want ErrClosed", err)
			}
		})
	})
}
