// SPDX-License-Identifier: MIT

package server

import (
	"testing"
	"testing/synctest"

	"github.com/otfabric/go-iec60870-5/iec103"
	"github.com/otfabric/go-iec60870-5/internal/testutil/serialline"
)

// longChannel is a record of one channel (ACC 1) of n ASDUs 30.
func longChannel(n int) *iec103.Disturbance {
	vals := make([]int16, n*iec103.MaxValues)
	for i := range vals {
		vals[i] = int16(i)
	}
	return &iec103.Disturbance{FAN: 7, Channels: []iec103.Channel{{ACC: 1, Values: vals}}}
}

// newPacedDistServer is newDistServer with Config.PaceChannels. Nobody
// polls its link: the test takes the class 1 queue itself, as the polls of
// a controlling system do.
func newPacedDistServer(t *testing.T, rec *iec103.Disturbance) *Server {
	t.Helper()
	a, b := serialline.Pair()
	s, err := New(a, Config{LinkAddr: 3, Fun: 128, QueueSize: 4, SyncDelay: -1, Logger: &warnRecorder{},
		Disturbances: oneRecord{rec}, PaceChannels: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close(); _ = b.Close() })
	return s
}

// class1 takes what the class 1 queue holds through the link user, as
// polls do, and returns the decoded bodies.
func class1(t *testing.T, s *Server) []iec103.Body {
	t.Helper()
	var out []iec103.Body
	for {
		raw := secondaryUser{s}.Class1()
		if raw == nil {
			return out
		}
		a, err := iec103.Decode(raw)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, a.Body)
	}
}

func (s *Server) class1Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.class[0])
}

// selected selects the record of s and takes the announcement.
func selected(t *testing.T, s *Server) iec103.DisturbanceReady {
	t.Helper()
	s.order(iec103.DisturbanceOrder{TOO: iec103.TOOSelectFault, FAN: 7})
	b := class1(t, s)
	r, ok := b[0].(iec103.DisturbanceReady)
	if len(b) != 1 || !ok {
		t.Fatalf("selection answered with %+v, want the record announced", b)
	}
	return r
}

// With PaceChannels a record whose channel is longer than the class 1
// queue (10 ASDUs 30 and its end, a queue of 4) is announced, and the
// channel goes out whole and in order as polls take the queue: the server
// queues while there is room and waits for a poll while the queue is full.
// It runs on the fake clock of testing/synctest, whose Wait lets the
// server get as far as it can before the test polls.
func TestPacedChannel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const asdus = 10
		s := newPacedDistServer(t, longChannel(asdus))
		if r := selected(t, s); r.NOE != asdus*iec103.MaxValues {
			t.Fatalf("announced NOE %d", r.NOE)
		}
		done := make(chan struct{})
		go func() {
			defer close(done)
			s.order(iec103.DisturbanceOrder{TOO: iec103.TOORequestChannel, FAN: 7, ACC: 1})
		}()
		var got []iec103.Body
		for finished := false; !finished; {
			synctest.Wait()
			select {
			case <-done:
				finished = true
			default:
				if n := s.class1Len(); n != 4 {
					t.Fatalf("the channel waits with %d ASDUs queued, want the queue full (4)", n)
				}
			}
			got = append(got, class1(t, s)...)
		}
		if len(got) != asdus+1 {
			t.Fatalf("polled %d ASDUs, want %d ASDUs 30 and the end of the channel: %+v", len(got), asdus, got)
		}
		for i, b := range got[:asdus] {
			v, ok := b.(iec103.DisturbanceValues)
			if !ok || int(v.NFE) != i*iec103.MaxValues || v.Values[0] != int16(i*iec103.MaxValues) {
				t.Fatalf("ASDU %d: %+v, want the values from %d", i, b, i*iec103.MaxValues)
			}
		}
		if e, ok := got[asdus].(iec103.EndOfTransmission); !ok || e.TOO != iec103.TOOEndChannel {
			t.Fatalf("last ASDU %+v, want the end of the channel (TOO 35)", got[asdus])
		}
	})
}

// A paced channel that waits for room ends at a reset, which ends the
// transfer: the rest of the channel is dropped and nothing of it is queued
// after the reset, nor an end of the channel. Close ends the wait too.
func TestPacedChannelEnds(t *testing.T) {
	for name, stop := range map[string]func(*Server){
		"reset of the communication unit": func(s *Server) { secondaryUser{s}.Reset(false) },
		"reset of the FCB":                func(s *Server) { secondaryUser{s}.ResetFCB() },
		"Close":                           func(s *Server) { _ = s.Close() },
	} {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				s := newPacedDistServer(t, longChannel(10))
				selected(t, s)
				done := make(chan struct{})
				go func() {
					defer close(done)
					s.order(iec103.DisturbanceOrder{TOO: iec103.TOORequestChannel, FAN: 7, ACC: 1})
				}()
				synctest.Wait()
				before := s.class1Len()
				stop(s)
				<-done
				values := 0
				for _, b := range class1(t, s) {
					switch b := b.(type) {
					case iec103.DisturbanceValues:
						values++
					case iec103.EndOfTransmission:
						t.Errorf("queued %+v after the wait ended", b)
					}
				}
				if values > before {
					t.Errorf("%d ASDUs 30 queued, %d before the wait ended", values, before)
				}
			})
		})
	}
}
