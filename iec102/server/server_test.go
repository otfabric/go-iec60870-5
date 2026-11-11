// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/otfabric/go-iec60870-5/ft12"
	"github.com/otfabric/go-iec60870-5/iec102"
	"github.com/otfabric/go-iec60870-5/link"
)

func TestQueueAndDefaults(t *testing.T) {
	a, b := net.Pipe()
	defer func() { _ = a.Close() }()
	var states []iec102.State
	s, err := New(b, Config{QueueSize: 1, OnState: func(st iec102.State, _ error) { states = append(states, st) }})
	if err != nil {
		t.Fatal(err)
	}
	if s.Now().IsZero() || s.Store() == nil {
		t.Fatal("defaults")
	}
	small := iec102.New(iec102.CauseRequest, 0, 0, iec102.SystemTime{})
	if err := s.Send(small); err != nil {
		t.Fatal(err)
	}
	if err := s.Send(small); !errors.Is(err, ErrQueueFull) {
		t.Fatal(err)
	}
	// Answers that do not fit the queue are logged and dropped.
	// Unknown record: the refusal itself does not fit.
	if err := s.serve(iec102.New(iec102.CauseActivation, 0, 0, iec102.Read{T: iec102.C_SP_NA_2})); !errors.Is(err, ErrQueueFull) {
		t.Fatal(err)
	}
	s.Store().AddSinglePoints(50, iec102.SinglePoint{})
	s.Store().AddPeriod(11, iec102.IntegratedTotals{T: iec102.M_IT_TK_2, Totals: []iec102.IntegratedTotal{{Addr: 1}}})
	for _, r := range []iec102.Read{{T: iec102.C_SP_NA_2}, {T: iec102.C_CI_NN_2}} {
		rec := uint8(50)
		if r.T != iec102.C_SP_NA_2 {
			rec = 11
		}
		if err := s.serve(iec102.New(iec102.CauseActivation, 0, rec, r)); !errors.Is(err, ErrQueueFull) {
			t.Fatal(r.T, err)
		}
	}
	(*linkUser)(s).Class2()
	(*linkUser)(s).Class2()
	if (*linkUser)(s).Class1() != nil || (*linkUser)(s).HasClass1() {
		t.Fatal("class 1")
	}
	(*linkUser)(s).Reset(true)
	(*linkUser)(s).Reset(false)
	// Three totals in a frame too small for one: split one per ASDU.
	s.budget = 6 + iec102.SizeTimeA + 6
	p := iec102.IntegratedTotals{T: iec102.M_IT_TK_2, Totals: make([]iec102.IntegratedTotal, 3)}
	if parts := s.split(p); len(parts) != 3 {
		t.Fatal(len(parts))
	}
	big := iec102.New(iec102.CauseRequest, 0, 0, iec102.SinglePoints{Points: make([]iec102.SinglePoint, 2)})
	if err := s.Send(big); !errors.Is(err, iec102.ErrTooLong) {
		t.Fatal(err)
	}
	if err := s.Send(&iec102.ASDU{}); !errors.Is(err, iec102.ErrInvalid) {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Send(small); !errors.Is(err, iec102.ErrClosed) {
		t.Fatal(err)
	}
	if len(states) != 2 || states[1] != iec102.StateClosed {
		t.Fatal(states)
	}
}

// TestJunkAndBusy sends an undecodable ASDU and more requests than the
// server's input holds, through a plain link primary.
func TestJunkAndBusy(t *testing.T) {
	a, b := net.Pipe()
	block := make(chan struct{})
	s, err := New(b, Config{LinkAddr: 1, Handler: HandlerFunc(func(*Server, *iec102.ASDU) { <-block })})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	pri, err := link.NewPrimary(a, link.Config{FT12: ft12.Params{AddrSize: 2}, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pri.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := pri.Init(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := pri.SendConfirm(ctx, 1, []byte{1}); err != nil {
		t.Fatal(err)
	}
	priv, _ := iec102.New(6, 0, 0, iec102.Raw{Data: []byte{1}}).Encode(iec102.Params{})
	priv[0] = 200
	nacked := false
	for i := 0; i < 40 && !nacked; i++ {
		_, err := pri.SendConfirm(ctx, 1, priv)
		nacked = errors.Is(err, link.ErrNack)
	}
	close(block)
	if !nacked {
		t.Fatal("input never full")
	}
}
