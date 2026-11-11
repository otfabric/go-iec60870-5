// SPDX-License-Identifier: MIT

package server

import (
	"testing"
	"time"

	"github.com/otfabric/go-iec60870-5/iec102"
	"github.com/otfabric/go-iec60870-5/internal/testutil/serialline"
)

// FuzzServe decodes arbitrary user data (per ASDU a length octet, then
// that many octets) and serves it as the server's worker does, against a
// store with totals and single points, with and without a handler and
// with one- and two-octet common addresses. It checks that nothing panics
// and the class 2 queue stays within QueueSize.
func FuzzServe(f *testing.F) {
	at := func(d, h int) iec102.TimeA { return iec102.TimeAOf(time.Date(2024, 3, d, h, 0, 0, 0, time.UTC), nil) }
	var seed []byte
	for _, a := range []*iec102.ASDU{
		iec102.New(iec102.CauseRequest, 1, 0, iec102.Read{T: iec102.C_RD_NA_2}),
		iec102.New(iec102.CauseRequest, 1, 0, iec102.Read{T: iec102.C_TI_NA_2}),
		iec102.New(iec102.CauseActivation, 1, 11, iec102.Read{T: iec102.C_CI_NU_2, First: 1, Last: 6, From: at(1, 0), To: at(3, 0)}),
		iec102.New(iec102.CauseActivation, 1, 11, iec102.Read{T: iec102.C_CI_NQ_2, First: 1, Last: 6, From: at(1, 3)}),
		iec102.New(iec102.CauseActivation, 1, 21, iec102.Read{T: iec102.C_CI_NG_2}),
		iec102.New(iec102.CauseActivation, 1, 52, iec102.Read{T: iec102.C_SP_NB_2, From: at(1, 0), To: at(2, 0)}),
		iec102.New(iec102.CauseActivation, 1, 52, iec102.Read{T: iec102.C_SP_NA_2}),
		iec102.New(iec102.CauseActivation, 2, 0, iec102.Read{T: iec102.C_CI_NA_2}),
		{Type: 200, Cause: iec102.CauseActivation, CommonAddr: 1, Body: iec102.Raw{Data: []byte{1, 2}}},
	} {
		b, err := a.Encode(iec102.Params{})
		if err != nil {
			f.Fatal(err)
		}
		seed = append(seed, byte(len(b)))
		seed = append(seed, b...)
	}
	f.Add(seed, uint8(0))
	f.Add(seed, uint8(1))
	f.Add([]byte{5, 104, 0, 6, 1, 0}, uint8(2))
	f.Fuzz(func(t *testing.T, data []byte, opts uint8) {
		st := NewStore()
		for i := range 30 {
			st.AddPeriod(11, iec102.IntegratedTotals{T: iec102.M_IT_TK_2, Time: at(1+i/24, i%24),
				Totals: []iec102.IntegratedTotal{{Addr: 1, Value: uint32(i)}, {Addr: 6, Value: 9}}})
		}
		st.AddPeriod(21, iec102.IntegratedTotals{T: iec102.M_IT_TG_2, Signed: true, Time: at(2, 0),
			Totals: []iec102.IntegratedTotal{{Addr: 1, Value: 1000, Signature: 7}}})
		for i := range 20 {
			st.AddSinglePoints(52, iec102.SinglePoint{Addr: uint8(i), On: i%2 == 0,
				Time: iec102.TimeBOf(time.Date(2024, 3, 1, 10, i, 0, 0, time.UTC), nil)})
		}
		var h Handler = HandlerFunc(func(s *Server, a *iec102.ASDU) {
			_ = s.Mirror(a, iec102.CauseActivationCon)
			_ = s.Send(iec102.New(iec102.CauseSpontaneous, a.CommonAddr, a.Record, iec102.Raw{Data: []byte{1}}))
		})
		if opts&1 != 0 {
			h = nil
		}
		p := iec102.Params{}
		if opts&2 != 0 {
			p.CommonAddrSize = 1
		}
		a, b := serialline.Pair()
		defer func() { _ = b.Close() }()
		s, err := New(a, Config{LinkAddr: 1, CommonAddr: 1, Params: p, Store: st, Handler: h, QueueSize: 64})
		if err != nil {
			t.Fatal(err)
		}
		for len(data) > 0 {
			n := min(int(data[0]), len(data)-1)
			if a, err := iec102.Decode(data[1:1+n], p); err == nil {
				_ = s.serve(a)
			}
			data = data[1+n:]
			s.mu.Lock()
			q := len(s.queue)
			s.mu.Unlock()
			if q > 64 {
				t.Fatalf("queue %d beyond 64", q)
			}
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
	})
}
