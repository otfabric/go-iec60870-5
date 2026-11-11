// SPDX-License-Identifier: MIT

package e2e

import (
	"errors"
	"io"
	"net"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/otfabric/go-iec60870-5/ft12"
	"github.com/otfabric/go-iec60870-5/iec103"
	"github.com/otfabric/go-iec60870-5/iec103/server"
	"github.com/otfabric/go-iec60870-5/link"
)

// eventually waits until ok holds.
func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestListAfterGIHasEquipmentFun: the ASDU 23 that follows a general
// interrogation carries the function type of the equipment (clause
// 7.3.1.11), not the GLB of the ASDU 7, and cause 31 (clause 7.4.7).
func TestListAfterGIHasEquipmentFun(t *testing.T) {
	r := start(t, options{noInit: true, handler: true})
	if _, err := r.c.Interrogate(ctx(t)); err != nil {
		t.Fatal(err)
	}
	a := r.expect(t, "list after GI", is(iec103.TypeDisturbanceList, iec103.CauseDisturbanceData))
	if a.Fun != fun {
		t.Fatalf("list after GI with FUN %d, want %d", a.Fun, fun)
	}
}

// rawServer starts a server driven by a bare link primary.
func rawServer(t *testing.T, cfg server.Config) (*server.Server, *link.Primary) {
	t.Helper()
	a, b := net.Pipe()
	cfg.LinkAddr, cfg.Fun = 4, fun
	s, err := server.New(b, cfg)
	if err != nil {
		t.Fatal(err)
	}
	pr, err := link.NewPrimary(a, link.Config{FT12: ft12.Params{AddrSize: 1}, Timeout: 100 * time.Millisecond, Retries: 2})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pr.Close(); _ = s.Close() })
	if _, err := pr.Init(ctx(t), 4); err != nil {
		t.Fatal(err)
	}
	// The two identifications.
	for range 2 {
		if _, _, err := pr.RequestData(ctx(t), 4, link.Class1); err != nil {
			t.Fatal(err)
		}
	}
	return s, pr
}

// drain polls class 1 until the server has nothing more.
func drain(t *testing.T, pr *link.Primary) []*iec103.ASDU {
	t.Helper()
	var out []*iec103.ASDU
	for {
		d, _, err := pr.RequestData(ctx(t), 4, link.Class1)
		if err != nil {
			t.Fatal(err)
		}
		if d == nil {
			return out
		}
		a, err := iec103.Decode(d)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, a)
	}
}

// TestNewGIAbortsCycle: a general interrogation (ASDU 7) or general
// interrogation of generic data (ASDU 21, INF 245) received while the
// previous cycle is still being transmitted aborts that cycle without its
// termination and starts a new one (clauses 7.4.3 and 7.4.8.7).
func TestNewGIAbortsCycle(t *testing.T) {
	var calls atomic.Int32
	h := server.HandlerFunc(func(s *server.Server, a *iec103.ASDU) {
		var data []*iec103.ASDU
		for i := range 20 {
			data = append(data, iec103.New(0, s.CommonAddr(), fun, iec103.InfoNumber(16+i), iec103.TimeTagged{DPI: iec103.DPIOn}))
		}
		var sets []iec103.DataSet
		for i := range 20 {
			sets = append(sets, iec103.DataSet{GIN: iec103.GIN{Group: 1, Entry: uint8(i)}, KOD: iec103.KODActualValue, Value: iec103.Uints(iec103.DataUint, 1, 1)})
		}
		switch a.Body.(type) {
		case iec103.GIInit:
			_ = s.AnswerGI(a, data...)
		case iec103.GenericCommand:
			_ = s.AnswerGenericGI(a, sets...)
		}
		calls.Add(1)
	})
	s, pr := rawServer(t, server.Config{Handler: h, Disturbances: newRecords()})
	send := func(a *iec103.ASDU) {
		t.Helper()
		b, _ := a.Encode()
		if _, err := pr.SendConfirm(ctx(t), 4, b); err != nil {
			t.Fatal(err)
		}
	}
	send(iec103.New(iec103.CauseGI, 4, iec103.FunGlobal, 0, iec103.GIInit{SCN: 1}))
	send(iec103.New(iec103.CauseGI, 4, iec103.FunGeneric, iec103.InfGenericGI, iec103.GenericCommand{RII: 1}))
	eventually(t, "first cycles queued", func() bool { return calls.Load() == 2 })
	// Take a few answers of the first cycles, then interrogate again.
	for range 3 {
		if _, _, err := pr.RequestData(ctx(t), 4, link.Class1); err != nil {
			t.Fatal(err)
		}
	}
	send(iec103.New(iec103.CauseGI, 4, iec103.FunGlobal, 0, iec103.GIInit{SCN: 2}))
	send(iec103.New(iec103.CauseGI, 4, iec103.FunGeneric, iec103.InfGenericGI, iec103.GenericCommand{RII: 2}))
	eventually(t, "second cycles queued", func() bool { return calls.Load() == 4 })
	var terms, ends, lists, gi2, ggi2 int
	for _, a := range drain(t, pr) {
		switch b := a.Body.(type) {
		case iec103.GITermination:
			if b.SCN != 2 {
				t.Errorf("termination of the aborted cycle %d", b.SCN)
			}
			terms++
		case iec103.TimeTagged:
			if b.SIN != 2 {
				t.Errorf("answer of the aborted cycle: %v SIN %d", a, b.SIN)
			}
			gi2++
		case iec103.GenericData:
			if b.RII != 2 {
				t.Errorf("generic answer of the aborted cycle: %v RII %d", a, b.RII)
			}
			if a.Inf == iec103.InfGenericGI {
				ends++
			} else {
				ggi2 += len(b.Sets)
			}
		case iec103.DisturbanceList:
			lists++
		}
	}
	if terms != 1 || ends != 1 || lists != 1 || gi2 != 20 || ggi2 != 20 {
		t.Fatalf("%d terminations, %d GGI ends, %d lists, %d GI answers, %d GGI sets", terms, ends, lists, gi2, ggi2)
	}
	if s.Pending(server.Class1) != 0 {
		t.Fatal("queue not empty")
	}
}

// TestDFCRefusesUserData: while the equipment sets DFC it does not process
// user data and answers NACK (clause 6.3); a broadcast is the exception.
func TestDFCRefusesUserData(t *testing.T) {
	release := make(chan struct{})
	var handled atomic.Int32
	_, pr := rawServer(t, server.Config{Handler: server.HandlerFunc(func(*server.Server, *iec103.ASDU) {
		handled.Add(1)
		<-release
	})})
	defer close(release)
	priv, _ := (&iec103.ASDU{Type: 200, VSQ: 0x81, Cause: 1, CommonAddr: 4, Fun: fun, Inf: 1, Body: iec103.Raw{Data: []byte{1}}}).Encode()
	accepted := 0
	for {
		st, err := pr.SendConfirm(ctx(t), 4, priv)
		if err != nil {
			t.Fatalf("after %d accepted: %v", accepted, err)
		}
		accepted++
		if st.DFC {
			break
		}
		if accepted > 100 {
			t.Fatal("DFC never set")
		}
	}
	if _, err := pr.SendConfirm(ctx(t), 4, priv); !errors.Is(err, link.ErrNack) {
		t.Fatalf("user data while DFC set: %v", err)
	}
	// A broadcast is still taken.
	sync, _ := iec103.New(iec103.CauseTimeSync, 255, iec103.FunGlobal, 0, iec103.TimeSync{}).Encode()
	if err := pr.SendNoReply(255, sync); err != nil {
		t.Fatal(err)
	}
}

// TestDFCHeldBack: the client holds back control-direction ASDUs while the
// equipment sets DFC (clause 6.3), so none of a burst is refused.
func TestDFCHeldBack(t *testing.T) {
	release := make(chan struct{})
	var handled atomic.Int32
	h := server.HandlerFunc(func(*server.Server, *iec103.ASDU) {
		<-release
		handled.Add(1)
	})
	r := start(t, options{custom: h, noInit: true, noDist: true})
	priv := &iec103.ASDU{Type: 200, VSQ: 0x81, Cause: 1, CommonAddr: 3, Fun: fun, Inf: 1, Body: iec103.Raw{Data: []byte{1}}}
	const n = 60
	errs := make(chan error, n)
	for range n {
		go func() { errs <- r.c.Send(ctx(t), priv) }()
	}
	time.Sleep(300 * time.Millisecond)
	close(release)
	for range n {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	eventually(t, "all handled", func() bool { return handled.Load() == n })
}

// TestDisturbanceAbortByEquipment: an implausible order ends the transfer
// in progress with TOO 34 and the updated list follows (clause 7.4.7).
func TestDisturbanceAbortByEquipment(t *testing.T) {
	r := start(t, options{noInit: true, handler: true})
	if err := r.c.Send(ctx(t), iec103.New(iec103.CauseDisturbanceData, 3, fun, 0, iec103.DisturbanceOrder{TOO: iec103.TOOSelectFault, FAN: 7})); err != nil {
		t.Fatal(err)
	}
	r.expect(t, "ready", is(iec103.TypeDisturbanceReady, iec103.CauseDisturbanceData))
	if e := r.order(t, iec103.DisturbanceOrder{TOO: 5, FAN: 7}); e.TOO != iec103.TOOEndDataAbortProt {
		t.Fatal(e)
	}
	a := r.expect(t, "updated list", is(iec103.TypeDisturbanceList, iec103.CauseDisturbanceData))
	if a.Fun != fun {
		t.Fatal(a)
	}
	// The transfer is over: a request for its data is implausible now.
	if e := r.order(t, iec103.DisturbanceOrder{TOO: iec103.TOORequestData, FAN: 7}); e.TOO != iec103.TOOEndDataAbortProt {
		t.Fatal(e)
	}
}

// slowPoll delays one class 2 poll of the client's link layer.
type slowPoll struct {
	io.ReadWriteCloser
	armed   atomic.Bool
	entered chan struct{}
}

func (s *slowPoll) Write(b []byte) (int, error) {
	if len(b) == 5 && b[0] == 0x10 && b[1]&0x0F == link.FuncRequestClass2 && s.armed.CompareAndSwap(true, false) {
		s.entered <- struct{}{}
		time.Sleep(300 * time.Millisecond)
	}
	return s.ReadWriteCloser.Write(b)
}

// TestClockSyncStampedAtSend: the synchronization command carries the time
// at which it is transmitted (clause 7.4.2), not the time it was queued
// while the link was busy with a poll.
func TestClockSyncStampedAtSend(t *testing.T) {
	type rx struct{ t, at time.Time }
	got := make(chan rx, 4)
	sp := &slowPoll{entered: make(chan struct{}, 1)}
	r := start(t, options{noInit: true, handler: true, link: time.Second,
		wrap: func(c io.ReadWriteCloser) io.ReadWriteCloser { sp.ReadWriteCloser = c; return sp },
		server: func(sc *server.Config) {
			sc.SyncDelay = -1
			sc.OnTimeSync = func(t time.Time, _ bool) { got <- rx{t, time.Now()} }
		}})
	for _, broadcast := range []bool{false, true} {
		sp.armed.Store(true)
		<-sp.entered
		var err error
		if broadcast {
			err = r.c.BroadcastClockSync(time.Now())
		} else {
			_, err = r.c.ClockSync(ctx(t), time.Now())
		}
		if err != nil {
			t.Fatal(err)
		}
		x := <-got
		if lag := x.at.Sub(x.t); lag > 150*time.Millisecond || lag < -150*time.Millisecond {
			t.Errorf("broadcast %v: time %v behind the reception", broadcast, lag)
		}
	}
}

// TestSyncTimeout: time tags carry IV once no synchronization came for
// SyncTimeout (23 h by default, clause 7.4.2).
func TestSyncTimeout(t *testing.T) {
	r := start(t, options{handler: true, server: func(sc *server.Config) { sc.SyncTimeout = 300 * time.Millisecond }})
	r.expect(t, "time synchronization answer", is(iec103.TypeTimeSync, iec103.CauseTimeSync))
	if r.s.Now().Invalid {
		t.Fatal("invalid right after the synchronization")
	}
	if err := r.s.Event(iec103.CauseSpontaneous, fun, iec103.InfGeneralTrip, iec103.DPIOn); err != nil {
		t.Fatal(err)
	}
	if a := r.expect(t, "event", is(iec103.TypeTimeTagged, iec103.CauseSpontaneous)); a.Body.(iec103.TimeTagged).Time.Invalid {
		t.Fatal("IV set while synchronized")
	}
	time.Sleep(400 * time.Millisecond)
	if !r.s.Now().Invalid {
		t.Fatal("valid after SyncTimeout")
	}
	if err := r.s.Event(iec103.CauseSpontaneous, fun, iec103.InfGeneralTrip, iec103.DPIOff); err != nil {
		t.Fatal(err)
	}
	if a := r.expect(t, "event", is(iec103.TypeTimeTagged, iec103.CauseSpontaneous)); !a.Body.(iec103.TimeTagged).Time.Invalid {
		t.Fatal("IV clear after SyncTimeout")
	}
}

// scripted is protection equipment that runs a disturbance data transfer
// from a script, to test the client's acknowledgements (clause 7.4.7,
// figure 10).
type scripted struct {
	tagsEnd  iec103.TOO
	firstTAP uint16
	noc      uint8
	chEnd    []iec103.TOO // the channels offered and how each ends
	dataEnd  iec103.TOO

	mu   sync.Mutex
	next int
	acks []iec103.TOO
}

func (q *scripted) HandleASDU(s *server.Server, a *iec103.ASDU) {
	o, ok := a.Body.(iec103.DisturbanceOrder)
	if !ok {
		return
	}
	send := func(b iec103.Body) {
		_ = s.Send(iec103.New(iec103.CauseDisturbanceData, s.CommonAddr(), fun, 0, b), server.Class1)
	}
	end := func(too iec103.TOO, acc uint8) {
		send(iec103.EndOfTransmission{TOO: too, TOV: 1, FAN: 7, ACC: acc})
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if o.Ack {
		q.acks = append(q.acks, o.TOO)
		switch o.TOO {
		case iec103.TOOTagsOK, iec103.TOOTagsFailed, iec103.TOOChannelOK, iec103.TOOChannelFailed:
			if q.next < len(q.chEnd) {
				q.next++
				send(iec103.ChannelReady{TOV: 1, FAN: 7, ACC: uint8(q.next)})
				return
			}
			end(q.dataEnd, 0)
		default:
		}
		return
	}
	switch o.TOO {
	case iec103.TOOSelectFault:
		send(iec103.DisturbanceReady{TOV: 1, FAN: 7, NOC: q.noc, NOE: 2, INT: 1000})
	case iec103.TOORequestData:
		send(iec103.TagsReady{FAN: 7})
	case iec103.TOORequestTags:
		send(iec103.Tags{FAN: 7, TAP: q.firstTAP, Tags: []iec103.Tag{{Fun: fun, Inf: 84, DPI: iec103.DPIOff}}})
		end(q.tagsEnd, 0)
	case iec103.TOORequestChannel:
		send(iec103.DisturbanceValues{TOV: 1, FAN: 7, ACC: o.ACC, Values: []int16{1, 2}})
		end(q.chEnd[o.ACC-1], o.ACC)
	default:
	}
}

// TestDisturbanceClientAcks: the client acknowledges every ASDU 31 with an
// ASDU 25 (clause 7.4.7, figure 10 sheets 2 to 4), also those that report
// an abortion by the protection equipment (TOO 37 and 40 negative, then
// the transfer goes on; TOO 34 negative, then it ends), and checks the
// tags (initial state at position 0) and the number of channels.
func TestDisturbanceClientAcks(t *testing.T) {
	cases := []struct {
		name string
		q    *scripted
		acks []iec103.TOO
		err  error
	}{
		{"complete", &scripted{tagsEnd: iec103.TOOEndTags, noc: 1, chEnd: []iec103.TOO{iec103.TOOEndChannel}, dataEnd: iec103.TOOEndData},
			[]iec103.TOO{iec103.TOOTagsOK, iec103.TOOChannelOK, iec103.TOODataOK}, nil},
		{"tags and channel aborted", &scripted{tagsEnd: iec103.TOOEndTagsAbortProt, noc: 2, chEnd: []iec103.TOO{iec103.TOOEndChannelAbortPrt, iec103.TOOEndChannel}, dataEnd: iec103.TOOEndData},
			[]iec103.TOO{iec103.TOOTagsFailed, iec103.TOOChannelFailed, iec103.TOOChannelOK, iec103.TOODataFailed}, iec103.ErrNegative},
		{"initial tags not at 0, channel missing", &scripted{tagsEnd: iec103.TOOEndTags, firstTAP: 5, noc: 2, chEnd: []iec103.TOO{iec103.TOOEndChannel}, dataEnd: iec103.TOOEndData},
			[]iec103.TOO{iec103.TOOTagsFailed, iec103.TOOChannelOK, iec103.TOODataFailed}, iec103.ErrNegative},
		{"data aborted", &scripted{tagsEnd: iec103.TOOEndTags, noc: 1, chEnd: []iec103.TOO{iec103.TOOEndChannel}, dataEnd: iec103.TOOEndDataAbortProt},
			[]iec103.TOO{iec103.TOOTagsOK, iec103.TOOChannelOK, iec103.TOODataFailed}, iec103.ErrAborted},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := start(t, options{custom: c.q, noInit: true, noDist: true})
			rec, err := r.c.Disturbance(ctx(t), fun, 7)
			if c.err == nil && err != nil || c.err != nil && !errors.Is(err, c.err) {
				t.Fatalf("%v, want %v", err, c.err)
			}
			if rec == nil || len(rec.Tags) != 1 {
				t.Fatalf("%+v", rec)
			}
			eventually(t, "acknowledgements", func() bool {
				c.q.mu.Lock()
				defer c.q.mu.Unlock()
				return len(c.q.acks) >= len(c.acks)
			})
			c.q.mu.Lock()
			defer c.q.mu.Unlock()
			if !reflect.DeepEqual(c.q.acks, c.acks) {
				t.Fatalf("acknowledgements %v, want %v", c.q.acks, c.acks)
			}
		})
	}
}

// TestCommandDCO: the double command is OFF or ON (clause 7.2.6.4), and
// only ON for INF 19 and 23 to 26 (table 17, column COM). The client
// refuses other values; the server acknowledges them negatively.
func TestCommandDCO(t *testing.T) {
	r := start(t, options{noInit: true, handler: true})
	for _, x := range []struct {
		inf iec103.InfoNumber
		dco iec103.DPI
	}{{iec103.InfLEDReset, iec103.DPIOff}, {16, 0}, {16, 3}, {23, iec103.DPIOff}} {
		if err := r.c.Command(ctx(t), fun, x.inf, x.dco); !errors.Is(err, iec103.ErrInvalid) {
			t.Errorf("INF %d DCO %d: %v", x.inf, x.dco, err)
		}
		if err := r.c.Send(ctx(t), iec103.New(iec103.CauseGeneralCommand, 3, fun, x.inf, iec103.GeneralCommand{DCO: x.dco, RII: 77})); err != nil {
			t.Fatal(err)
		}
		a := r.expect(t, "acknowledgement", func(a *iec103.ASDU) bool { return a.Type == iec103.TypeTimeTagged && a.Inf == x.inf })
		if a.Cause != iec103.CauseCommandNack || a.Body.(iec103.TimeTagged).SIN != 77 {
			t.Errorf("INF %d DCO %d: %v", x.inf, x.dco, a)
		}
	}
	// The command procedure is not blocked by them.
	if err := r.c.Command(ctx(t), fun, iec103.InfLEDReset, iec103.DPIOn); err != nil {
		t.Fatal(err)
	}
}

// TestTestModeAndBlocking covers clauses 7.4.5 and 7.4.6 as far as the
// library supports them: neither is a server feature, the application
// reports them (INF 21, INF 20) and sends the causes itself; the client
// delivers everything to the Handler and keeps GI answers apart.
func TestTestModeAndBlocking(t *testing.T) {
	var blocked atomic.Bool
	h := server.HandlerFunc(func(s *server.Server, a *iec103.ASDU) {
		if _, ok := a.Body.(iec103.GIInit); !ok {
			return
		}
		if blocked.Load() {
			// Clause 7.4.6: a GI is answered with its termination only.
			_ = s.AnswerGI(a)
			return
		}
		_ = s.AnswerGI(a, iec103.New(0, s.CommonAddr(), fun, iec103.InfTestMode, iec103.TimeTagged{DPI: iec103.DPIOn}))
	})
	r := start(t, options{custom: h, noInit: true, noDist: true})
	// Test mode: spontaneous messages with cause 7; GI answers keep cause 9.
	if err := r.s.Event(iec103.CauseSpontaneous, fun, iec103.InfTestMode, iec103.DPIOn); err != nil {
		t.Fatal(err)
	}
	if err := r.s.Event(iec103.CauseTestMode, fun, iec103.InfGeneralTrip, iec103.DPIOn); err != nil {
		t.Fatal(err)
	}
	r.expect(t, "test mode on", func(a *iec103.ASDU) bool { return a.Inf == iec103.InfTestMode && a.Cause == iec103.CauseSpontaneous })
	r.expect(t, "event in test mode", is(iec103.TypeTimeTagged, iec103.CauseTestMode))
	data, err := r.c.Interrogate(ctx(t))
	if err != nil || len(data) != 1 || data[0].Cause != iec103.CauseGI {
		t.Fatal(data, err)
	}
	// Monitor direction blocked: the GI ends without data.
	if err := r.s.Event(iec103.CauseSpontaneous, fun, iec103.InfMonitorBlocked, iec103.DPIOn); err != nil {
		t.Fatal(err)
	}
	blocked.Store(true)
	r.expect(t, "blocking on", func(a *iec103.ASDU) bool { return a.Inf == iec103.InfMonitorBlocked })
	if data, err := r.c.Interrogate(ctx(t)); err != nil || len(data) != 0 {
		t.Fatal(data, err)
	}
	// Time synchronization keeps working (clause 7.4.6).
	if _, err := r.c.ClockSync(ctx(t), time.Now()); err != nil {
		t.Fatal(err)
	}
}
