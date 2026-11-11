// SPDX-License-Identifier: MIT

package server

import (
	"testing"
	"time"

	"github.com/otfabric/go-iec60870-5/cp"
	"github.com/otfabric/go-iec60870-5/iec103"
	"github.com/otfabric/go-iec60870-5/internal/testutil/serialline"
)

// fuzzRecords is a disturbance source with one record.
type fuzzRecords struct{ rec *iec103.Disturbance }

func (r fuzzRecords) List() []iec103.RecordedFault {
	return []iec103.RecordedFault{{FAN: r.rec.FAN, Time: cp.Timestamp{Time: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}}}
}
func (r fuzzRecords) Record(fan uint16) (*iec103.Disturbance, bool) { return r.rec, fan == r.rec.FAN }
func (fuzzRecords) Transmitted(uint16, bool)                        {}

// fuzzHandler answers what the application answers.
type fuzzHandler struct{}

func (fuzzHandler) HandleASDU(s *Server, a *iec103.ASDU) {
	switch a.Body.(type) {
	case iec103.GIInit:
		_ = s.AnswerGI(a, iec103.New(iec103.CauseGI, s.CommonAddr(), 128, 16, iec103.TimeTagged{DPI: iec103.DPIOn}))
	case iec103.GeneralCommand:
		_ = s.AckCommand(a, true)
	case iec103.GenericCommand:
		if a.Inf == iec103.InfGenericGI {
			_ = s.AnswerGenericGI(a)
			return
		}
		_ = s.GenericAnswer(a, iec103.CauseGenericReadInvalid)
	case iec103.GenericData:
		_ = s.GenericAnswer(a, iec103.CauseGenericWriteAck)
	}
}

// FuzzDispatch decodes arbitrary user data (per ASDU a length octet, a
// flag octet whose bit 0 marks a broadcast, then the octets) and hands it
// to the server's dispatcher, as the link layer and the handler goroutine
// do, with a handler and a disturbance source or without. It checks that
// nothing panics and the queues stay within QueueSize.
func FuzzDispatch(f *testing.F) {
	var seed []byte
	for _, a := range []*iec103.ASDU{
		iec103.New(iec103.CauseGI, 3, iec103.FunGlobal, iec103.InfGIEnd, iec103.GIInit{SCN: 1}),
		iec103.New(iec103.CauseGeneralCommand, 3, 128, 16, iec103.GeneralCommand{DCO: iec103.DPIOn, RII: 2}),
		iec103.New(iec103.CauseTimeSync, iec103.CommonAddrGlobal, iec103.FunGlobal, iec103.InfTimeSync, iec103.TimeSync{Time: cp.At(time.Now())}),
		iec103.New(iec103.CauseGI, 3, iec103.FunGeneric, iec103.InfGenericGI, iec103.GenericCommand{RII: 3}),
		iec103.New(iec103.CauseDisturbanceData, 3, 128, 0, iec103.DisturbanceOrder{TOO: iec103.TOORequestList}),
		iec103.New(iec103.CauseDisturbanceData, 3, 128, 0, iec103.DisturbanceOrder{TOO: iec103.TOOSelectFault, FAN: 7}),
		iec103.New(iec103.CauseDisturbanceData, 3, 128, 0, iec103.DisturbanceOrder{TOO: iec103.TOORequestData, FAN: 7}),
		iec103.New(iec103.CauseDisturbanceData, 3, 128, 0, iec103.DisturbanceOrder{TOO: iec103.TOORequestTags, FAN: 7}),
		iec103.New(iec103.CauseDisturbanceData, 3, 128, 0, iec103.DisturbanceOrder{Ack: true, TOO: iec103.TOOTagsOK, FAN: 7}),
		iec103.New(iec103.CauseDisturbanceData, 3, 128, 0, iec103.DisturbanceOrder{TOO: iec103.TOORequestChannel, FAN: 7, ACC: 1}),
		iec103.New(iec103.CauseDisturbanceData, 3, 128, 0, iec103.DisturbanceOrder{Ack: true, TOO: iec103.TOOChannelOK, FAN: 7, ACC: 1}),
		iec103.New(iec103.CauseDisturbanceData, 3, 128, 0, iec103.DisturbanceOrder{Ack: true, TOO: iec103.TOODataOK, FAN: 7}),
	} {
		b, err := a.Encode()
		if err != nil {
			f.Fatal(err)
		}
		seed = append(seed, byte(len(b)), byte(len(seed)%3&1))
		seed = append(seed, b...)
	}
	f.Add(seed, uint8(0))
	f.Add(seed, uint8(1))
	f.Add([]byte{3, 0, 0xFF, 0x81, 0}, uint8(2))
	rec := &iec103.Disturbance{Fun: 128, FAN: 7, NOF: 1, Interval: 1000,
		Tags:     []iec103.TagChange{{Pos: 0, Tag: iec103.Tag{Fun: 128, Inf: 84, DPI: iec103.DPIOff}}},
		Channels: []iec103.Channel{{ACC: 1, RPV: 1, RSV: 1, RFA: 1, Values: []int16{1, 2, 3}}}}
	f.Fuzz(func(t *testing.T, data []byte, opts uint8) {
		cfg := Config{LinkAddr: 3, Fun: 128, QueueSize: 32, SyncDelay: -1}
		if opts&1 == 0 {
			cfg.Handler = fuzzHandler{}
			cfg.Disturbances = fuzzRecords{rec}
		}
		cfg.AnswerBroadcastSync = opts&2 != 0
		a, b := serialline.Pair()
		defer func() { _ = b.Close() }()
		s, err := New(a, cfg)
		if err != nil {
			t.Fatal(err)
		}
		for len(data) >= 2 {
			n := min(int(data[0]), len(data)-2)
			bc := data[1]&1 != 0
			if a, err := iec103.DecodeIn(data[2:2+n], nil); err == nil {
				s.dispatch(inbound{a: a, broadcast: bc, at: time.Now()})
			}
			data = data[2+n:]
			if s.Pending(Class1) > 32 || s.Pending(Class2) > 32 {
				t.Fatalf("queues %d %d beyond 32", s.Pending(Class1), s.Pending(Class2))
			}
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
	})
}
