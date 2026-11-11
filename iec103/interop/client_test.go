//go:build interop

// SPDX-License-Identifier: MIT

package interop

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/otfabric/go-iec60870-5/ft12"
	"github.com/otfabric/go-iec60870-5/iec103"
	"github.com/otfabric/go-iec60870-5/iec103/client"
	"github.com/otfabric/go-iec60870-5/link"
)

// wire keeps the ASDUs the reference server sent, as octets: the ones the
// client cannot decode (a structure that is not the standard's) never
// reach its handler, and a known difference is asserted on the octets.
type wire struct {
	mu    sync.Mutex
	asdus [][]byte
}

func (w *wire) frame(t *testing.T) func(bool, ft12.Frame) {
	trace := os.Getenv("IEC103_INTEROP_TRACE") == "1"
	return func(tx bool, f ft12.Frame) {
		if trace {
			t.Logf("client tx=%t %s % X", tx, f, f.Data)
		}
		if tx || f.Kind != ft12.KindVariable {
			return
		}
		w.mu.Lock()
		w.asdus = append(w.asdus, append([]byte{}, f.Data...))
		w.mu.Unlock()
	}
}

// since returns the ASDUs received after the first n, and the count now.
func (w *wire) since(n int) ([][]byte, int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([][]byte{}, w.asdus[n:]...), len(w.asdus)
}

func (w *wire) all() [][]byte { a, _ := w.since(0); return a }

func (w *wire) count() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.asdus)
}

// find returns the first ASDU of the given type and cause (cause 0: any).
func find(asdus [][]byte, typ iec103.TypeID, cause iec103.Cause) []byte {
	for _, a := range asdus {
		if len(a) >= 6 && iec103.TypeID(a[0]) == typ && (cause == 0 || iec103.Cause(a[2]) == cause) {
			return a
		}
	}
	return nil
}

// serverBug returns the entry of known-bugs.tsv for the reference server a
// whose operation pattern matches op, or fails the test: a deviation this
// suite meets without an entry is a failure, not a known difference.
func serverBug(t *testing.T, a adapter, op string) knownBug {
	t.Helper()
	b, ok := knownBugOf(t, a, false, op)
	if !ok {
		t.Fatalf("%s: no known bug of %s %s explains this", op, a.name, a.caps.Upstream.Version)
	}
	return b
}

// This module's client against every reference server (vitusdong is the
// only one): link start-up, identification, clock synchronization, general
// interrogation and general commands. What the reference does as IEC
// 60870-5-103 says must pass; what known-bugs.tsv records as its deviation
// is asserted on the wire and skipped with the bug id, never passed.
func TestClientAgainstReference(t *testing.T) {
	var servers []adapter
	for _, a := range adapters(t) {
		if a.caps.Roles.Server {
			servers = append(servers, a)
		}
	}
	if len(servers) == 0 {
		skipOrFail(t, "no reference server selected")
	}
	for _, a := range servers {
		f := loadFixture(t, a)
		t.Run(a.name, func(t *testing.T) { clientScenario(t, a, f) })
	}
}

func clientScenario(t *testing.T, a adapter, f *fixture) {
	port := startServer(t, a)
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	w := &wire{}
	c, err := client.New(conn, client.Config{
		Link:     link.Config{Timeout: 500 * time.Millisecond, OnFrame: w.frame(t)},
		LinkAddr: 1, PollInterval: 20 * time.Millisecond,
		// The scenario runs the synchronization and the interrogation
		// itself, to check each.
		NoInitTimeSync: true, NoInitGI: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	ca := uint8(f.Station.CommonAddress)

	t.Run("link start-up", func(t *testing.T) {
		if err := c.WaitUp(ctx); err != nil {
			t.Fatalf("request status of link, reset CU: %v", err)
		}
	})
	if c.State() != iec103.StateUp {
		t.Fatal("link down")
	}

	t.Run("identification", func(t *testing.T) {
		// Clause 7.4.1, table 8: ASDU 5, cause 4, INF 3, COL, eight
		// characters and four octets of software after reset CU.
		deadline := time.Now().Add(3 * time.Second)
		for c.Identification() == nil && find(w.all(), iec103.TypeIdentification, iec103.CauseResetCU) == nil && time.Now().Before(deadline) {
			time.Sleep(20 * time.Millisecond)
		}
		if id := c.Identification(); id != nil && id.Cause == iec103.CauseResetCU {
			b := id.Body.(iec103.Identification)
			want := f.Station.Identification
			if int(id.Fun) != want.Fun || id.Inf != iec103.InfResetCU || int(b.COL) != want.Col || b.Manufacturer() != want.ASCII ||
				fmt.Sprintf("%x", b.Software) != want.Software {
				t.Errorf("identification %v %+v, want %+v", id, b, want)
			}
			return
		}
		raw := find(w.all(), iec103.TypeIdentification, iec103.CauseResetCU)
		if raw == nil {
			t.Fatal("no identification (ASDU 5, cause 4) after reset CU")
		}
		if _, err := iec103.Decode(raw); err == nil {
			t.Fatalf("identification % X decodes but the client did not keep it", raw)
		}
		// vitusdong: FUN 255, INF 80, eight characters only (no COL, no
		// software identification): 8 octets after FUN/INF, not 13.
		if raw[4] != byte(iec103.FunGlobal) || len(raw) != 6+8 {
			t.Fatalf("identification % X is not the recorded deviation", raw)
		}
		knownDifference(t, serverBug(t, a, "identification"), fmt.Sprintf("% X", raw))
	})

	t.Run("clock synchronization", func(t *testing.T) {
		// Clause 7.4.2: the addressed ASDU 6 is answered with ASDU 6, cause 8.
		sent := time.Now()
		got, err := c.ClockSync(ctx, sent)
		if err != nil {
			t.Fatal(err)
		}
		if d := got.Sub(sent); d < -time.Minute || d > time.Minute {
			t.Errorf("answer %v for %v", got.Time, sent)
		}
	})

	var gi []*iec103.ASDU
	var giWire [][]byte
	t.Run("general interrogation termination", func(t *testing.T) {
		// Clause 7.4.3: ASDU 8 with the scan number of the ASDU 7 ends it.
		n := w.count()
		gctx, gcancel := context.WithTimeout(ctx, 10*time.Second)
		defer gcancel()
		var err error
		gi, err = c.Interrogate(gctx)
		giWire, _ = w.since(n)
		if err != nil {
			t.Fatalf("no termination (ASDU 8) with the scan number: %v", err)
		}
		if find(giWire, iec103.TypeGITermination, iec103.CauseGITermination) == nil {
			t.Fatal("no ASDU 8, cause 10 on the wire")
		}
	})

	t.Run("general interrogation answers", func(t *testing.T) {
		data := gi
		asdus := giWire
		// Clause 7.4.3: ASDU 1, cause 9, SIN the scan number, one per
		// indication of the fixture that takes part (Interrogate keeps only
		// those with the scan number).
		want := map[funInf]iec103.DPI{}
		for _, ind := range f.Indications {
			if ind.GI == nil || *ind.GI {
				want[funInf{ind.Fun, ind.Inf}] = iec103.DPI(ind.DPI)
			}
		}
		got := map[funInf]iec103.DPI{}
		for _, x := range data {
			if tt, ok := x.Body.(iec103.TimeTagged); ok && x.CommonAddr == ca {
				got[funInf{int(x.Fun), int(x.Inf)}] = tt.DPI
			}
		}
		if len(got) == len(want) {
			for k, v := range want {
				if got[k] != v {
					t.Errorf("FUN %d INF %d: DPI %d, want %d", k.fun, k.inf, got[k], v)
				}
			}
			return
		}
		raw := find(asdus, iec103.TypeTimeTagged, iec103.CauseLocalOperation)
		// vitusdong: cause 11, DPI, four octets FF and a CP56Time2a: 12
		// octets after FUN/INF instead of 6, no SIN.
		if len(data) != 0 || raw == nil || len(raw) != 6+12 {
			t.Fatalf("%d answers %v, want %v; wire %X", len(data), got, want, asdus)
		}
		knownDifference(t, serverBug(t, a, "interrogate"), fmt.Sprintf("% X", raw))
	})

	t.Run("general command", func(t *testing.T) {
		// Clause 7.4.4: ASDU 1, cause 20, the command's FUN and INF, SIN the
		// RII. 160/17 is a command of the fixture.
		n := w.count()
		cctx, ccancel := context.WithTimeout(ctx, 5*time.Second)
		defer ccancel()
		err := c.Command(cctx, 160, 17, iec103.DPIOn)
		if err == nil {
			return
		}
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
		asdus, _ := w.since(n)
		raw := find(asdus, iec103.TypeTimeTagged, 0)
		// vitusdong: ASDU 1 cause 11 with the 12-octet elements, never cause
		// 20 with the RII.
		if raw == nil || raw[2] != byte(iec103.CauseLocalOperation) || raw[4] != 160 || raw[5] != 17 || len(raw) != 6+12 {
			t.Fatalf("no acknowledgement and not the recorded deviation: %v; wire %X", err, asdus)
		}
		knownDifference(t, serverBug(t, a, "command --fun 160 --inf 17"), fmt.Sprintf("% X", raw))
	})

	t.Run("refused command", func(t *testing.T) {
		// Clause 7.4.4: a command that cannot be executed is refused: ASDU
		// 1, cause 21, SIN the RII. 160/18 is a pair of table 17 that is
		// no command of the fixture.
		n := w.count()
		cctx, ccancel := context.WithTimeout(ctx, 5*time.Second)
		defer ccancel()
		err := c.Command(cctx, 160, 18, iec103.DPIOn)
		if errors.Is(err, iec103.ErrNegative) {
			return
		}
		if err == nil {
			t.Fatal("160/18 acknowledged positively")
		}
		asdus, _ := w.since(n)
		raw := find(asdus, iec103.TypeTimeTagged, 0)
		// vitusdong: it takes a command for every indication and answers
		// with ASDU 1 cause 11 and the 12-octet elements, never cause 21.
		if !errors.Is(err, context.DeadlineExceeded) || raw == nil || raw[2] != byte(iec103.CauseLocalOperation) || raw[4] != 160 || raw[5] != 18 || len(raw) != 6+12 {
			t.Fatalf("not refused with cause 21 and not the recorded deviation: %v; wire %X", err, asdus)
		}
		knownDifference(t, serverBug(t, a, "command --fun 160 --inf 18"), fmt.Sprintf("% X", raw))
	})

	// Contract 2.3: the generic services (clause 7.4.8) and the
	// transmission of disturbance data (clause 7.4.7), where the reference
	// server declares them, checked against the fixture.
	t.Run("generic read", func(t *testing.T) {
		if !a.caps.Features["genericServices"] {
			t.Skip("server feature genericServices not declared")
		}
		// Clause 7.4.8.6.2: ASDU 21, INF 244, answered by ASDU 10 with INF
		// 244, cause 42, the RII, the entry's GDD and value.
		for _, g := range f.Generic {
			n := w.count()
			rctx, rcancel := context.WithTimeout(ctx, 5*time.Second)
			res, err := c.ReadEntry(rctx, g.gin(), iec103.KODActualValue)
			rcancel()
			if err == nil && len(res.Sets) == 1 && res.Cause == iec103.CauseGenericReadValid {
				v := res.Sets[0].Value
				want := g.actual()
				if v.Type != want.Type || v.Size != want.Size || v.Number != 1 || fmt.Sprint(normalize(valuesOf(v))) != fmt.Sprint(normalize([]any{g.want()})) {
					t.Errorf("GIN %s: %+v %v, want %+v %v", g.gin(), v.GDD, valuesOf(v), want.GDD, g.want())
				}
				continue
			}
			asdus, _ := w.since(n)
			raw := find(asdus, iec103.TypeGenericData, iec103.CauseGenericReadValid)
			if raw == nil || iec103.InfoNumber(raw[5]) == iec103.InfReadEntry {
				t.Fatalf("GIN %s: %v %+v; wire %X", g.gin(), err, res, asdus)
			}
			knownDifference(t, serverBug(t, a, "generic-read"), fmt.Sprintf("% X", raw))
		}
	})

	t.Run("generic write", func(t *testing.T) {
		if !a.caps.Features["genericServices"] {
			t.Skip("server feature genericServices not declared")
		}
		// Clause 7.4.8.6.3, figure 17: ASDU 10, cause 40, INF 250, answered
		// by ASDU 10, cause 40, INF 250, the RII; 1.2 is writable.
		n := w.count()
		wctx, wcancel := context.WithTimeout(ctx, 5*time.Second)
		defer wcancel()
		res, err := c.GenericWrite(wctx, iec103.InfWriteExecute,
			iec103.DataSet{GIN: iec103.GIN{Group: 1, Entry: 2}, KOD: iec103.KODActualValue, Value: iec103.Float32s(2.25)})
		if err == nil && res.Cause == iec103.CauseGenericWriteAck {
			r, err := c.ReadEntry(wctx, iec103.GIN{Group: 1, Entry: 2}, iec103.KODActualValue)
			if err != nil || len(r.Sets) != 1 {
				t.Fatalf("read after write: %v %+v", err, r)
			}
			if f, _ := r.Sets[0].Value.Float(0); f != 2.25 {
				t.Errorf("read after write: %v", f)
			}
			return
		}
		asdus, _ := w.since(n)
		raw := find(asdus, iec103.TypeGenericData, 0)
		if raw == nil || iec103.InfoNumber(raw[5]) == iec103.InfWriteExecute {
			t.Fatalf("%v %+v; wire %X", err, res, asdus)
		}
		knownDifference(t, serverBug(t, a, "generic-write"), fmt.Sprintf("% X", raw))
	})

	t.Run("disturbance list", func(t *testing.T) {
		if !a.caps.Features["disturbanceList"] {
			t.Skip("server feature disturbanceList not declared")
		}
		// Clause 7.4.7, 7.3.1.11: ASDU 24 with TOO 24 is answered by ASDU
		// 23, cause 31, the function type of the equipment, VSQ SQ = 0 and
		// the number of faults, FAN, SOF and the time of each.
		fun := iec103.FunctionType(f.Station.Identification.Fun)
		n := w.count()
		lctx, lcancel := context.WithTimeout(ctx, 5*time.Second)
		defer lcancel()
		faults, err := c.DisturbanceList(lctx, fun)
		if err == nil {
			got := []faultDoc{}
			for _, x := range faults {
				d := faultDoc{FAN: int(x.FAN), Time: x.Time.UTC().Format("2006-01-02T15:04:05.000Z")}
				d.SOF.TP, d.SOF.TM, d.SOF.Test, d.SOF.OTEV = x.SOF&iec103.SOFTrip != 0, x.SOF&iec103.SOFTM != 0, x.SOF&iec103.SOFTest != 0, x.SOF&iec103.SOFOTEV != 0
				got = append(got, d)
			}
			want := []faultDoc{}
			for _, d := range f.Disturbances {
				x := faultDoc{FAN: d.FAN, Time: d.Time}
				x.SOF.TP, x.SOF.TM, x.SOF.Test, x.SOF.OTEV = d.SOF.TP, d.SOF.TM, d.SOF.Test, d.SOF.OTEV
				want = append(want, x)
			}
			if fmt.Sprint(got) == fmt.Sprint(want) {
				return
			}
			asdus, _ := w.since(n)
			knownDifference(t, serverBug(t, a, "disturbance-list"), fmt.Sprintf("%+v, want %+v; % X", got, want, find(asdus, iec103.TypeDisturbanceList, 0)))
		}
		asdus, _ := w.since(n)
		raw := find(asdus, iec103.TypeDisturbanceList, iec103.CauseDisturbanceData)
		if raw == nil {
			t.Fatalf("no ASDU 23: %v; wire %X", err, asdus)
		}
		knownDifference(t, serverBug(t, a, "disturbance-list"), fmt.Sprintf("%v: % X", err, raw))
	})

	t.Run("disturbance transfer", func(t *testing.T) {
		if !a.caps.Features["disturbanceDataServer"] {
			t.Skip("server feature disturbanceDataServer not declared")
		}
		d := f.Disturbances[0]
		tctx, tcancel := context.WithTimeout(ctx, 20*time.Second)
		defer tcancel()
		rec, err := c.Disturbance(tctx, iec103.FunctionType(f.Station.Identification.Fun), uint16(d.FAN))
		if err != nil {
			t.Fatal(err)
		}
		got := disturbanceDoc{FAN: int(rec.FAN), NOC: len(rec.Channels), Interval: int(rec.Interval), Start: rec.Start.UTC().Format("15:04:05.000")}
		for _, x := range rec.Tags {
			p := int(x.Pos)
			got.Tags = append(got.Tags, tag{Pos: &p, Fun: int(x.Fun), Inf: int(x.Inf), DPI: int(x.DPI)})
		}
		for _, ch := range rec.Channels {
			cd := channelDoc{ACC: int(ch.ACC), RPV: float64(ch.RPV), RSV: float64(ch.RSV), RFA: float64(ch.RFA)}
			for _, v := range ch.Values {
				cd.Values = append(cd.Values, int(v))
			}
			got.NOE = len(ch.Values)
			got.Channels = append(got.Channels, cd)
		}
		if fmt.Sprint(normalize(got)) != fmt.Sprint(normalize(wantDisturbance(d))) {
			t.Errorf("got %+v\nwant %+v", got, wantDisturbance(d))
		}
	})

	t.Run("idle line", func(t *testing.T) {
		time.Sleep(3 * time.Second)
		if c.State() != iec103.StateUp {
			t.Fatal("link down after 3 s of polling")
		}
		if _, err := c.ClockSync(ctx, time.Now()); err != nil {
			t.Fatalf("clock synchronization after the idle time: %v", err)
		}
	})
}
