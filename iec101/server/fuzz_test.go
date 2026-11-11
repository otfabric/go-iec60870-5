// SPDX-License-Identifier: MIT

package server

import (
	"testing"
	"time"

	"github.com/otfabric/go-iec60870-5/asdu"
	"github.com/otfabric/go-iec60870-5/iec101"
	"github.com/otfabric/go-iec60870-5/internal/testutil/serialline"
)

// replier is a handler that answers every request the way a station does:
// it confirms, sends data, terminates, rejects.
type replier struct{}

func (replier) HandleASDU(s *Server, a *asdu.ASDU) {
	switch a.First().(type) {
	case asdu.Interrogation, asdu.CounterInterrogation:
		_ = s.Confirm(a)
		_ = s.Send(asdu.New(asdu.CauseInterrogatedStation, a.CommonAddr, asdu.SinglePoint{IOA: 1}))
		_ = s.Terminate(a)
	case asdu.Read:
		_ = s.Send(asdu.New(asdu.CauseRequest, a.CommonAddr, asdu.MeasuredFloat{IOA: a.First().Address()}))
	case nil:
		_ = s.Reject(a, asdu.CauseUnknownIOA)
	default:
		if a.Type.IsProcessCommand() {
			_ = s.Negative(a)
			return
		}
		_ = s.Reject(a, asdu.CauseUnknownType)
	}
}

// FuzzReceive feeds arbitrary user data (per ASDU a length octet, then
// that many octets) to the receive path of a server, as the link layer
// hands it over, with and without the profile checks of Params.Strict and
// without a handler, and checks that nothing panics and the queues stay
// within QueueSize.
func FuzzReceive(f *testing.F) {
	p := iec101.DefaultParams().ASDU
	var seed []byte
	for _, a := range []*asdu.ASDU{
		asdu.New(asdu.CauseActivation, 5, asdu.Interrogation{Qualifier: asdu.QOIStation}),
		asdu.New(asdu.CauseActivation, 5, asdu.SingleCommand{IOA: 1, Select: true}),
		asdu.New(asdu.CauseRequest, 5, asdu.Read{IOA: 9}),
		asdu.New(asdu.CauseActivation, 5, asdu.ClockSync{Time: asdu.Now()}),
		asdu.New(asdu.CauseActivation, 0xFF, asdu.ResetProcess{Qualifier: 1}),
		asdu.New(asdu.CauseSpontaneous, 5, asdu.SinglePoint{IOA: 1}),
		asdu.New(asdu.CauseActivation, 0, asdu.CounterInterrogation{}),
	} {
		b, err := a.Encode(p)
		if err != nil {
			f.Fatal(err)
		}
		seed = append(seed, byte(len(b)))
		seed = append(seed, b...)
	}
	f.Add(seed, uint8(0))
	f.Add(seed, uint8(1))
	f.Add([]byte{6, 0xFF, 1, 6, 5, 1, 0, 3, 0, 0, 0}, uint8(2))
	f.Fuzz(func(t *testing.T, data []byte, opts uint8) {
		a, b := serialline.Pair()
		defer func() { _ = b.Close() }()
		params := iec101.DefaultParams()
		params.Strict = opts&1 != 0
		var h Handler = replier{}
		if opts&2 != 0 {
			h = nil
		}
		s, err := New(a, Config{Params: params, LinkAddr: 3, CommonAddr: 5, Handler: h, QueueSize: 16})
		if err != nil {
			t.Fatal(err)
		}
		u := secondaryUser{s}
		for len(data) > 0 {
			n := min(int(data[0]), len(data)-1)
			u.UserData(data[1:1+n], false)
			data = data[1+n:]
			if s.Pending(Class1) > 16 || s.Pending(Class2) > 16 {
				t.Fatalf("queues %d %d beyond 16", s.Pending(Class1), s.Pending(Class2))
			}
		}
		for deadline := time.Now().Add(time.Second); len(s.in) > 0 && time.Now().Before(deadline); {
			time.Sleep(time.Millisecond)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
	})
}
