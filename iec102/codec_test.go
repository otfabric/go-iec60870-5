// SPDX-License-Identifier: MIT

package iec102_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/otfabric/go-iec60870-5/iec102"
	"github.com/otfabric/go-iec60870-5/iec102/ree"
)

var ta = iec102.TimeA{Minute: 15, Hour: 13, Day: 2, Weekday: 2, Month: 3, Year: 21, TIS: true, ETI: 2, PTI: 1}

// TestBeckhoffLayouts encodes each body and compares with the octets
// written down from the layouts of the Beckhoff TS650x manual (sections
// 4.22.6 to 4.22.29 and 4.25.1 to 4.25.13).
func TestBeckhoffLayouts(t *testing.T) {
	taOctets := []byte{0x4F, 0x0D, 0x42, 0x63, 21}
	tb := iec102.TimeB{Millisecond: 500, Second: 30, TimeA: ta}
	tbOctets := append([]byte{0xF4, 0x79}, taOctets...) // 500 | 30<<10 = 0x79F4
	cases := []struct {
		name string
		body iec102.Body
		vsq  byte
		obj  []byte
	}{
		{"C_RD_NA_2", iec102.Read{T: iec102.C_RD_NA_2}, 0, nil},
		{"C_SP_NA_2", iec102.Read{T: iec102.C_SP_NA_2}, 0, nil},
		{"C_SP_NB_2", iec102.Read{T: iec102.C_SP_NB_2, From: ta, To: ta}, 1, append(append([]byte{}, taOctets...), taOctets...)},
		{"C_TI_NA_2", iec102.Read{T: iec102.C_TI_NA_2}, 0, nil},
		{"C_CI_NA_2", iec102.Read{T: iec102.C_CI_NA_2, First: 9, Last: 9, From: ta}, 0, nil},
		{"C_CI_NF_2", iec102.Read{T: iec102.C_CI_NF_2, First: 1, Last: 6}, 1, []byte{1, 6}},
		{"C_CI_NL_2", iec102.Read{T: iec102.C_CI_NL_2, From: ta}, 1, taOctets},
		{"C_CI_NQ_2", iec102.Read{T: iec102.C_CI_NQ_2, First: 2, Last: 3, From: ta}, 1, append([]byte{2, 3}, taOctets...)},
		{"C_CI_NR_2", iec102.Read{T: iec102.C_CI_NR_2, First: 1, Last: 8, From: ta, To: ta}, 1, append(append([]byte{1, 8}, taOctets...), taOctets...)},
		{"M_IT_TA_2", iec102.IntegratedTotals{T: iec102.M_IT_TA_2, Totals: []iec102.IntegratedTotal{{Addr: 1, Value: 0x01020304, Quality: iec102.QualityOf(5, iec102.QualityCarry)}}, Time: ta}, 1,
			append([]byte{1, 4, 3, 2, 1, 0x25}, taOctets...)},
		{"M_IT_TE_2 signed", iec102.IntegratedTotals{T: iec102.M_IT_TE_2, Signed: true, Totals: []iec102.IntegratedTotal{{Addr: 2, Value: 0x030201, Quality: iec102.QualityInvalid, Signature: 0xAA}}, Time: ta}, 1,
			append([]byte{2, 1, 2, 3, 0x80, 0xAA}, taOctets...)},
		{"M_IT_TM_2", iec102.IntegratedTotals{T: iec102.M_IT_TM_2, Totals: []iec102.IntegratedTotal{{Addr: 3, Value: 0x0201}, {Addr: 4, Value: 7, Quality: iec102.QualityAdjusted}}, Time: ta}, 2,
			append([]byte{3, 1, 2, 0, 4, 7, 0, 0x40}, taOctets...)},
		{"M_SP_TA_2", iec102.SinglePoints{Points: []iec102.SinglePoint{{Addr: 7, On: true, Qualifier: 3, Time: tb}}}, 1, append([]byte{7, 0x07}, tbOctets...)},
		{"P_MP_NA_2", iec102.ProductSpec{Standard: 0x14, Manufacturer: 9, Product: 0x0A0B0C0D}, 1, []byte{0x14, 9, 0x0D, 0x0C, 0x0B, 0x0A}},
		{"M_TI_TA_2", iec102.SystemTime{Time: tb}, 1, tbOctets},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := iec102.New(iec102.CauseActivation, 0x0102, 11, tc.body)
			got, err := a.Encode(iec102.Params{})
			if err != nil {
				t.Fatal(err)
			}
			want := append([]byte{byte(tc.body.Type()), tc.vsq, 6, 2, 1, 11}, tc.obj...)
			if !bytes.Equal(got, want) {
				t.Fatalf("% x, want % x", got, want)
			}
			back := roundTrip(t, got, iec102.Params{})
			if back.Type != tc.body.Type() || back.CommonAddr != 0x0102 || back.Record != 11 {
				t.Fatalf("%v", back)
			}
		})
	}
}

func TestQualityAndCause(t *testing.T) {
	q := iec102.QualityOf(0x3F, 0xFF)
	if !q.IV() || !q.CA() || !q.CY() || q.Seq() != 31 {
		t.Fatalf("%08b", q)
	}
	c := iec102.CauseActivationCon | iec102.CauseNegative | iec102.CauseTest
	if c.Code() != 7 || !c.Negative() || !c.Test() || !c.Refusal() || c.String() != "<7> activation confirmation negative test" {
		t.Fatal(c.String())
	}
	if !iec102.CauseNoPeriod.Refusal() || iec102.CauseActivationTerm.Refusal() || iec102.Cause(40).String() != "<40>" {
		t.Fatal("refusal")
	}
	if iec102.VSQ(0x85).Number() != 5 || !iec102.VSQ(0x85).SQ() {
		t.Fatal("vsq")
	}
}

func TestTypeTables(t *testing.T) {
	for typ := iec102.TypeID(0); ; typ++ {
		w, ok := typ.IntegratedTotalsType()
		if ok != (typ >= 2 && typ <= 13) || (ok && w != []int{4, 3, 2}[(typ-2)%3]) {
			t.Fatalf("%v: %d %v", typ, w, ok)
		}
		if typ.Standard() != (typ.String() != "TypeID("+itoa(int(typ))+")") {
			t.Fatalf("%v", typ)
		}
		if typ.Private() != (typ >= 128) {
			t.Fatalf("%v private", typ)
		}
		if typ == 255 {
			break
		}
	}
	fam := map[iec102.TypeID]iec102.Family{
		iec102.M_IT_TA_2: iec102.FamilyAccounting, iec102.M_IT_TF_2: iec102.FamilyResetAccounting,
		iec102.M_IT_TH_2: iec102.FamilyOperational, iec102.M_IT_TM_2: iec102.FamilyResetOperational,
		iec102.C_CI_ND_2: iec102.FamilyAccounting, iec102.C_CI_NE_2: iec102.FamilyResetAccounting,
		iec102.C_CI_NM_2: iec102.FamilyOperational, iec102.C_CI_NQ_2: iec102.FamilyResetOperational,
		iec102.C_CI_NR_2: iec102.FamilyAccounting, iec102.C_CI_NS_2: iec102.FamilyResetAccounting,
		iec102.C_CI_NT_2: iec102.FamilyOperational, iec102.C_CI_NU_2: iec102.FamilyResetOperational,
		iec102.C_SP_NA_2: iec102.FamilyNone,
	}
	for typ, f := range fam {
		if typ.Family() != f {
			t.Fatalf("%v: %v, want %v", typ, typ.Family(), f)
		}
	}
	names := map[iec102.Family]string{iec102.FamilyNone: "none", iec102.FamilyAccounting: "accounting",
		iec102.FamilyResetAccounting: "periodical reset accounting", iec102.FamilyOperational: "operational",
		iec102.FamilyResetOperational: "periodical reset operational"}
	for f, n := range names {
		if f.String() != n {
			t.Fatal(f.String())
		}
	}
	sel := map[iec102.TypeID]iec102.Selection{
		iec102.C_CI_NI_2: iec102.SelectOldest, iec102.C_CI_NK_2: iec102.SelectOldestRange,
		iec102.C_CI_NL_2: iec102.SelectPeriod, iec102.C_CI_NM_2: iec102.SelectPeriodRange,
		iec102.C_CI_NS_2: iec102.SelectTimeRange, iec102.C_RD_NA_2: iec102.SelectNone,
	}
	for typ, s := range sel {
		if typ.Selection() != s {
			t.Fatalf("%v: %v", typ, typ.Selection())
		}
	}
}

func TestTimeConversion(t *testing.T) {
	madrid, err := time.LoadLocation("Europe/Madrid")
	if err != nil {
		t.Skip("no time zone database")
	}
	// 2019-10-27 02:30 happens twice in Madrid; SU selects which.
	summer := time.Date(2019, 10, 27, 0, 30, 0, 0, time.UTC)
	winter := summer.Add(time.Hour)
	for _, want := range []time.Time{summer, winter} {
		a := iec102.TimeAOf(want, madrid)
		if a.Hour != 2 || a.Minute != 30 || a.SummerTime != want.Equal(summer) || a.Weekday != 7 {
			t.Fatalf("%+v", a)
		}
		got, ok := a.Time(madrid)
		if !ok || !got.Equal(want) {
			t.Fatalf("%v, want %v", got, want)
		}
	}
	b := iec102.TimeBOf(time.Date(2017, 11, 29, 23, 58, 59, 978e6, time.UTC), nil)
	if b.String() != "2017-11-29 23:58:59.978" {
		t.Fatal(b.String())
	}
	if got, ok := b.Time(nil); !ok || got.Nanosecond() != 978e6 {
		t.Fatal(got)
	}
	if a := iec102.TimeAOf(time.Date(1999, 1, 1, 0, 0, 0, 0, time.UTC), nil); !a.Invalid || !strings.HasSuffix(a.String(), " IV") {
		t.Fatalf("%v", a)
	}
	flags := iec102.TimeA{Year: 99, Month: 12, Day: 31, TIS: true, Res1: 3, Res2: true}
	back, _ := iec102.ParseTimeA(flags.AppendTo(nil))
	if back != flags || !strings.HasSuffix(flags.String(), " TIS") {
		t.Fatalf("%+v", back)
	}
	if _, ok := (iec102.TimeA{Year: 23, Month: 2, Day: 30}).Time(nil); ok {
		t.Fatal("30 February")
	}
}

func TestDecodeErrors(t *testing.T) {
	p := iec102.Params{}
	for _, tc := range []struct {
		name string
		b    []byte
		err  error
	}{
		{"short header", []byte{8, 1, 5, 1, 0}, iec102.ErrShort},
		{"too long", make([]byte, iec102.MaxSize+1), iec102.ErrTooLong},
		{"totals length", append(header(iec102.M_IT_TG_2, 2, 5, 1, 11), make([]byte, 9)...), iec102.ErrInvalid},
		{"single points short", append(header(iec102.M_SP_TA_2, 1, 5, 1, 50), 1, 1), iec102.ErrShort},
		{"read trailing", append(header(iec102.C_CI_NB_2, 1, 6, 1, 11), 1, 2, 3), iec102.ErrTrailing},
		{"product short", append(header(iec102.P_MP_NA_2, 1, 5, 1, 0), 1, 2), iec102.ErrShort},
		{"time short", append(header(iec102.M_TI_TA_2, 1, 5, 1, 0), 1, 2), iec102.ErrShort},
		{"REE key short", append(header(ree.C_AC_NA_2, 1, 6, 1, 0), 1), iec102.ErrShort},
		{"REE tariff length", append(header(ree.M_TA_VC_2, 1, 5, 1, 134), 1), iec102.ErrInvalid},
	} {
		_, err := iec102.Decode(tc.b, iec102.Params{Profile: ree.Profile})
		if !errors.Is(err, tc.err) {
			t.Errorf("%s: %v, want %v", tc.name, err, tc.err)
		}
	}
	if _, err := iec102.Decode(make([]byte, 8), iec102.Params{CommonAddrSize: 3}); !errors.Is(err, iec102.ErrInvalid) {
		t.Fatal(err)
	}
	// A refusal whose objects do not fit the type decodes as Raw.
	a := roundTrip(t, append(header(iec102.C_CI_NU_2, 1, byte(iec102.CauseNoPeriod), 1, 11), 1), p)
	if _, ok := a.Body.(iec102.Raw); !ok || !a.Cause.Refusal() {
		t.Fatalf("%#v", a.Body)
	}
	// Lists with SQ set, M_EI_NA_2 and reserved types stay raw.
	for _, b := range [][]byte{
		append(header(iec102.M_IT_TG_2, 0x81, 5, 1, 11), 1, 2, 3),
		append(header(iec102.M_SP_TA_2, 0x81, 5, 1, 11), 1),
		append(header(iec102.M_EI_NA_2, 1, 4, 1, 0), 0),
		append(header(124, 1, 5, 1, 0), 9, 9),
	} {
		if a := roundTrip(t, b, p); a.Body.Type() != 0 {
			t.Fatalf("%v %#v", a, a.Body)
		}
	}
}

func TestEncodeErrors(t *testing.T) {
	p := iec102.Params{}
	for _, tc := range []struct {
		name string
		a    *iec102.ASDU
		p    iec102.Params
	}{
		{"no body", &iec102.ASDU{}, p},
		{"bad params", iec102.New(5, 1, 0, iec102.Read{T: iec102.C_RD_NA_2}), iec102.Params{CommonAddrSize: 4}},
		{"ca too large", iec102.New(5, 256, 0, iec102.Read{T: iec102.C_RD_NA_2}), iec102.Params{CommonAddrSize: 1}},
		{"not a read", iec102.New(5, 1, 0, iec102.Read{T: iec102.M_IT_TA_2}), p},
		{"not totals", iec102.New(5, 1, 0, iec102.IntegratedTotals{T: iec102.C_RD_NA_2}), p},
		{"value too wide", iec102.New(5, 1, 0, iec102.IntegratedTotals{T: iec102.M_IT_TC_2, Totals: []iec102.IntegratedTotal{{Value: 1 << 16}}}), p},
		{"too many", iec102.New(5, 1, 0, iec102.IntegratedTotals{T: iec102.M_IT_TC_2, Totals: make([]iec102.IntegratedTotal, 128)}), p},
		{"too long", iec102.New(5, 1, 0, iec102.SinglePoints{Points: make([]iec102.SinglePoint, 30)}), p},
		{"REE unknown request", iec102.New(6, 1, 0, ree.Request{T: ree.C_PK_2}), p},
	} {
		if _, err := tc.a.Encode(tc.p); err == nil {
			t.Errorf("%s: no error", tc.name)
		}
	}
	// One-octet common address.
	a := iec102.New(iec102.CauseRequest, 7, 0, iec102.Read{T: iec102.C_TI_NA_2})
	b, err := a.Encode(iec102.Params{CommonAddrSize: 1})
	if err != nil || !bytes.Equal(b, []byte{103, 0, 5, 7, 0}) {
		t.Fatalf("% x %v", b, err)
	}
	if a.String() != "C_TI_NA_2 <5> request CA=7 record=0" {
		t.Fatal(a.String())
	}
	if iec102.TypeID(200).String() != "TypeID(200)" || ree.Name(ree.C_CB_UN_2) != "C_CB_UN_2" || ree.Name(iec102.M_SP_TA_2) != "M_SP_TA_2" {
		t.Fatal("names")
	}
}

func TestReader(t *testing.T) {
	r := iec102.NewReader([]byte{1, 2, 3})
	if r.U32(3) != 0x030201 || r.Len() != 0 || r.Done() != nil {
		t.Fatal("u32")
	}
	r.U8()
	if !errors.Is(r.Done(), iec102.ErrShort) || r.U8() != 0 {
		t.Fatal(r.Err)
	}
}

func TestRefusedError(t *testing.T) {
	a := iec102.New(iec102.CauseNoPeriod, 1, 11, iec102.Read{T: iec102.C_CI_NU_2})
	err := error(&iec102.RefusedError{ASDU: a})
	var re *iec102.RefusedError
	if !errors.Is(err, iec102.ErrNegative) || !errors.As(err, &re) || re.Cause() != iec102.CauseNoPeriod ||
		!strings.Contains(err.Error(), "integration period not available") {
		t.Fatal(err)
	}
	for s, want := range map[iec102.State]string{iec102.StateDown: "down", iec102.StateUp: "up", iec102.StateClosed: "closed", 9: "unknown"} {
		if s.String() != want {
			t.Fatal(s.String())
		}
	}
	for size, want := range map[int]int{0: 2, 1: 1, 2: 2, iec102.NoLinkAddr: 0} {
		fp, err := iec102.LinkParams(size)
		if err != nil || fp.AddrSize != want {
			t.Fatal(size, err)
		}
	}
	if _, err := iec102.LinkParams(3); !errors.Is(err, iec102.ErrInvalidConfig) {
		t.Fatal(err)
	}
}

// TestREEBodies round-trips the REE bodies that the vectors above leave
// out, and the REE quality bits.
func TestREEBodies(t *testing.T) {
	p := iec102.Params{Profile: ree.Profile}
	for _, body := range []iec102.Body{
		ree.Request{T: ree.C_FS_NA_2}, ree.Request{T: ree.C_TA_VC_2}, ree.Request{T: ree.C_RM_NA_2},
		ree.Request{T: ree.C_PC_NA_2}, ree.Request{T: ree.C_DF_NA_2},
		ree.Request{T: ree.C_CB_UN_2, Object: 10, From: ta, To: ta}, ree.Request{T: ree.C_CB_NT_2, Object: 9, From: ta, To: ta},
		ree.ContractedPowers{Powers: []ree.ContractedPower{{Addr: 1, Power: 450}, {Addr: 2, Power: 450}}, Time: ta},
		ree.SpecialDays{Days: []iec102.TimeA{ta, ta}},
		ree.TariffInfo{T: ree.M_TA_VC_2, Records: []ree.TariffRecord{{Addr: 20, MaxPower: 1}}},
	} {
		b, err := iec102.New(5, 1, 134, body).Encode(p)
		if err != nil {
			t.Fatal(err)
		}
		a := roundTrip(t, b, p)
		if a.Body.Type() != body.Type() {
			t.Fatalf("%v: %#v", a, a.Body)
		}
	}
	q := ree.Quality(ree.QualityIV | ree.QualityVH | ree.QualityAL)
	if !q.Has(ree.QualityIV|ree.QualityAL) || q.Has(ree.QualityMP) {
		t.Fatal("quality")
	}
}

// FuzzDecode decodes arbitrary octets with and without the REE profile;
// whatever decodes must encode back to the same octets.
func FuzzDecode(f *testing.F) {
	f.Add(append(header(iec102.M_IT_TK_2, 1, 5, 1, 11), 1, 0x4f, 0, 0, 0, 0, 0, 0x81, 0xe1, 5, 0x10))
	f.Add(append(header(iec102.C_CI_NU_2, 1, 7, 1, 11), 1, 1, 0, 0, 1, 5, 0x10, 0, 0, 1, 6, 0x10))
	f.Add(append(header(ree.C_AC_NA_2, 1, 7, 1, 0), 1, 0, 0, 0))
	f.Add(append(header(iec102.M_SP_TA_2, 1, 5, 1, 52), 1, 1, 0, 0x90, 0x25, 0x8b, 0x7c, 8, 0x13))
	f.Add(append(header(ree.M_PC_NA_2, 1, 5, 1, 134), 1, 1, 0, 0, 0, 0, 0x81, 0xe1, 7, 0x12))
	f.Fuzz(func(t *testing.T, b []byte) {
		for _, p := range []iec102.Params{{}, {Profile: ree.Profile}, {CommonAddrSize: 1}} {
			a, err := iec102.Decode(b, p)
			if err != nil {
				continue
			}
			enc, err := a.Encode(p)
			if err != nil {
				t.Fatalf("% x decoded to %v but does not encode: %v", b, a, err)
			}
			if !bytes.Equal(enc, b) {
				t.Fatalf("% x re-encoded as % x", b, enc)
			}
		}
	})
}
