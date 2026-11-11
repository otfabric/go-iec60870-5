// SPDX-License-Identifier: MIT

package e2e

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/otfabric/go-iec60870-5/cp"
	"github.com/otfabric/go-iec60870-5/iec103"
	"github.com/otfabric/go-iec60870-5/iec103/client"
	"github.com/otfabric/go-iec60870-5/iec103/server"
	"github.com/otfabric/go-iec60870-5/link"
)

const fun = iec103.FunDistance

// device is the application of a protection equipment station: a few
// indications, a generic directory and one command that waits.
type device struct {
	mu      sync.Mutex
	written map[iec103.GIN]iec103.Value
	queued  []iec103.DataSet
	block   chan struct{} // INF 23 waits for it before acknowledging
	entered chan struct{} // INF 23 arrived
	gis     int
	writes  []writeASDU // every ASDU 10 of a write, as received
}

// writeASDU is what the device saw of one ASDU 10 of a generic write.
type writeASDU struct {
	inf iec103.InfoNumber
	rii uint8
	ngd iec103.Counted
	n   int
}

func newDevice() *device {
	return &device{written: map[iec103.GIN]iec103.Value{}, block: make(chan struct{}), entered: make(chan struct{}, 8)}
}

var headings = []iec103.DataSet{
	{GIN: iec103.GIN{Group: 0}, KOD: iec103.KODDescription, Value: iec103.ASCII("System data")},
	{GIN: iec103.GIN{Group: 1}, KOD: iec103.KODDescription, Value: iec103.ASCII("Measurands")},
}

func (d *device) HandleASDU(s *server.Server, a *iec103.ASDU) {
	switch b := a.Body.(type) {
	case iec103.GIInit:
		d.mu.Lock()
		d.gis++
		d.mu.Unlock()
		_ = s.AnswerGI(a,
			iec103.New(0, s.CommonAddr(), fun, iec103.InfProtectionActive, iec103.TimeTagged{DPI: iec103.DPIOn, Time: s.Now()}),
			iec103.New(0, s.CommonAddr(), fun, iec103.InfGeneralStart, iec103.RelTimeTagged{DPI: iec103.DPIOff, Time: s.Now()}))
	case iec103.GeneralCommand:
		switch a.Inf {
		case iec103.InfLEDReset:
			_ = s.AckCommand(a, true)
			_ = s.Event(iec103.CauseRemoteOperation, a.Fun, a.Inf, b.DCO)
		case 23:
			d.entered <- struct{}{}
			select {
			case <-d.block:
			case <-time.After(10 * time.Second):
			}
			_ = s.AckCommand(a, true)
		default:
			_ = s.AckCommand(a, false)
		}
	case iec103.GenericCommand:
		d.generic(s, a, b)
	case iec103.GenericData:
		d.write(s, a, b)
	}
}

func (d *device) generic(s *server.Server, a *iec103.ASDU, b iec103.GenericCommand) {
	switch a.Inf {
	case iec103.InfReadHeadings:
		_ = s.GenericAnswer(a, iec103.CauseGenericReadValid, headings...)
	case iec103.InfReadGroup:
		// A group large enough for several ASDUs.
		var sets []iec103.DataSet
		for i := range 80 {
			sets = append(sets, iec103.DataSet{GIN: iec103.GIN{Group: 1, Entry: uint8(i)}, KOD: b.Items[0].KOD, Value: iec103.Float32s(float32(i))})
		}
		_ = s.GenericAnswer(a, iec103.CauseGenericReadValid, sets...)
	case iec103.InfReadDirectory:
		_ = s.GenericIdentAnswer(a, iec103.CauseGenericReadValid, b.Items[0].GIN,
			iec103.DescElement{KOD: iec103.KODActualValue, Value: iec103.Uints(iec103.DataUint, 1, 3)},
			iec103.DescElement{KOD: iec103.KODDescription, Value: iec103.ASCII("Tap position")})
	case iec103.InfReadEntry:
		it := b.Items[0]
		d.mu.Lock()
		v, ok := d.written[it.GIN]
		d.mu.Unlock()
		if !ok {
			_ = s.GenericAnswer(a, iec103.CauseGenericReadInvalid, iec103.DataSet{GIN: it.GIN, KOD: it.KOD, Value: iec103.Reply(iec103.GRCNotExist)})
			return
		}
		_ = s.GenericAnswer(a, iec103.CauseGenericReadValid, iec103.DataSet{GIN: it.GIN, KOD: it.KOD, Value: v})
	case iec103.InfGenericGI:
		_ = s.AnswerGenericGI(a, headings...)
	default:
	}
}

func (d *device) write(s *server.Server, a *iec103.ASDU, b iec103.GenericData) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.writes = append(d.writes, writeASDU{inf: a.Inf, rii: b.RII, ngd: b.NGD, n: len(b.Sets)})
	d.queued = append(d.queued, b.Sets...)
	if b.NGD.Cont {
		return
	}
	switch a.Inf {
	case iec103.InfWriteConfirm:
		_ = s.GenericAnswer(a, iec103.CauseGenericWriteConf, d.queued...)
	case iec103.InfWriteExecute:
		var refused []iec103.DataSet
		for _, x := range d.queued {
			if x.GIN.Group == 9 {
				refused = append(refused, iec103.DataSet{GIN: x.GIN, Value: iec103.Reply(iec103.GRCOutOfRange)})
				continue
			}
			d.written[x.GIN] = x.Value
		}
		d.queued = nil
		if refused != nil {
			_ = s.GenericAnswer(a, iec103.CauseGenericWriteNack, refused...)
			return
		}
		_ = s.GenericAnswer(a, iec103.CauseGenericWriteAck)
	case iec103.InfWriteAbort:
		d.queued = nil
		_ = s.GenericAnswer(a, iec103.CauseGenericWriteAck)
	default:
	}
}

// records is a disturbance source with one record: two tags at position 0,
// 30 changes at position 10, two channels of 60 values.
type records struct {
	mu   sync.Mutex
	rec  *iec103.Disturbance
	acks []bool
}

func newRecords() *records {
	r := &iec103.Disturbance{Fun: fun, TOV: iec103.TOVInstantaneous, FAN: 7, NOF: 3, Interval: 1000,
		Start: cp.Timestamp{Time: time.Date(1, 1, 1, 9, 30, 0, 0, time.UTC)}}
	r.Tags = []iec103.TagChange{{Pos: 0, Tag: iec103.Tag{Fun: fun, Inf: 84, DPI: iec103.DPIOff}}, {Pos: 0, Tag: iec103.Tag{Fun: fun, Inf: 68, DPI: iec103.DPIOff}}}
	for i := range 30 {
		r.Tags = append(r.Tags, iec103.TagChange{Pos: 10, Tag: iec103.Tag{Fun: fun, Inf: iec103.InfoNumber(i), DPI: iec103.DPIOn}})
	}
	for _, acc := range []uint8{iec103.ACCIL1, iec103.ACCIL2} {
		ch := iec103.Channel{ACC: acc, RPV: 1000, RSV: 1, RFA: 0.5}
		for i := range 60 {
			ch.Values = append(ch.Values, int16(i*int(acc)))
		}
		r.Channels = append(r.Channels, ch)
	}
	return &records{rec: r}
}

func (r *records) List() []iec103.RecordedFault {
	return []iec103.RecordedFault{{FAN: r.rec.FAN, SOF: iec103.SOFTrip, Time: cp.Timestamp{Time: time.Date(2026, 11, 11, 9, 30, 0, 0, time.UTC)}}}
}

func (r *records) Record(fan uint16) (*iec103.Disturbance, bool) {
	return r.rec, fan == r.rec.FAN
}

// waitAcks waits for the final acknowledgement of a transfer, which
// reaches the source on the server's goroutine after the client's link
// layer saw it confirmed.
func (r *records) waitAcks(t *testing.T) []bool {
	t.Helper()
	eventually(t, "final acknowledgement", func() bool {
		r.mu.Lock()
		defer r.mu.Unlock()
		return len(r.acks) > 0
	})
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]bool(nil), r.acks...)
}

func (r *records) Transmitted(_ uint16, ok bool) {
	r.mu.Lock()
	r.acks = append(r.acks, ok)
	r.mu.Unlock()
}

// lossy drops every n-th write while n > 0, or every write while muted.
type lossy struct {
	io.ReadWriteCloser
	mu    sync.Mutex
	n, i  int
	muted bool
}

func (l *lossy) Write(b []byte) (int, error) {
	l.mu.Lock()
	l.i++
	drop := l.muted || (l.n > 0 && l.i%l.n == 0)
	l.mu.Unlock()
	if drop {
		return len(b), nil
	}
	return l.ReadWriteCloser.Write(b)
}

func (l *lossy) mute(m bool) { l.mu.Lock(); l.muted = m; l.mu.Unlock() }

type rig struct {
	c    *client.Client
	s    *server.Server
	dev  *device
	rec  *records
	line *lossy
	got  chan *iec103.ASDU
	sync atomic.Int32
}

type options struct {
	resetFCB bool
	dropN    int
	noInit   bool
	handler  bool
	custom   server.Handler // instead of the device
	noDist   bool           // no disturbance source
	timeout  time.Duration  // client request timeout
	onState  func(iec103.State, error)
	// server is applied to the server configuration; wrap to the client's
	// side of the line.
	server func(*server.Config)
	wrap   func(io.ReadWriteCloser) io.ReadWriteCloser
	link   time.Duration // link time-out, default 50 ms
}

func start(t *testing.T, o options) *rig {
	t.Helper()
	a, b := net.Pipe()
	r := &rig{dev: newDevice(), rec: newRecords(), got: make(chan *iec103.ASDU, 1024)}
	r.line = &lossy{ReadWriteCloser: b, n: o.dropN}
	lc := link.Config{Timeout: 50 * time.Millisecond, Retries: 3}
	if o.link > 0 {
		lc.Timeout = o.link
	}
	var h server.Handler = r.dev
	if !o.handler {
		h = nil
	}
	if o.custom != nil {
		h = o.custom
	}
	var src server.DisturbanceSource = r.rec
	if o.noDist {
		src = nil
	}
	scfg := server.Config{
		Link: lc, LinkAddr: 3, Fun: fun, Handler: h, Disturbances: src,
		Identification: iec103.IdentificationOf(iec103.COLWithGeneric, "OTFABRIC", [4]byte{1, 0, 0, 0}),
		OnTimeSync:     func(time.Time, bool) { r.sync.Add(1) },
	}
	if o.server != nil {
		o.server(&scfg)
	}
	var err error
	r.s, err = server.New(r.line, scfg)
	if err != nil {
		t.Fatal(err)
	}
	var cl io.ReadWriteCloser = a
	if o.wrap != nil {
		cl = o.wrap(a)
	}
	r.c, err = client.New(cl, client.Config{
		Link: lc, LinkAddr: 3, ResetFCB: o.resetFCB, NoInitGI: o.noInit, NoInitTimeSync: o.noInit,
		PollInterval: 5 * time.Millisecond, RetryInterval: 50 * time.Millisecond, RequestTimeout: o.timeout,
		Handler: func(a *iec103.ASDU) { r.got <- a }, OnState: o.onState,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.c.Close(); _ = r.s.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r.c.WaitUp(ctx); err != nil {
		t.Fatal(err)
	}
	return r
}

func ctx(t *testing.T) context.Context {
	c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return c
}

// expect waits for an ASDU of the handler that satisfies ok.
func (r *rig) expect(t *testing.T, what string, ok func(*iec103.ASDU) bool) *iec103.ASDU {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case a := <-r.got:
			if ok(a) {
				return a
			}
		case <-timeout:
			t.Fatalf("no %s", what)
		}
	}
}

func is(typ iec103.TypeID, cause iec103.Cause) func(*iec103.ASDU) bool {
	return func(a *iec103.ASDU) bool { return a.Type == typ && a.Cause == cause }
}

// TestInitialization covers clause 7.4.1 with reset CU: the identification
// with cause 4, then the one with cause 6 (power on), then the time
// synchronization and the general interrogation the client runs.
func TestInitialization(t *testing.T) {
	r := start(t, options{handler: true})
	a := r.expect(t, "identification", func(a *iec103.ASDU) bool { return a.Type == iec103.TypeIdentification })
	if a.Cause != iec103.CauseResetCU || a.Inf != iec103.InfResetCU || a.Fun != fun {
		t.Fatalf("first identification %v", a)
	}
	if id := a.Body.(iec103.Identification); id.Manufacturer() != "OTFABRIC" || id.COL != iec103.COLWithGeneric {
		t.Fatalf("%+v", id)
	}
	a = r.expect(t, "identification", func(a *iec103.ASDU) bool { return a.Type == iec103.TypeIdentification })
	if a.Cause != iec103.CausePowerOn || a.Inf != iec103.InfPowerOn {
		t.Fatalf("second identification %v", a)
	}
	r.expect(t, "time synchronization answer", is(iec103.TypeTimeSync, iec103.CauseTimeSync))
	r.expect(t, "GI termination", is(iec103.TypeGITermination, iec103.CauseGITermination))
	r.expect(t, "disturbance list after GI", is(iec103.TypeDisturbanceList, iec103.CauseDisturbanceData))
	if r.c.Identification() == nil || r.sync.Load() != 1 || r.s.Now().Invalid {
		t.Fatalf("identification %v, %d syncs, server time invalid %v", r.c.Identification(), r.sync.Load(), r.s.Now().Invalid)
	}
	if r.c.State() != iec103.StateUp || r.s.State() != iec103.StateUp || r.c.CommonAddr() != 3 {
		t.Fatal("states")
	}
}

// TestInitializationResetFCB covers reset FCB (function 7): the
// identification has cause 3 and INF 2 (table 8).
func TestInitializationResetFCB(t *testing.T) {
	r := start(t, options{resetFCB: true, noInit: true, handler: true})
	a := r.expect(t, "identification", func(a *iec103.ASDU) bool { return a.Type == iec103.TypeIdentification })
	if a.Cause != iec103.CauseResetFCB || a.Inf != iec103.InfResetFCB {
		t.Fatalf("%v", a)
	}
	if !r.s.Now().Invalid {
		t.Fatal("server time valid without synchronization")
	}
}

// TestGeneralInterrogation covers clause 7.4.3: the answers carry cause 9
// and the scan number as SIN.
func TestGeneralInterrogation(t *testing.T) {
	r := start(t, options{noInit: true, handler: true})
	data, err := r.c.Interrogate(ctx(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 2 || data[0].Cause != iec103.CauseGI || data[0].Inf != iec103.InfProtectionActive || data[1].Type != iec103.TypeRelTimeTagged {
		t.Fatalf("%v", data)
	}
	scn := data[0].Body.(iec103.TimeTagged).SIN
	if data[1].Body.(iec103.RelTimeTagged).SIN != scn {
		t.Fatal("SIN differs")
	}
	data2, err := r.c.Interrogate(ctx(t))
	if err != nil || len(data2) != 2 || data2[0].Body.(iec103.TimeTagged).SIN == scn {
		t.Fatalf("second GI %v %v", data2, err)
	}
}

// TestTimeSync covers clause 7.4.2, addressed and broadcast.
func TestTimeSync(t *testing.T) {
	r := start(t, options{noInit: true, handler: true, server: func(sc *server.Config) { sc.AnswerBroadcastSync = true }})
	want := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	got, err := r.c.ClockSync(ctx(t), want)
	if err != nil {
		t.Fatal(err)
	}
	if got.Invalid || got.Sub(want) < 0 || got.Sub(want) > 5*time.Second {
		t.Fatalf("answer %v", got)
	}
	if err := r.c.BroadcastClockSync(want); err != nil {
		t.Fatal(err)
	}
	r.expect(t, "answer to broadcast", is(iec103.TypeTimeSync, iec103.CauseTimeSync))
	deadline := time.Now().Add(5 * time.Second)
	for r.sync.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if r.sync.Load() != 2 {
		t.Fatalf("%d synchronizations", r.sync.Load())
	}
}

// TestCommands covers clause 7.4.4: positive and negative
// acknowledgements, the change of state with cause 12, and one command at
// a time.
func TestCommands(t *testing.T) {
	r := start(t, options{noInit: true, handler: true})
	if err := r.c.Command(ctx(t), fun, iec103.InfLEDReset, iec103.DPIOn); err != nil {
		t.Fatal(err)
	}
	a := r.expect(t, "change of state", is(iec103.TypeTimeTagged, iec103.CauseRemoteOperation))
	if a.Inf != iec103.InfLEDReset || a.Body.(iec103.TimeTagged).DPI != iec103.DPIOn {
		t.Fatalf("%v", a)
	}
	if err := r.c.Command(ctx(t), fun, iec103.InfAutoRecloser, iec103.DPIOff); !errors.Is(err, iec103.ErrNegative) {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- r.c.Command(ctx(t), fun, 23, iec103.DPIOn) }()
	<-r.dev.entered
	if err := r.c.Command(ctx(t), fun, iec103.InfLEDReset, iec103.DPIOn); !errors.Is(err, iec103.ErrBusy) {
		t.Fatal(err)
	}
	close(r.dev.block)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// TestClasses covers class 2 measurands and spontaneous class 1 events.
func TestClasses(t *testing.T) {
	r := start(t, options{noInit: true, handler: true})
	m := iec103.New(iec103.CauseCyclic, 3, fun, iec103.InfMeasurandsIVPQf, iec103.Measurands{Set2: true, Values: []iec103.MEA{iec103.MEAOf(0.5), iec103.MEAOf(-0.25)}})
	if err := r.s.Send(m, server.Class2); err != nil {
		t.Fatal(err)
	}
	if err := r.s.Event(iec103.CauseSpontaneous, fun, iec103.InfGeneralTrip, iec103.DPIOn); err != nil {
		t.Fatal(err)
	}
	var event, meas *iec103.ASDU
	for event == nil || meas == nil {
		a := r.expect(t, "event and measurands", func(a *iec103.ASDU) bool {
			return is(iec103.TypeTimeTagged, iec103.CauseSpontaneous)(a) || is(iec103.TypeMeasurands2, iec103.CauseCyclic)(a)
		})
		if a.Type == iec103.TypeTimeTagged {
			event = a
		} else {
			meas = a
		}
	}
	if v := meas.Body.(iec103.Measurands).Values[0].Value(); v != 0.5 {
		t.Fatal(v)
	}
	if r.s.Pending(server.Class1) != 0 || r.s.Pending(server.Class2) != 0 {
		t.Fatal("queues not empty")
	}
}

// TestGenericServices covers clause 7.4.8: reads of headings, a group over
// several ASDUs, a directory and an entry; writes with confirmation,
// execution (refused and accepted), abort and without answer; the general
// interrogation of generic data.
func TestGenericServices(t *testing.T) {
	r := start(t, options{noInit: true, handler: true})
	c := ctx(t)
	res, err := r.c.ReadHeadings(c)
	if err != nil || len(res.Sets) != 2 || res.Sets[1].Value.Text() != "Measurands" || res.Cause != iec103.CauseGenericReadValid {
		t.Fatalf("%+v %v", res, err)
	}
	res, err = r.c.ReadGroup(c, 1, iec103.KODActualValue)
	if err != nil || len(res.Sets) != 80 || len(res.ASDUs) < 2 {
		t.Fatalf("%d sets in %d ASDUs: %v", len(res.Sets), len(res.ASDUs), err)
	}
	if v, _ := res.Sets[79].Value.Float(0); v != 79 {
		t.Fatal(v)
	}
	res, err = r.c.ReadDirectory(c, iec103.GIN{Group: 2, Entry: 0x11})
	if err != nil || res.GIN != (iec103.GIN{Group: 2, Entry: 0x11}) || len(res.Elements) != 2 {
		t.Fatalf("%+v %v", res, err)
	}
	gin := iec103.GIN{Group: 2, Entry: 1}
	res, err = r.c.ReadEntry(c, gin, iec103.KODActualValue)
	if err != nil || res.Cause != iec103.CauseGenericReadInvalid || res.Sets[0].Value.Type != iec103.DataGRC {
		t.Fatalf("%+v %v", res, err)
	}
	set := iec103.DataSet{GIN: gin, KOD: iec103.KODActualValue, Value: iec103.Uints(iec103.DataIndex, 2, 2)}
	res, err = r.c.GenericWrite(c, iec103.InfWriteConfirm, set)
	if err != nil || res.Cause != iec103.CauseGenericWriteConf || len(res.Sets) != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	res, err = r.c.GenericWrite(c, iec103.InfWriteExecute)
	if err != nil || res.Cause != iec103.CauseGenericWriteAck {
		t.Fatalf("%+v %v", res, err)
	}
	res, err = r.c.ReadEntry(c, gin, iec103.KODActualValue)
	if v, _ := res.Sets[0].Value.Uint(0); err != nil || v != 2 {
		t.Fatalf("%+v %v", res, err)
	}
	// Refused: group 9.
	res, err = r.c.GenericWrite(c, iec103.InfWriteExecute, iec103.DataSet{GIN: iec103.GIN{Group: 9}, Value: iec103.Uints(iec103.DataUint, 1, 1)})
	if !errors.Is(err, iec103.ErrNegative) || res.Sets[0].Value.Data[0] != byte(iec103.GRCOutOfRange) {
		t.Fatalf("%+v %v", res, err)
	}
	// Write entry (no answer), then abort.
	if res, err := r.c.GenericWrite(c, iec103.InfWriteEntry, set); err != nil || res != nil {
		t.Fatal(res, err)
	}
	if res, err := r.c.GenericWrite(c, iec103.InfWriteAbort); err != nil || res.Cause != iec103.CauseGenericWriteAck {
		t.Fatal(res, err)
	}
	// Many sets: the write is split over several ASDUs.
	var many []iec103.DataSet
	for i := range 70 {
		many = append(many, iec103.DataSet{GIN: iec103.GIN{Group: 4, Entry: uint8(i)}, KOD: iec103.KODActualValue, Value: iec103.Float32s(1)})
	}
	if res, err := r.c.GenericWrite(c, iec103.InfWriteConfirm, many...); err != nil || len(res.Sets) != 70 {
		t.Fatal(err)
	}
	sets, err := r.c.GenericInterrogation(c)
	if err != nil || len(sets) != 2 {
		t.Fatalf("%v %v", sets, err)
	}
	if _, err := r.c.GenericRead(c, 99); !errors.Is(err, iec103.ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := r.c.GenericWrite(c, 99); !errors.Is(err, iec103.ErrInvalid) {
		t.Fatal(err)
	}
}

// TestDisturbanceTransfer covers clause 7.4.7 against the server's built-in
// procedure: the list, the whole transfer, an unknown fault number.
func TestDisturbanceTransfer(t *testing.T) {
	r := start(t, options{noInit: true, handler: true})
	c := ctx(t)
	list, err := r.c.DisturbanceList(c, fun)
	if err != nil || len(list) != 1 || list[0].FAN != 7 {
		t.Fatalf("%v %v", list, err)
	}
	rec, err := r.c.Disturbance(c, fun, 7)
	if err != nil {
		t.Fatal(err)
	}
	want := r.rec.rec
	if rec.NOF != 3 || rec.Interval != 1000 || len(rec.Tags) != len(want.Tags) || len(rec.Channels) != 2 {
		t.Fatalf("%+v", rec)
	}
	for i := range want.Tags {
		if rec.Tags[i] != want.Tags[i] {
			t.Fatalf("tag %d: %+v, want %+v", i, rec.Tags[i], want.Tags[i])
		}
	}
	for i, ch := range rec.Channels {
		if ch.ACC != want.Channels[i].ACC || ch.RFA != 0.5 || len(ch.Values) != 60 || ch.Values[59] != want.Channels[i].Values[59] {
			t.Fatalf("channel %d: %+v", i, ch)
		}
	}
	acks := r.rec.waitAcks(t)
	if len(acks) != 1 || !acks[0] {
		t.Fatalf("acknowledgements %v", acks)
	}
	r.expect(t, "updated list", is(iec103.TypeDisturbanceList, iec103.CauseDisturbanceData))
	if _, err := r.c.Disturbance(c, fun, 99); !errors.Is(err, iec103.ErrAborted) {
		t.Fatal(err)
	}
	if err := r.s.SendDisturbanceList(); err != nil {
		t.Fatal(err)
	}
	r.expect(t, "announced list", is(iec103.TypeDisturbanceList, iec103.CauseDisturbanceData))
}

// TestLossyLine runs the procedures over a line that loses every fifth
// frame of the protection equipment: the link layer repeats (FCB) and
// nothing is executed twice.
func TestLossyLine(t *testing.T) {
	r := start(t, options{dropN: 5, noInit: true, handler: true})
	c := ctx(t)
	for range 3 {
		if _, err := r.c.Interrogate(c); err != nil {
			t.Fatal(err)
		}
		if err := r.c.Command(c, fun, iec103.InfLEDReset, iec103.DPIOn); err != nil {
			t.Fatal(err)
		}
		if _, err := r.c.ReadGroup(c, 1, iec103.KODActualValue); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.c.Disturbance(c, fun, 7); err != nil {
		t.Fatal(err)
	}
	r.dev.mu.Lock()
	gis := r.dev.gis
	r.dev.mu.Unlock()
	if gis != 3 {
		t.Fatalf("%d general interrogations executed", gis)
	}
}

// TestLinkLoss mutes the protection equipment: the client goes down,
// requests fail with ErrLinkDown, and after the line returns the client
// initializes the link again (reset CU) and gets a new identification.
func TestLinkLoss(t *testing.T) {
	var states []iec103.State
	var mu sync.Mutex
	r := start(t, options{noInit: true, handler: true})
	r.expect(t, "identification", is(iec103.TypeIdentification, iec103.CauseResetCU))
	r.line.mute(true)
	deadline := time.Now().Add(5 * time.Second)
	for r.c.State() != iec103.StateDown {
		if time.Now().After(deadline) {
			t.Fatal("client did not notice the loss")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := r.c.Interrogate(ctx(t)); !errors.Is(err, iec103.ErrLinkDown) {
		t.Fatal(err)
	}
	if err := r.c.BroadcastClockSync(time.Now()); !errors.Is(err, iec103.ErrLinkDown) {
		t.Fatal(err)
	}
	r.line.mute(false)
	if err := r.c.WaitUp(ctx(t)); err != nil {
		t.Fatal(err)
	}
	r.expect(t, "identification after recovery", is(iec103.TypeIdentification, iec103.CauseResetCU))
	mu.Lock()
	_ = states
	mu.Unlock()
}

// TestRequestFailsOnLinkLoss checks that a pending request ends with
// ErrLinkDown when the link is lost while it waits.
func TestRequestFailsOnLinkLoss(t *testing.T) {
	r := start(t, options{noInit: true, handler: true})
	done := make(chan error, 1)
	go func() { done <- r.c.Command(ctx(t), fun, 23, iec103.DPIOn) }()
	time.Sleep(100 * time.Millisecond)
	r.line.mute(true)
	select {
	case err := <-done:
		if !errors.Is(err, iec103.ErrLinkDown) {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("request did not fail")
	}
	close(r.dev.block)
}

// TestNoHandler checks the server's defaults without a handler.
func TestNoHandler(t *testing.T) {
	r := start(t, options{noInit: true})
	data, err := r.c.Interrogate(ctx(t))
	if err != nil || len(data) != 0 {
		t.Fatal(data, err)
	}
	if err := r.c.Command(ctx(t), fun, iec103.InfLEDReset, iec103.DPIOn); !errors.Is(err, iec103.ErrNegative) {
		t.Fatal(err)
	}
}

// TestClose checks Close on both sides and requests after it.
func TestClose(t *testing.T) {
	r := start(t, options{noInit: true, handler: true})
	if err := r.c.Close(); err != nil {
		t.Fatal(err)
	}
	if err := r.c.Close(); err != nil {
		t.Fatal(err)
	}
	if err := r.c.WaitUp(ctx(t)); !errors.Is(err, iec103.ErrClosed) {
		t.Fatal(err)
	}
	if _, err := r.c.Interrogate(ctx(t)); err == nil {
		t.Fatal("request after Close")
	}
	_ = r.s.Close()
	if err := r.s.Event(iec103.CauseSpontaneous, fun, 16, iec103.DPIOn); !errors.Is(err, iec103.ErrClosed) {
		t.Fatal(err)
	}
	if r.s.State() != iec103.StateClosed || r.c.State() != iec103.StateClosed {
		t.Fatal("states")
	}
}
