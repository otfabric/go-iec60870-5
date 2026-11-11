// SPDX-License-Identifier: MIT

package e2e

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/otfabric/go-iec60870-5/iec102"
	"github.com/otfabric/go-iec60870-5/iec102/client"
	"github.com/otfabric/go-iec60870-5/iec102/ree"
	"github.com/otfabric/go-iec60870-5/iec102/server"
	"github.com/otfabric/go-iec60870-5/link"
)

const (
	linkAddr   = 1
	commonAddr = 1
	key        = 7
)

// hour returns the TimeA of 2024-03-0d hh:00.
func hour(d, h int) iec102.TimeA {
	return iec102.TimeAOf(time.Date(2024, 3, d, h, 0, 0, 0, time.UTC), nil)
}

// meter fills a store: an hourly load profile (record 11, M_IT_TK_2, six
// totals) for 48 hours, daily absolute totals (record 21, M_IT_TG_2), a
// record of accounting totals with signatures (record 1, M_IT_TA_2) and
// events (record 52).
func meter() *server.Store {
	st := server.NewStore()
	for i := 0; i < 48; i++ {
		var tot []iec102.IntegratedTotal
		for a := uint8(1); a <= 6; a++ {
			tot = append(tot, iec102.IntegratedTotal{Addr: a, Value: uint32(i*10) + uint32(a), Quality: iec102.QualityOf(uint8(i), 0)})
		}
		st.AddPeriod(11, iec102.IntegratedTotals{T: iec102.M_IT_TK_2, Totals: tot, Time: hour(1+(i+1)/24, (i+1)%24)})
	}
	st.AddPeriod(21, iec102.IntegratedTotals{T: iec102.M_IT_TG_2, Totals: []iec102.IntegratedTotal{{Addr: 1, Value: 1000}, {Addr: 2, Value: 20}}, Time: hour(2, 0)})
	st.AddPeriod(21, iec102.IntegratedTotals{T: iec102.M_IT_TG_2, Totals: []iec102.IntegratedTotal{{Addr: 1, Value: 1100}, {Addr: 2, Value: 25}}, Time: hour(3, 0)})
	// 100 totals: more than one frame holds.
	var many []iec102.IntegratedTotal
	for a := 0; a < 100; a++ {
		many = append(many, iec102.IntegratedTotal{Addr: uint8(a), Value: uint32(a), Signature: uint8(a ^ 0x5A)})
	}
	st.AddPeriod(1, iec102.IntegratedTotals{T: iec102.M_IT_TA_2, Signed: true, Totals: many, Time: hour(1, 0)})
	for i := 0; i < 40; i++ {
		st.AddSinglePoints(52, iec102.SinglePoint{Addr: uint8(i % 3), On: i%2 == 0, Qualifier: 1,
			Time: iec102.TimeBOf(time.Date(2024, 3, 1+i/20, 10, i%20, 5, 0, time.UTC), nil)})
	}
	return st
}

type rig struct {
	c       *client.Client
	s       *server.Server
	session *ree.Session
	clock   chan iec102.TimeB
	other   chan *iec102.ASDU
}

func newRig(t *testing.T, mod func(*client.Config, *server.Config)) *rig {
	t.Helper()
	cl, sv := net.Pipe()
	r := &rig{clock: make(chan iec102.TimeB, 1), other: make(chan *iec102.ASDU, 4)}
	r.session = &ree.Session{Key: key, OnSetTime: func(t iec102.TimeB) { r.clock <- t },
		Next: server.HandlerFunc(func(s *server.Server, a *iec102.ASDU) {
			// A private request answered by an ASDU of another common
			// address before its confirmation: the client hands the
			// first to its Handler.
			_ = s.Send(iec102.New(iec102.CauseSpontaneous, 99, 0, iec102.Raw{Data: []byte{1}}))
			_ = s.Mirror(a, iec102.CauseActivationCon)
		})}
	scfg := server.Config{
		Link: link.Config{Timeout: 200 * time.Millisecond}, LinkAddr: linkAddr, CommonAddr: commonAddr,
		Params:      iec102.Params{Profile: ree.Profile},
		ProductSpec: iec102.ProductSpec{Standard: 0x14, Manufacturer: 0xFB, Product: 12345678},
		Store:       meter(),
		Clock:       func() time.Time { return time.Date(2024, 3, 3, 12, 0, 1, 250e6, time.UTC) },
		Handler:     r.session,
	}
	ccfg := client.Config{
		Link: link.Config{Timeout: 200 * time.Millisecond, Retries: 2}, LinkAddr: linkAddr, CommonAddr: commonAddr,
		Params:       iec102.Params{Profile: ree.Profile},
		PollInterval: 5 * time.Millisecond,
		Handler:      func(a *iec102.ASDU) { r.other <- a },
	}
	if mod != nil {
		mod(&ccfg, &scfg)
	}
	var err error
	if r.s, err = server.New(sv, scfg); err != nil {
		t.Fatal(err)
	}
	if r.c, err = client.New(cl, ccfg); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = r.c.Close()
		_ = r.s.Close()
	})
	return r
}

func ctx(t *testing.T) context.Context {
	c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return c
}

func TestSessionAndIdentification(t *testing.T) {
	var mu sync.Mutex
	var states []iec102.State
	r := newRig(t, func(c *client.Config, _ *server.Config) {
		c.OnState = func(s iec102.State, _ error) { mu.Lock(); states = append(states, s); mu.Unlock() }
	})
	if err := r.c.Init(ctx(t)); err != nil {
		t.Fatal(err)
	}
	if r.c.State() != iec102.StateUp || r.s.State() != iec102.StateUp {
		t.Fatal(r.c.State(), r.s.State())
	}
	err := ree.Login(ctx(t), r.c, key+1)
	var re *iec102.RefusedError
	if !errors.As(err, &re) || !re.Cause().Negative() || r.session.Open() {
		t.Fatalf("wrong key: %v", err)
	}
	if err := ree.Login(ctx(t), r.c, key); err != nil || !r.session.Open() {
		t.Fatal(err)
	}
	spec, err := r.c.ReadProductSpec(ctx(t))
	if err != nil || spec.Manufacturer != 0xFB || spec.Product != 12345678 {
		t.Fatal(spec, err)
	}
	now, err := r.c.ReadTime(ctx(t))
	if err != nil || now.String() != "2024-03-03 12:00:01.250" {
		t.Fatal(now, err)
	}
	set := iec102.TimeBOf(time.Date(2024, 3, 3, 12, 0, 2, 0, time.UTC), nil)
	if err := ree.SetTime(ctx(t), r.c, set); err != nil {
		t.Fatal(err)
	}
	if got := <-r.clock; got != set {
		t.Fatal(got)
	}
	if err := ree.Logout(ctx(t), r.c); err != nil || r.session.Open() {
		t.Fatal(err)
	}
	if r.s.CommonAddr() != commonAddr || r.s.Params().Profile == nil || r.s.Store() == nil || r.c.CommonAddr() != commonAddr {
		t.Fatal("accessors")
	}
	_ = r.c.Close()
	if _, err := r.c.ReadTime(ctx(t)); !errors.Is(err, iec102.ErrClosed) {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(states) != 2 || states[0] != iec102.StateUp || states[1] != iec102.StateClosed {
		t.Fatal(states)
	}
}

func TestReadTotals(t *testing.T) {
	r := newRig(t, nil)
	c := ctx(t)
	for _, tc := range []struct {
		name    string
		record  uint8
		q       iec102.Read
		periods int
		totals  int
		first   uint32
	}{
		{"oldest", 11, iec102.Read{T: iec102.C_CI_NN_2}, 1, 6, 1},
		{"oldest range", 11, iec102.Read{T: iec102.C_CI_NO_2, First: 2, Last: 3}, 1, 2, 2},
		{"period", 11, iec102.Read{T: iec102.C_CI_NP_2, From: hour(1, 5)}, 1, 6, 41},
		{"period range", 11, iec102.Read{T: iec102.C_CI_NQ_2, First: 6, Last: 9, From: hour(2, 0)}, 1, 1, 236},
		{"time range", 11, iec102.Read{T: iec102.C_CI_NU_2, First: 1, Last: 1, From: hour(1, 1), To: hour(1, 12)}, 12, 12, 1},
		{"whole profile", 11, iec102.Read{T: iec102.C_CI_NU_2, First: 1, Last: 8, From: hour(1, 0), To: hour(3, 0)}, 48, 288, 1},
		{"absolute", 21, iec102.Read{T: iec102.C_CI_NT_2, First: 1, Last: 2, From: hour(1, 0), To: hour(9, 0)}, 2, 4, 1000},
		{"signed, split", 1, iec102.Read{T: iec102.C_CI_NA_2}, 3, 100, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := r.c.ReadTotals(c, tc.record, tc.q)
			if err != nil {
				t.Fatal(err)
			}
			n := 0
			for _, p := range got {
				n += len(p.Totals)
				if p.T.Family() != tc.q.T.Family() {
					t.Fatal(p.T)
				}
			}
			if len(got) != tc.periods || n != tc.totals || got[0].Totals[0].Value != tc.first {
				t.Fatalf("%d periods, %d totals, first %+v", len(got), n, got[0].Totals[0])
			}
		})
	}
	sig, _ := r.c.ReadTotals(c, 1, iec102.Read{T: iec102.C_CI_NA_2})
	if !sig[1].Signed || sig[1].Totals[0].Signature != sig[1].Totals[0].Addr^0x5A {
		t.Fatalf("%+v", sig[1].Totals[0])
	}
}

func TestRefusals(t *testing.T) {
	r := newRig(t, nil)
	c := ctx(t)
	for _, tc := range []struct {
		name   string
		record uint8
		q      iec102.Read
		cause  iec102.Cause
	}{
		{"unknown record", 99, iec102.Read{T: iec102.C_CI_NU_2, From: hour(1, 0), To: hour(2, 0)}, iec102.CauseUnknownRecordAddr},
		{"family not held", 11, iec102.Read{T: iec102.C_CI_NI_2}, iec102.CauseUnknownType},
		{"no period", 11, iec102.Read{T: iec102.C_CI_NU_2, From: hour(20, 0), To: hour(21, 0)}, iec102.CauseNoPeriod},
		{"no object", 11, iec102.Read{T: iec102.C_CI_NO_2, First: 7, Last: 8}, iec102.CauseNoObject},
	} {
		_, err := r.c.ReadTotals(c, tc.record, tc.q)
		var re *iec102.RefusedError
		if !errors.As(err, &re) || re.Cause() != tc.cause {
			t.Errorf("%s: %v", tc.name, err)
		}
	}
	if _, err := r.c.ReadTotals(c, 11, iec102.Read{T: iec102.C_RD_NA_2}); !errors.Is(err, iec102.ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := r.c.ReadSinglePoints(c, 77, nil, nil); !errors.Is(err, iec102.ErrNegative) {
		t.Fatal(err)
	}
	// A request for another common address.
	_, err := r.c.Request(c, iec102.New(5, 2, 0, iec102.Read{T: iec102.C_TI_NA_2}), client.UntilReply)
	var re *iec102.RefusedError
	if !errors.As(err, &re) || re.Cause() != iec102.CauseUnknownRecordAddr {
		t.Fatal(err)
	}
}

func TestSinglePointsAndPrivate(t *testing.T) {
	r := newRig(t, nil)
	c := ctx(t)
	all, err := r.c.ReadSinglePoints(c, 52, nil, nil)
	if err != nil || len(all) != 40 {
		t.Fatal(len(all), err)
	}
	from := iec102.TimeAOf(time.Date(2024, 3, 2, 10, 5, 0, 0, time.UTC), nil)
	to := iec102.TimeAOf(time.Date(2024, 3, 2, 10, 9, 0, 0, time.UTC), nil)
	some, err := r.c.ReadSinglePoints(c, 52, &from, &to)
	if err != nil || len(some) != 5 || some[0].Time.Minute != 5 || some[0].Time.Second != 5 {
		t.Fatal(some, err)
	}
	// A private type goes to the server's handler chain.
	priv := iec102.New(iec102.CauseActivation, commonAddr, 0, iec102.Raw{Data: []byte{0xC0}})
	priv.Type = 200
	out, err := r.c.Request(c, priv, client.UntilConfirmation)
	if err != nil || len(out) != 1 {
		t.Fatal(out, err)
	}
	select {
	case a := <-r.other:
		if a.CommonAddr != 99 {
			t.Fatal(a)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no ASDU for the handler")
	}
}

func TestNoHandlerAndLinkSizes(t *testing.T) {
	for _, size := range []int{1, iec102.NoLinkAddr} {
		r := newRig(t, func(c *client.Config, s *server.Config) {
			c.LinkAddrSize, s.LinkAddrSize = size, size
			c.Params.CommonAddrSize, s.Params.CommonAddrSize = 1, 1
			s.Handler = nil
			if size == iec102.NoLinkAddr {
				c.LinkAddr, s.LinkAddr = 0, 0
			}
		})
		c := ctx(t)
		if _, err := r.c.ReadProductSpec(c); err != nil {
			t.Fatal(size, err)
		}
		err := ree.Login(c, r.c, key)
		var re *iec102.RefusedError
		if !errors.As(err, &re) || re.Cause() != iec102.CauseUnknownType {
			t.Fatal(err)
		}
	}
}

func TestLinkLoss(t *testing.T) {
	r := newRig(t, func(c *client.Config, _ *server.Config) { c.RequestTimeout = 3 * time.Second })
	if err := r.c.Init(ctx(t)); err != nil {
		t.Fatal(err)
	}
	_ = r.s.Close()
	<-r.s.Done()
	if _, err := r.c.ReadTime(ctx(t)); !errors.Is(err, iec102.ErrLinkDown) {
		t.Fatal(err)
	}
	if r.c.State() != iec102.StateDown {
		t.Fatal(r.c.State())
	}
	if err := r.c.Init(ctx(t)); !errors.Is(err, iec102.ErrLinkDown) {
		t.Fatal(err)
	}
}

func TestConfigErrors(t *testing.T) {
	a, b := net.Pipe()
	defer func() { _ = a.Close(); _ = b.Close() }()
	for _, cfg := range []client.Config{
		{LinkAddrSize: 3},
		{Params: iec102.Params{CommonAddrSize: 3}},
		{LinkAddrSize: 1, LinkAddr: 300},
		{Link: link.Config{Timeout: -1}},
	} {
		if _, err := client.New(a, cfg); !errors.Is(err, iec102.ErrInvalidConfig) {
			t.Fatal(cfg, err)
		}
	}
	for _, cfg := range []server.Config{
		{LinkAddrSize: 3},
		{Params: iec102.Params{CommonAddrSize: 3}},
		{LinkAddrSize: 1, LinkAddr: 300},
	} {
		if _, err := server.New(b, cfg); !errors.Is(err, iec102.ErrInvalidConfig) {
			t.Fatal(cfg, err)
		}
	}
}
