// SPDX-License-Identifier: MIT

package iec103

import (
	"bytes"
	"errors"
	"reflect"
	"testing"
)

// TestInf73RelTimeMeasurand: INF 73 (fault location, table 12) resolves in
// an ASDU 4, the only ASDU that carries SCL (clauses 7.2.6.20, 7.3.1.4),
// and in the ASDU 2 the table prints.
func TestInf73RelTimeMeasurand(t *testing.T) {
	var c Catalog
	for _, typ := range []TypeID{TypeRelTimeTagged, TypeRelTimeMeasurand} {
		if s, ok := c.Lookup(Monitor, typ, FunDistance, InfFaultLocation); !ok || s.Inf != 73 {
			t.Errorf("type %d: %+v %v", typ, s, ok)
		}
	}
}

// TestGDDRanges: clause 7.2.6.32 gives DATASIZE 1..255 and NUMBER 1..127;
// a description outside is refused on encode and on decode.
func TestGDDRanges(t *testing.T) {
	for _, g := range []GDD{{Type: DataUint, Size: 0, Number: 1}, {Type: DataUint, Size: 1, Number: 0}, {Type: DataNone}} {
		v := Value{GDD: g, Data: make([]byte, g.DataLen())}
		if _, err := New(42, 1, 254, 244, GenericData{Sets: []DataSet{{Value: v}}}).Encode(); !errors.Is(err, ErrInvalid) {
			t.Errorf("encode %+v: %v", g, err)
		}
		b := cat([]byte{10, 0x81, 42, 1, 254, 244, 5, 1, 1, 1, 1}, gdd(g.Type, g.Size, g.Number), v.Data)
		if _, err := Decode(b); !errors.Is(err, ErrInvalid) {
			t.Errorf("decode % X: %v", b, err)
		}
	}
	if _, err := Structure(); !errors.Is(err, ErrInvalid) {
		t.Fatal("empty structure", err)
	}
	if _, err := Structure(Value{GDD: GDD{Type: DataUint, Size: 1}}); !errors.Is(err, ErrInvalid) {
		t.Fatal("structure field with NUMBER 0", err)
	}
}

// TestNDERange: NDE counts 1..63 description elements (clause 7.2.6.35).
func TestNDERange(t *testing.T) {
	if _, err := PackGenericIdent(1, GIN{1, 1}, nil); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := New(42, 1, 254, 243, GenericIdent{GIN: GIN{1, 1}}).Encode(); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := Decode([]byte{11, 0x81, 42, 1, 254, 243, 5, 1, 1, 0}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
}

// TestVSQChecked: clause 7.2.2 and the structures of clause 7.3 fix the
// VSQ: 81H for single information objects, SQ = 0 for ASDUs 3, 9 and 23.
func TestVSQChecked(t *testing.T) {
	for _, b := range [][]byte{
		cat([]byte{1, 0x82, 1, 3, 128, 16, 2}, t32Oct, []byte{0}),
		cat([]byte{1, 0x02, 1, 3, 128, 16, 2}, t32Oct, []byte{0}),
		{8, 0x00, 10, 3, 255, 0, 1},
		{3, 0x81, 2, 3, 128, 144, 0, 0},
		cat([]byte{23, 0x81, 31, 3, 128, 0, 1, 0, 0}, t56Oct),
	} {
		if _, err := Decode(b); !errors.Is(err, ErrInvalid) {
			t.Errorf("% X: %v", b, err)
		}
	}
	if _, err := Decode(cat([]byte{23, 0x01, 31, 3, 128, 0, 1, 0, 0}, t56Oct)); err != nil {
		t.Fatal(err)
	}
	// SQ = 0 with one object (01H) is accepted on receive (ARIEC103 and
	// Wireshark send it); the encoder writes the conformant 81H.
	for _, b := range [][]byte{
		cat([]byte{1, 0x01, 1, 3, 128, 16, 2}, t32Oct, []byte{0}),
		{8, 0x01, 10, 3, 255, 0, 1},
	} {
		a, err := Decode(b)
		if err != nil {
			t.Fatalf("% X: %v", b, err)
		}
		want := bytes.Clone(b)
		want[1] = 0x81
		if back, err := a.Encode(); err != nil || !bytes.Equal(back, want) {
			t.Errorf("% X re-encoded % X %v", b, back, err)
		}
	}
}

// TestCommandAllowed: DCO 0 and 3 are not used (clause 7.2.6.4); the COM
// column of table 17 allows only ON for INF 19 and 23..26; a compatible
// function type takes no other INF of the control direction with ASDU 20
// (clause 7.2.5.2: 20..22 and 27..31 are in no table, 32..239 not used).
func TestCommandAllowed(t *testing.T) {
	cases := []struct {
		fun  FunctionType
		inf  InfoNumber
		dco  DPI
		want bool
	}{
		{FunDistance, 16, DPIOff, true},
		{FunDistance, 16, DPIOn, true},
		{FunDistance, 16, 0, false},
		{FunDistance, 16, 3, false},
		{FunDistance, InfLEDReset, DPIOff, false},
		{FunDistance, InfLEDReset, DPIOn, true},
		{FunDistance, 23, DPIOff, false},
		{FunDistance, 26, DPIOff, false},
		{FunDistance, 27, DPIOff, false},
		{FunDistance, 20, DPIOn, false},
		{FunDistance, 31, DPIOn, false},
		{FunDistance, 15, DPIOn, false},
		{160, 99, DPIOn, false},
		{160, 18, DPIOff, true},
		{160, 23, DPIOn, true}, // FUN in table 17 is typical, not mandatory
		{160, 240, DPIOn, false},
		{17, 99, DPIOn, true},           // private function type
		{17, InfLEDReset, DPIOff, true}, // private function type
		{17, InfLEDReset, 3, false},
	}
	for _, c := range cases {
		if got := CommandAllowed(c.fun, c.inf, c.dco); got != c.want {
			t.Errorf("FUN %d INF %d DCO %d: %v", c.fun, c.inf, c.dco, got)
		}
	}
}

// TestGenericDataTypes encodes one data set of each data type of clause
// 7.2.6.32 not covered by annex B (5, 6, 8 to 12, 15 to 20) with the
// layout the clause gives, and decodes it back.
func TestGenericDataTypes(t *testing.T) {
	tt := append([]byte{2}, t32Oct...)
	cases := []struct {
		name string
		v    Value
		gid  []byte
	}{
		{"5 UF", Uints(DataUFixed, 2, 0x8001), []byte{0x01, 0x80}},
		{"6 F", Value{GDD: GDD{Type: DataFixed, Size: 2, Number: 2}, Data: []byte{0xFF, 0x7F, 0x00, 0x80}}, []byte{0xFF, 0x7F, 0x00, 0x80}},
		{"8 R64.53", Float64s(-2.5), []byte{0, 0, 0, 0, 0, 0, 0x04, 0xC0}},
		{"9 DPI", Uints(DataDPI, 1, uint64(DPIOn)), []byte{2}},
		{"10 SPI", Uints(DataSPI, 1, 1, 0), []byte{1, 0}},
		{"11 DPI with transient and error", Uints(DataDPITransErr, 1, 0, 1, 2, 3), []byte{0, 1, 2, 3}},
		{"12 MEA", Uints(DataMEA, 2, uint64(MEA{Raw: -1, Overflow: true}.word())), []byte{0xF9, 0xFF}},
		{"15 GIN", Value{GDD: GDD{Type: DataGIN, Size: 2, Number: 1}, Data: []byte{3, 1}}, []byte{3, 1}},
		{"16 relative time", Uints(DataRelTime, 2, 1500), []byte{0xDC, 0x05}},
		{"17 FUN and INF", Value{GDD: GDD{Type: DataFunInf, Size: 2, Number: 1}, Data: []byte{128, 68}}, []byte{128, 68}},
		{"18 time-tagged message", Value{GDD: GDD{Type: DataTimeTagged, Size: 6, Number: 1}, Data: append(append([]byte{}, tt...), 9)}, cat(tt, []byte{9})},
		{"19 time-tagged message with relative time", Value{GDD: GDD{Type: DataRelTagged, Size: 10, Number: 1},
			Data: cat([]byte{1, 0x10, 0, 0x07, 0}, t32Oct, []byte{9})}, cat([]byte{1, 0x10, 0, 0x07, 0}, t32Oct, []byte{9})},
		{"20 time-tagged measurand with relative time", Value{GDD: GDD{Type: DataRelMeasure, Size: 12, Number: 1},
			Data: cat(floatLE(1.25), []byte{0x10, 0, 0x07, 0}, t32Oct)}, cat(floatLE(1.25), []byte{0x10, 0, 0x07, 0}, t32Oct)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := New(CauseGenericReadValid, 1, FunGeneric, InfReadEntry, GenericData{RII: 5, Sets: []DataSet{{GIN{3, 1}, KODActualValue, c.v}}})
			got, err := a.Encode()
			if err != nil {
				t.Fatal(err)
			}
			want := cat([]byte{10, 0x81, 42, 1, 254, 244, 5, 1, 3, 1, 1}, gdd(c.v.Type, c.v.Size, c.v.Number), c.gid)
			if !bytes.Equal(got, want) {
				t.Fatalf("encode\n got % X\nwant % X", got, want)
			}
			d, err := Decode(got)
			if err != nil || !reflect.DeepEqual(d, a) {
				t.Fatalf("decode %#v %v", d, err)
			}
		})
	}
	if f, err := Float64s(-2.5).Float(0); err != nil || f != -2.5 {
		t.Fatal(f, err)
	}
	if x, err := Uints(DataRelTime, 2, 1500).Uint(0); err != nil || x != 1500 {
		t.Fatal(x, err)
	}
	if x, err := (Value{GDD: GDD{Type: DataFixed, Size: 2, Number: 2}, Data: []byte{0xFF, 0x7F, 0x00, 0x80}}).Int(1); err != nil || x != -32768 {
		t.Fatal(x, err)
	}
}

// TestTimeRES1: the bit after the minutes is RES1 in CP32Time2a and
// CP56Time2a (clauses 7.2.6.28, 7.2.6.29), not the GEN flag of IEC 101/104:
// ignored on receive, written as 0.
func TestTimeRES1(t *testing.T) {
	gen32 := []byte{0xC5, 0x76, 20 | 0x40, 10}
	a, err := Decode(cat([]byte{1, 0x81, 1, 3, 128, 16, 2}, gen32, []byte{0}))
	if err != nil || a.Body.(TimeTagged).Time.Substituted {
		t.Fatal(a, err)
	}
	gen56 := append([]byte{}, t56Oct...)
	gen56[2] |= 0x40
	a, err = Decode(cat([]byte{6, 0x81, 8, 3, 255, 0}, gen56))
	if err != nil || a.Body.(TimeSync).Time.Substituted {
		t.Fatal(a, err)
	}
	ts := t32
	ts.Substituted = true
	b, err := New(1, 3, 128, 16, TimeTagged{DPI: DPIOn, Time: ts}).Encode()
	if err != nil || !bytes.Equal(b[7:11], t32Oct) {
		t.Fatalf("% X %v", b, err)
	}
	t5 := t56
	t5.Substituted = true
	b, err = New(8, 3, 255, 0, TimeSync{Time: t5}).Encode()
	if err != nil || !bytes.Equal(b[6:], t56Oct) {
		t.Fatalf("% X %v", b, err)
	}
}
