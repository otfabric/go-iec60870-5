//go:build interop

// SPDX-License-Identifier: MIT

package interop

import (
	"context"
	"fmt"
	"net"
	"reflect"
	"slices"
	"strconv"
	"testing"
	"time"
)

// TestReferenceClientAgainstServer runs every client operation of the client
// images (gisce/iec870ree; otfabric when it is selected) (contract 2.2, "IEC
// 102 client") against this module's integrated total station serving the
// baseline fixture, and checks each result document against the fixture.
// The steps share one station, as the contract's stations keep their clock
// across connections, and run in order: set-time changes the clock that the
// last read-time reads back.
func TestReferenceClientAgainstServer(t *testing.T) {
	for _, a := range adapters(t) {
		if a.caps.Roles.Client {
			t.Run(a.name, func(t *testing.T) { clientAgainstServer(t, a) })
		}
	}
}

func clientAgainstServer(t *testing.T, a adapter) {
	f := loadFixture(t, a)
	st := newStation(t, f)
	ln, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	serve(t, ln, st)
	port := ln.Addr().(*net.TCPAddr).Port
	key := strconv.FormatUint(uint64(f.Station.AccessKey), 10)
	ree := []string{"--profile", "ree", "--access-key", key}
	from, to := "2026-01-01T00:00", "2026-01-01T05:00"

	// step runs one operation; check returns an empty string when the
	// document is right, or what is wrong.
	step := func(name string, want int, op []string, check func(r result) string) {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			r, code, err := runClient(ctx, a, port, op)
			if err != nil {
				t.Fatal(err)
			}
			problem := ""
			if code != want {
				problem = fmt.Sprintf("exit %d, want %d (error %s)", code, want, r.code())
			} else if p := check(r); p != "" {
				problem = p
			}
			if problem == "" {
				return
			}
			if knownDifference(t, a, op, problem) {
				return
			}
			t.Errorf("%v: %s\nresult: %+v", op, problem, r)
		})
	}
	// session checks the REE session of a result.
	session := func(r result, login, logout bool) string {
		if r.Session == nil || r.Session.Login != login || r.Session.Logout != logout {
			return fmt.Sprintf("session %+v, want login %t logout %t", r.Session, login, logout)
		}
		return ""
	}
	// confirmed checks the confirmations and the termination of a read.
	confirmed := func(r result, want []confirmation, term bool) string {
		if !slices.Equal(r.Confirmations, want) || r.Terminated != term {
			return fmt.Sprintf("confirmations %v terminated %t, want %v %t", r.Confirmations, r.Terminated, want, term)
		}
		return ""
	}
	positive := []confirmation{{Cot: 7}}
	// periods compares the integrated totals of a result with the fixture's
	// periods of the kind within [lo, hi] and addresses [first, last].
	periods := func(r result, typeID int, kind, lo, hi string, first, last uint8) string {
		type period struct {
			time   string
			totals []total
		}
		var want, got []period
		for _, l := range f.Totals {
			if l.Kind != kind || l.Record != 11 {
				continue
			}
			for _, p := range l.Periods {
				if p.End < lo || p.End > hi {
					continue
				}
				var ts []total
				for _, o := range p.Totals {
					if o.Address >= first && o.Address <= last {
						ts = append(ts, o)
					}
				}
				if len(ts) > 0 {
					want = append(want, period{p.End, ts})
				}
			}
		}
		for _, d := range r.of(typeID) {
			if d.Cot != 5 || d.Record != 11 || d.SU || d.TimeInvalid || d.CommonAddress != f.Station.CommonAddress {
				return fmt.Sprintf("ASDU %+v: want cause 5, record 11, SU and IV 0, common address %d", d, f.Station.CommonAddress)
			}
			var ts []total
			for _, o := range d.Objects {
				ts = append(ts, total{Address: o.Address, Value: o.Value, Quality: o.Quality})
			}
			got = append(got, period{d.Time, ts})
		}
		if !reflect.DeepEqual(got, want) {
			return fmt.Sprintf("periods %+v, want %+v", got, want)
		}
		return ""
	}
	tariffs := func(r result, typeID int, stored bool, lo, hi string) string {
		var want, got []tariffRecord
		for _, tr := range f.Tariffs {
			if tr.Register != 134 {
				continue
			}
			if !stored {
				want = tr.Current
				continue
			}
			for _, rec := range tr.Stored {
				if rec.PeriodEnd >= lo && rec.PeriodEnd <= hi {
					want = append(want, rec)
				}
			}
		}
		for _, d := range r.of(typeID) {
			if d.Cot != 5 || d.Record != 134 || d.Count != 1 {
				return fmt.Sprintf("ASDU %+v: want cause 5, record 134, one record", d)
			}
			for _, o := range d.Objects {
				got = append(got, o.tariffRecord)
			}
		}
		if len(want) == 0 || !reflect.DeepEqual(got, want) {
			return fmt.Sprintf("records %+v, want %+v", got, want)
		}
		return ""
	}
	clock := func(r result, want string) string {
		ts := r.of(72)
		if len(ts) != 1 || ts[0].Cot != 5 || len(ts[0].Objects) != 1 || ts[0].Objects[0].Time != want || ts[0].Objects[0].TimeInvalid {
			return fmt.Sprintf("M_TI_TA_2 %+v, want one with cause 5 and time %s", ts, want)
		}
		return ""
	}
	refused := func(cot int, typeID int) func(r result) string {
		return func(r result) string {
			if r.OK || r.code() != "negative-confirmation" {
				return "error " + r.code() + ", want negative-confirmation"
			}
			if p := confirmed(r, []confirmation{{Cot: cot, Negative: true}}, false); p != "" {
				return p
			}
			if len(r.ASDUs) != 1 || r.ASDUs[0].TypeID != typeID {
				return fmt.Sprintf("ASDUs %+v, want only the refusal", r.ASDUs)
			}
			return ""
		}
	}

	step("link-init-timeout", 3, []string{"read-time", "--link-address", "9", "--timeout-ms", "1500"}, func(r result) string {
		if r.OK || r.Connected || r.code() != "link-init-timeout" {
			return "want link-init-timeout, not connected; got " + r.code()
		}
		return ""
	})
	step("connect", 0, []string{"connect"}, func(r result) string {
		if !r.OK || !r.Connected || len(r.ASDUs) != 0 || r.Session != nil {
			return "want a link and nothing else"
		}
		return ""
	})
	step("connect-ree", 0, append([]string{"connect"}, ree...), func(r result) string {
		if p := session(r, true, true); p != "" {
			return p
		}
		if len(r.ASDUs) != 2 || r.ASDUs[0].TypeID != 183 || r.ASDUs[0].Cot != 7 || r.ASDUs[1].TypeID != 187 || r.ASDUs[1].Cot != 7 {
			return fmt.Sprintf("ASDUs %+v, want C_AC_NA_2 and C_FS_NA_2 confirmed", r.ASDUs)
		}
		return ""
	})
	step("login-refused", 1, []string{"connect", "--profile", "ree", "--access-key", strconv.FormatUint(uint64(f.Station.AccessKey)+1, 10)}, func(r result) string {
		if r.code() != "login-refused" || !r.Connected {
			return "error " + r.code() + ", want login-refused"
		}
		if p := session(r, false, false); p != "" {
			return p
		}
		if acs := r.of(183); len(acs) != 1 || acs[0].Cot != 7 || !acs[0].Negative {
			return fmt.Sprintf("C_AC_NA_2 %+v, want cause 7 with P/N", acs)
		}
		return ""
	})
	step("read-product", 0, []string{"read-product"}, func(r result) string {
		ps := r.of(71)
		p := f.Station.Product
		if len(ps) != 1 || ps[0].Cot != 5 || len(ps[0].Objects) != 1 {
			return fmt.Sprintf("P_MP_NA_2 %+v", ps)
		}
		if o := ps[0].Objects[0]; o.Standard != p.Standard || o.Manufacturer != p.Manufacturer || o.Product != p.Product {
			return fmt.Sprintf("product %+v, want %+v", o, p)
		}
		return ""
	})
	step("read-time", 0, append([]string{"read-time"}, ree...), func(r result) string {
		if p := session(r, true, true); p != "" {
			return p
		}
		return clock(r, f.Station.Clock)
	})
	step("read-totals-absolute", 0, []string{"read-totals", "--from", from, "--to", to}, func(r result) string {
		if p := confirmed(r, positive, true); p != "" {
			return p
		}
		return periods(r, 8, "absolute", from, to, 1, 8)
	})
	step("read-totals-incremental-ree", 0, append([]string{"read-totals", "--kind", "incremental", "--from", from, "--to", to}, ree...), func(r result) string {
		if p := session(r, true, true); p != "" {
			return p
		}
		if p := confirmed(r, positive, true); p != "" {
			return p
		}
		return periods(r, 11, "incremental", from, to, 1, 8)
	})
	step("read-totals-subset", 0, []string{"read-totals", "--kind", "incremental", "--from", "2026-01-01T02:00", "--to", "2026-01-01T03:00", "--first", "1", "--last", "3"}, func(r result) string {
		if p := confirmed(r, positive, true); p != "" {
			return p
		}
		if len(r.of(11)) != 2 {
			return fmt.Sprintf("%d periods, want 2", len(r.of(11)))
		}
		return periods(r, 11, "incremental", "2026-01-01T02:00", "2026-01-01T03:00", 1, 3)
	})
	step("refused-no-period", 1, []string{"read-totals", "--from", "2027-01-01T00:00", "--to", "2027-01-01T05:00"}, refused(18, 122))
	step("refused-unknown-record", 1, []string{"read-totals", "--record", "12", "--from", from, "--to", to}, refused(16, 122))
	step("refused-no-object", 1, []string{"read-totals", "--first", "7", "--last", "8", "--from", from, "--to", to}, refused(17, 122))
	step("refused-common-address", 1, []string{"read-totals", "--common-address", "2", "--from", from, "--to", to}, refused(16, 122))
	step("read-tariff-current", 0, append([]string{"read-tariff"}, ree...), func(r result) string {
		if p := confirmed(r, positive, true); p != "" {
			return p
		}
		return tariffs(r, 135, false, "", "")
	})
	step("read-tariff-stored", 0, append([]string{"read-tariff", "--from", "2025-11-15T00:00", "--to", "2026-01-01T00:00"}, ree...), func(r result) string {
		if p := confirmed(r, positive, true); p != "" {
			return p
		}
		return tariffs(r, 136, true, "2025-11-15T00:00", "2026-01-01T00:00")
	})
	step("read-tariff-refused", 1, append([]string{"read-tariff", "--register", "135"}, ree...), func(r result) string {
		if p := session(r, true, true); p != "" {
			return p
		}
		return confirmed(r, []confirmation{{Cot: 16, Negative: true}}, false)
	})
	const set = "2026-02-03T04:05:06.789"
	step("set-time", 0, append([]string{"set-time", "--time", set}, ree...), func(r result) string {
		if p := confirmed(r, positive, false); p != "" {
			return p
		}
		if cs := r.of(181); len(cs) != 1 || len(cs[0].Objects) != 1 || cs[0].Objects[0].Time != set {
			return fmt.Sprintf("C_CS_TA_2 %+v, want the mirror with %s", cs, set)
		}
		st.mu.Lock()
		defer st.mu.Unlock()
		if n := len(st.sets); n == 0 || fmt.Sprintf("20%02d-%02d-%02dT%02d:%02d:%02d.%03d", st.sets[n-1].Year, st.sets[n-1].Month, st.sets[n-1].Day,
			st.sets[n-1].Hour, st.sets[n-1].Minute, st.sets[n-1].Second, st.sets[n-1].Millisecond) != set || st.sets[n-1].SummerTime {
			return fmt.Sprintf("the station was set to %+v, want %s in standard time", st.sets, set)
		}
		return ""
	})
	step("read-time-after-set", 0, []string{"read-time"}, func(r result) string { return clock(r, set) })
}
