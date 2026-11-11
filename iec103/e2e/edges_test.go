// SPDX-License-Identifier: MIT

package e2e

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/otfabric/go-iec60870-5/ft12"
	"github.com/otfabric/go-iec60870-5/iec103"
	"github.com/otfabric/go-iec60870-5/iec103/client"
	"github.com/otfabric/go-iec60870-5/iec103/server"
	"github.com/otfabric/go-iec60870-5/link"
)

func TestInvalidConfig(t *testing.T) {
	a, b := net.Pipe()
	defer func() { _ = a.Close(); _ = b.Close() }()
	if _, err := client.New(a, client.Config{LinkAddr: 255}); !errors.Is(err, iec103.ErrInvalidConfig) {
		t.Fatal(err)
	}
	if _, err := client.New(a, client.Config{Link: link.Config{Timeout: -1}}); !errors.Is(err, iec103.ErrInvalidConfig) {
		t.Fatal(err)
	}
	if _, err := server.New(b, server.Config{LinkAddr: 255}); !errors.Is(err, iec103.ErrInvalidConfig) {
		t.Fatal(err)
	}
	if _, err := server.New(b, server.Config{Fun: fun, StartCause: iec103.CauseCyclic}); !errors.Is(err, iec103.ErrInvalidConfig) {
		t.Fatal(err)
	}
	if _, err := server.New(b, server.Config{Fun: fun, Link: link.Config{Timeout: -1}}); !errors.Is(err, iec103.ErrInvalidConfig) {
		t.Fatal(err)
	}
}

// TestServerLink drives the server with a bare link primary: function 1
// (not in clause 6.2.3) is answered with function 15, the start
// identification honours StartCause, the queue bound holds, and a full
// handler queue sets DFC.
func TestServerLink(t *testing.T) {
	a, b := net.Pipe()
	release := make(chan struct{})
	s, err := server.New(b, server.Config{LinkAddr: 4, CommonAddr: 9, Fun: fun, StartCause: iec103.CauseStartRestart, QueueSize: 3,
		Handler: server.HandlerFunc(func(*server.Server, *iec103.ASDU) { <-release })})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	defer close(release)
	pr, err := link.NewPrimary(a, link.Config{FT12: ft12.Params{AddrSize: 1}, Timeout: 50 * time.Millisecond, Retries: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pr.Close() }()
	c := ctx(t)
	if _, err := pr.ResetUser(c, 4); !errors.Is(err, link.ErrNotImplemented) {
		t.Fatal(err)
	}
	if _, err := pr.Init(c, 4); err != nil {
		t.Fatal(err)
	}
	d, _, err := pr.RequestData(c, 4, link.Class1)
	if err != nil {
		t.Fatal(err)
	}
	if a, err := iec103.Decode(d); err != nil || a.Cause != iec103.CauseResetCU || a.CommonAddr != 9 {
		t.Fatal(a, err)
	}
	d, _, _ = pr.RequestData(c, 4, link.Class1)
	if a, err := iec103.Decode(d); err != nil || a.Cause != iec103.CauseStartRestart || a.Inf != iec103.InfStartRestart {
		t.Fatal(a, err)
	}
	for range 3 {
		_ = s.Event(iec103.CauseSpontaneous, fun, 16, iec103.DPIOn)
	}
	if err := s.Event(iec103.CauseSpontaneous, fun, 16, iec103.DPIOn); !errors.Is(err, server.ErrQueueFull) {
		t.Fatal(err)
	}
	if err := s.Send(iec103.New(1, 9, fun, 1, iec103.Measurands{}), server.Class2); err == nil {
		t.Fatal("invalid ASDU queued")
	}
	// An ASDU the codec refuses is accepted by the link and dropped; one
	// for another common address too.
	if _, err := pr.SendConfirm(c, 4, []byte{1, 0x81, 1}); err != nil {
		t.Fatal(err)
	}
	other, _ := iec103.New(iec103.CauseGI, 77, iec103.FunGlobal, 0, iec103.GIInit{}).Encode()
	if _, err := pr.SendConfirm(c, 4, other); err != nil {
		t.Fatal(err)
	}
	// The blocked handler fills its queue: DFC, then NACK.
	gi, _ := iec103.New(iec103.CauseGI, 9, iec103.FunGlobal, 0, iec103.GIInit{}).Encode()
	sawDFC := false
	for range 80 {
		st, err := pr.SendConfirm(c, 4, gi)
		if st.DFC {
			sawDFC = true
		}
		if errors.Is(err, link.ErrNack) {
			break
		}
	}
	if !sawDFC {
		t.Fatal("DFC never set")
	}
}

// orders sends a disturbance order or acknowledgement and returns the
// ASDU 31 the server answers with.
func (r *rig) order(t *testing.T, o iec103.DisturbanceOrder) iec103.EndOfTransmission {
	t.Helper()
	if err := r.c.Send(ctx(t), iec103.New(iec103.CauseDisturbanceData, 3, fun, 0, o)); err != nil {
		t.Fatal(err)
	}
	return r.expect(t, "end of transmission", is(iec103.TypeEndOfTransmission, iec103.CauseDisturbanceData)).Body.(iec103.EndOfTransmission)
}

// TestDisturbanceOrders sends the orders the client does not use and
// implausible ones to the server's transfer (clause 7.4.7).
func TestDisturbanceOrders(t *testing.T) {
	r := start(t, options{noInit: true, handler: true})
	// No transfer selected.
	if e := r.order(t, iec103.DisturbanceOrder{TOO: iec103.TOORequestData, FAN: 7}); e.TOO != iec103.TOOEndDataAbortProt {
		t.Fatal(e)
	}
	sel := func() {
		if err := r.c.Send(ctx(t), iec103.New(iec103.CauseDisturbanceData, 3, fun, 0, iec103.DisturbanceOrder{TOO: iec103.TOOSelectFault, FAN: 7})); err != nil {
			t.Fatal(err)
		}
		r.expect(t, "ready", is(iec103.TypeDisturbanceReady, iec103.CauseDisturbanceData))
	}
	sel()
	for _, c := range []struct {
		o    iec103.DisturbanceOrder
		want iec103.TOO
	}{
		{iec103.DisturbanceOrder{TOO: iec103.TOOAbortTags, FAN: 7}, iec103.TOOEndTagsAbortCtrl},
		{iec103.DisturbanceOrder{TOO: iec103.TOOAbortChannel, FAN: 7, ACC: 1}, iec103.TOOEndChannelAbortCtl},
		{iec103.DisturbanceOrder{TOO: iec103.TOORequestChannel, FAN: 7, ACC: 9}, iec103.TOOEndChannelAbortPrt},
		{iec103.DisturbanceOrder{TOO: iec103.TOOAbortData, FAN: 7}, iec103.TOOEndDataAbortCtrl},
	} {
		if e := r.order(t, c.o); e.TOO != c.want {
			t.Fatalf("%+v: %+v", c.o, e)
		}
	}
	// Implausible orders end the transfer with TOO 34 (each after a new
	// selection, as the abort ends the transfer).
	for _, o := range []iec103.DisturbanceOrder{{TOO: 5, FAN: 7}, {Ack: true, TOO: 70, FAN: 7}} {
		sel()
		if e := r.order(t, o); e.TOO != iec103.TOOEndDataAbortProt {
			t.Fatalf("%+v: %+v", o, e)
		}
	}
	// A record without channels ends right after the tags.
	r.rec.rec.Channels = nil
	sel()
	if e := r.order(t, iec103.DisturbanceOrder{Ack: true, TOO: iec103.TOOTagsOK, FAN: 7}); e.TOO != iec103.TOOEndData {
		t.Fatal(e)
	}
}

// TestDisturbanceIncomplete makes a channel shorter than NOE: the client
// acknowledges it negatively, and the whole record too.
func TestDisturbanceIncomplete(t *testing.T) {
	r := start(t, options{noInit: true, handler: true})
	r.rec.rec.Channels[1].Values = r.rec.rec.Channels[1].Values[:30]
	rec, err := r.c.Disturbance(ctx(t), fun, 7)
	if !errors.Is(err, iec103.ErrNegative) || rec == nil || len(rec.Channels) != 2 {
		t.Fatal(rec, err)
	}
	// The final acknowledgement reaches the source on the server's
	// goroutine after the link confirmed it.
	if acks := r.rec.waitAcks(t); len(acks) != 1 || acks[0] {
		t.Fatal(acks)
	}
}

// TestClientEdges covers the client's argument and timing errors.
func TestClientEdges(t *testing.T) {
	r := start(t, options{noInit: true, handler: true})
	bad := iec103.New(1, 3, fun, 1, iec103.Measurands{})
	if err := r.c.Send(ctx(t), bad); err == nil {
		t.Fatal("invalid ASDU sent")
	}
	if err := r.c.Broadcast(bad); err == nil {
		t.Fatal("invalid ASDU broadcast")
	}
	short, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := r.c.Command(short, fun, 23, iec103.DPIOn); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	close(r.dev.block)
	done, cancel2 := context.WithCancel(context.Background())
	cancel2()
	if _, err := r.c.Interrogate(done); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	big := []iec103.DataSet{{Value: iec103.ASCII(strings.Repeat("x", 250))}}
	if _, err := r.c.GenericWrite(ctx(t), iec103.InfWriteEntry, big...); !errors.Is(err, iec103.ErrTooLong) {
		t.Fatal(err)
	}
	if _, err := r.c.GenericRead(ctx(t), iec103.InfReadEntry, make([]iec103.Item, 100)...); !errors.Is(err, iec103.ErrTooLong) {
		t.Fatal(err)
	}
	if err := r.s.GenericAnswer(iec103.New(42, 3, 254, 244, iec103.GenericCommand{}), 42, big...); !errors.Is(err, iec103.ErrTooLong) {
		t.Fatal(err)
	}
	if err := r.s.GenericIdentAnswer(iec103.New(42, 3, 254, 243, iec103.GenericCommand{}), 42, iec103.GIN{}, iec103.DescElement{Value: big[0].Value}); !errors.Is(err, iec103.ErrTooLong) {
		t.Fatal(err)
	}
}

func TestPack(t *testing.T) {
	var elems []iec103.DescElement
	for range 70 {
		elems = append(elems, iec103.DescElement{KOD: 1, Value: iec103.Uints(iec103.DataUint, 1, 1)})
	}
	b, err := iec103.PackGenericIdent(1, iec103.GIN{}, elems)
	if err != nil || len(b) != 2 || !b[0].NDE.Cont || b[1].NDE.Cont || !b[1].NDE.Count {
		t.Fatal(b, err)
	}
	for _, x := range b {
		if _, err := iec103.New(42, 1, 254, 243, x).Encode(); err != nil {
			t.Fatal(err)
		}
	}
}
