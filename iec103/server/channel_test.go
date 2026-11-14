// SPDX-License-Identifier: MIT

package server

import (
	"testing"

	"github.com/otfabric/go-iec60870-5/iec103"
	"github.com/otfabric/go-iec60870-5/internal/testutil/serialline"
)

// oneRecord is a DisturbanceSource with a single record.
type oneRecord struct{ rec *iec103.Disturbance }

func (o oneRecord) List() []iec103.RecordedFault { return nil }
func (o oneRecord) Record(fan uint16) (*iec103.Disturbance, bool) {
	return o.rec, o.rec != nil && fan == o.rec.FAN
}
func (oneRecord) Transmitted(uint16, bool) {}

// queuedBodies takes the class 1 queue and returns its decoded bodies.
func queuedBodies(t *testing.T, s *Server) []iec103.Body {
	t.Helper()
	s.mu.Lock()
	q := s.class[0]
	s.class[0] = nil
	s.mu.Unlock()
	var out []iec103.Body
	for _, x := range q {
		a, err := iec103.Decode(x.raw)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, a.Body)
	}
	return out
}

func newDistServer(t *testing.T, rec *iec103.Disturbance, log *warnRecorder) *Server {
	t.Helper()
	a, b := serialline.Pair()
	t.Cleanup(func() { _ = b.Close() })
	s, err := New(a, Config{LinkAddr: 3, Fun: 128, QueueSize: 4, SyncDelay: -1, Logger: log, Disturbances: oneRecord{rec}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func (s *Server) order(o iec103.DisturbanceOrder) {
	s.disturbance(iec103.New(iec103.CauseDisturbanceData, s.ca, 128, 0, o), o)
}

// TestDisturbanceChannelQueuedWhole: a channel's ASDUs 30 and its end are
// queued all or none. Without room for all of them the channel ends
// aborted by the equipment (TOO 37) instead of losing values.
func TestDisturbanceChannelQueuedWhole(t *testing.T) {
	log := &warnRecorder{}
	rec := &iec103.Disturbance{FAN: 7, Channels: []iec103.Channel{{ACC: 1, Values: make([]int16, 2*iec103.MaxValues)}}}
	s := newDistServer(t, rec, log)
	s.order(iec103.DisturbanceOrder{TOO: iec103.TOOSelectFault, FAN: 7})
	if b := queuedBodies(t, s); len(b) != 1 || b[0].(iec103.DisturbanceReady).NOE != 2*iec103.MaxValues {
		t.Fatalf("selection: %+v", b)
	}
	// Two ASDUs already queued: the channel's three do not fit.
	for range 2 {
		if err := s.Send(iec103.New(iec103.CauseDisturbanceData, s.ca, 128, 0, iec103.TagsReady{FAN: 7}), Class1); err != nil {
			t.Fatal(err)
		}
	}
	s.order(iec103.DisturbanceOrder{TOO: iec103.TOORequestChannel, FAN: 7, ACC: 1})
	b := queuedBodies(t, s)
	if len(b) != 3 || b[2].(iec103.EndOfTransmission).TOO != iec103.TOOEndChannelAbortPrt {
		t.Fatalf("channel without room: %+v", b)
	}
	if !log.has("disturbance channel not queued") {
		t.Errorf("no warning in %q", log.warns)
	}
	// With room the whole channel is queued.
	s.order(iec103.DisturbanceOrder{TOO: iec103.TOORequestChannel, FAN: 7, ACC: 1})
	b = queuedBodies(t, s)
	if len(b) != 3 || b[1].(iec103.DisturbanceValues).NFE != iec103.MaxValues || b[2].(iec103.EndOfTransmission).TOO != iec103.TOOEndChannel {
		t.Fatalf("channel: %+v", b)
	}
}

// TestDisturbanceSelectNOE: NOE announces the longest channel, so that no
// channel carries values beyond it (clause 7.2.6.11); a record whose
// channel cannot fit the class 1 queue at once is not announced: the
// selection is answered with TOO 34.
func TestDisturbanceSelectNOE(t *testing.T) {
	log := &warnRecorder{}
	chs := []iec103.Channel{{ACC: 1, Values: make([]int16, 3)}, {ACC: 2, Values: make([]int16, 4)}}
	s := newDistServer(t, &iec103.Disturbance{FAN: 7, Channels: chs}, log)
	s.order(iec103.DisturbanceOrder{TOO: iec103.TOOSelectFault, FAN: 7})
	if b := queuedBodies(t, s); len(b) != 1 || b[0].(iec103.DisturbanceReady).NOE != 4 || b[0].(iec103.DisturbanceReady).NOC != 2 {
		t.Fatalf("selection: %+v", b)
	}

	s = newDistServer(t, &iec103.Disturbance{FAN: 7, Channels: []iec103.Channel{{ACC: 1, Values: make([]int16, 4*iec103.MaxValues)}}}, log)
	s.order(iec103.DisturbanceOrder{TOO: iec103.TOOSelectFault, FAN: 7})
	b := queuedBodies(t, s)
	if len(b) != 1 || b[0].(iec103.EndOfTransmission).TOO != iec103.TOOEndDataAbortProt {
		t.Fatalf("selection: %+v", b)
	}
	if !log.has("disturbance record not announced") {
		t.Errorf("no warning in %q", log.warns)
	}
}
