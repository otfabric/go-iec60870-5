// SPDX-License-Identifier: MIT

package iec102_test

import (
	"bytes"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/otfabric/go-iec60870-5/ft12"
	"github.com/otfabric/go-iec60870-5/iec102"
	"github.com/otfabric/go-iec60870-5/iec102/ree"
)

func unhex(t testing.TB, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(strings.ReplaceAll(s, " ", ""))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// header returns the data unit identifier and record address with a
// two-octet common address.
func header(t iec102.TypeID, vsq, cause byte, ca uint16, record byte) []byte {
	return []byte{byte(t), vsq, cause, byte(ca), byte(ca >> 8), record}
}

// roundTrip decodes b, encodes it again and requires the same octets.
func roundTrip(t *testing.T, b []byte, p iec102.Params) *iec102.ASDU {
	t.Helper()
	a, err := iec102.Decode(b, p)
	if err != nil {
		t.Fatalf("decode % x: %v", b, err)
	}
	enc, err := a.Encode(p)
	if err != nil || !bytes.Equal(enc, b) {
		t.Fatalf("re-encoded % x, want % x (%v)", enc, b, err)
	}
	return a
}

// gisce/iec870ree (AGPL-3.0, commit 993242daa3e998c9d86948881d97835438d8bef3)
// test vectors, used as observed protocol data only: tests/test_base_asdu.py,
// tests/test_protocol.py and tests/test_app_asdu.py.

func TestGisceFrames(t *testing.T) {
	for _, tc := range []struct {
		hex  string
		kind ft12.Kind
		ctrl byte
		addr uint16
	}{
		{"10 49 0c 87 DC 16", ft12.KindFixed, 0x49, 34572}, // request status of link
		{"10 00 0c 87 93 16", ft12.KindFixed, 0x00, 34572}, // ACK
		{"10 0b 95 d1 71 16", ft12.KindFixed, 0x0B, 53653}, // status of link
		{"10 00 95 d1 66 16", ft12.KindFixed, 0x00, 53653},
		{"68 0d 0d 68 08 95 d1 b7 01 07 01 00 00 01 00 00 00 2f 16", ft12.KindVariable, 0x08, 53653},
		{"68 0D 0D 68 73 0C 87 B7 01 06 01 00 00 07 00 00 00 CC 16", ft12.KindVariable, 0x73, 34572},
		{"68 0d 0d 68 73 0c 87 b7 01 06 01 00 00 08 00 00 00 cd 16", ft12.KindVariable, 0x73, 34572},
	} {
		raw := unhex(t, tc.hex)
		f, n, err := ft12.Decode(raw, fp2)
		if err != nil || n != len(raw) || f.Kind != tc.kind || f.Control != tc.ctrl || f.Addr != tc.addr {
			t.Fatalf("%s: %v %v", tc.hex, f, err)
		}
		if f.Kind != ft12.KindVariable {
			continue
		}
		a := roundTrip(t, f.Data, iec102.Params{Profile: ree.Profile})
		k, ok := a.Body.(ree.AccessKey)
		if !ok || a.CommonAddr != 1 || a.Record != 0 || a.VSQ != 1 {
			t.Fatalf("%s: %v %#v", tc.hex, a, a.Body)
		}
		if want := uint32(f.Data[6]); k.Key != want {
			t.Fatalf("key %d, want %d", k.Key, want)
		}
	}
}

func TestGisceTimes(t *testing.T) {
	for _, tc := range []struct {
		hex  string
		want time.Time
	}{
		{"01 00 12 09 09", time.Date(2009, 9, 18, 0, 1, 0, 0, time.UTC)},
		{"00 0b 07 02 0a", time.Date(2010, 2, 7, 11, 0, 0, 0, time.UTC)},
		{"00 81 e1 07 12", time.Date(2018, 7, 1, 1, 0, 0, 0, time.UTC)},
		{"00 80 61 08 12", time.Date(2018, 8, 1, 0, 0, 0, 0, time.UTC)},
	} {
		ta, err := iec102.ParseTimeA(unhex(t, tc.hex))
		if err != nil {
			t.Fatal(err)
		}
		got, ok := ta.Time(nil)
		if !ok || !got.Equal(tc.want) || !bytes.Equal(ta.AppendTo(nil), unhex(t, tc.hex)) {
			t.Fatalf("%s: %v %v", tc.hex, got, ok)
		}
	}
	empty, _ := iec102.ParseTimeA(make([]byte, 5))
	if _, ok := empty.Time(nil); ok {
		t.Fatal("all-zero time a is a valid date")
	}
	for _, tc := range []struct {
		hex  string
		want time.Time
	}{
		{"00 90 25 8b 7c 08 13", time.Date(2019, 8, 28, 11, 37, 36, 0, time.UTC)},
		{"0a 2a 10 0b e7 02 0a", time.Date(2010, 2, 7, 11, 16, 10, 522e6, time.UTC)},
		{"00 64 2a 44 12 08 12", time.Date(2018, 8, 18, 4, 42, 25, 0, time.UTC)},
	} {
		tb, err := iec102.ParseTimeB(unhex(t, tc.hex))
		if err != nil {
			t.Fatal(err)
		}
		got, ok := tb.Time(nil)
		if !ok || !got.Equal(tc.want) || !bytes.Equal(tb.AppendTo(nil), unhex(t, tc.hex)) {
			t.Fatalf("%s: %v %v", tc.hex, got, ok)
		}
	}
	if _, err := iec102.ParseTimeA(nil); !errors.Is(err, iec102.ErrShort) {
		t.Fatal(err)
	}
	if _, err := iec102.ParseTimeB(make([]byte, 6)); !errors.Is(err, iec102.ErrShort) {
		t.Fatal(err)
	}
}

func TestGisceBodies(t *testing.T) {
	p := iec102.Params{Profile: ree.Profile}
	// P_MP_NA_2: manufacturer 251, product 608343970.
	a := roundTrip(t, append(header(iec102.P_MP_NA_2, 1, 5, 1, 0), unhex(t, "04 fb a2 97 42 24")...), p)
	if b := a.Body.(iec102.ProductSpec); b.Manufacturer != 251 || b.Product != 608343970 || b.Standard != 4 {
		t.Fatalf("%+v", b)
	}
	// M_TI_TA_2 and C_CS_TA_2: 2018-08-18 04:42:25.
	for _, typ := range []iec102.TypeID{iec102.M_TI_TA_2, ree.C_CS_TA_2} {
		a = roundTrip(t, append(header(typ, 1, 5, 1, 0), unhex(t, "00 64 2a 44 12 08 12")...), p)
		var tb iec102.TimeB
		switch b := a.Body.(type) {
		case iec102.SystemTime:
			tb = b.Time
		case ree.SetClock:
			tb = b.Time
		}
		if tb.String() != "2018-08-18 04:42:25.000" {
			t.Fatalf("%v %v", a, tb)
		}
	}
	// C_CI_NU_2 and C_CI_NT_2: addresses 1..8, 2018-07-01 01:00 .. 2018-08-01 00:00.
	for _, typ := range []iec102.TypeID{iec102.C_CI_NT_2, iec102.C_CI_NU_2} {
		a = roundTrip(t, append(header(typ, 1, 6, 1, 11), unhex(t, "01 08 00 81 e1 07 12 00 80 61 08 12")...), p)
		q := a.Body.(iec102.Read)
		if q.First != 1 || q.Last != 8 || q.From.String() != "2018-07-01 01:00 SU" || q.To.String() != "2018-08-01 00:00 SU" {
			t.Fatalf("%+v", q)
		}
	}
	// M_IT_TK_2 with 8 totals; 7 and 8 invalid (quality 80h).
	a = roundTrip(t, append(header(iec102.M_IT_TK_2, 8, 5, 1, 11), unhex(t,
		"01 04 00 00 00 00 02 00 00 00 00 00 03 1b 00 00 00 00 04 00 00 00 00 00 05 00 00 00 00 00 06 00 00 00 00 00 07 00 00 00 00 80 08 00 00 00 00 80 00 81 e1 07 12")...), p)
	it := a.Body.(iec102.IntegratedTotals)
	if len(it.Totals) != 8 || it.Totals[0].Value != 4 || it.Totals[2].Value != 27 || !it.Totals[6].Quality.IV() || it.Totals[5].Quality.IV() {
		t.Fatalf("%+v", it)
	}
	// C_TA_VM_2 and M_TA_VM_2 (one tariff record).
	a = roundTrip(t, append(header(ree.C_TA_VM_2, 1, 6, 1, 134), unhex(t, "00 01 e1 07 12 00 00 61 08 12")...), p)
	if q := a.Body.(ree.Request); q.From.String() != "2018-07-01 01:00" || q.To.String() != "2018-08-01 00:00" {
		t.Fatalf("%+v", q)
	}
	a = roundTrip(t, append(header(ree.M_TA_VM_2, 1, 5, 1, 134), unhex(t,
		"14 74 37 3b 00 c0 6e 00 00 00 fb 59 1c 00 ba 5c 00 00 00 e4 bd 05 00 db 06 00 00 00 00 00 00 00 80 00 00 00 00 80 65 01 00 00 0f 87 f2 07 12 00 00 00 00 00 80 00 80 e1 07 12 00 80 61 08 12")...), p)
	r := a.Body.(ree.TariffInfo).Records[0]
	if r.Addr != 20 || r.ActiveAbs != 3880820 || r.ActiveInc != 28352 || r.ReactiveIndAbs != 1858043 || r.ReactiveCapInc != 1755 ||
		r.Reserved7Qual != 128 || r.MaxPower != 357 || r.MaxPowerTime.String() != "2018-07-18 07:15 SU" || r.ExcessPowerQual != 128 ||
		r.Start.String() != "2018-07-01 00:00 SU" || r.End.String() != "2018-08-01 00:00 SU" {
		t.Fatalf("%+v", r)
	}
}
