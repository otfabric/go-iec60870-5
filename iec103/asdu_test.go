// SPDX-License-Identifier: MIT

package iec103

import (
	"bytes"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/otfabric/go-iec60870-5/cp"
)

func cat(parts ...[]byte) []byte { return bytes.Join(parts, nil) }

func ascii(s string) []byte { return []byte(s) }

// cp32 is 10:20:30.405 with IV clear: ms 30405 = 0x76C5, minute 20, hour 10.
var (
	t32     = cp.Timestamp{Time: time.Date(1, 1, 1, 10, 20, 30, 405e6, time.UTC)}
	t32Oct  = []byte{0xC5, 0x76, 20, 10}
	t56     = cp.Timestamp{Time: time.Date(1995, 1, 1, 12, 5, 1, 999e6, time.UTC)}
	t56Oct  = []byte{0xCF, 0x07, 5, 12, 1 | 7<<5, 1, 95} // 1995-01-01 is a Sunday: day of week 7
	gdd     = func(t DataType, size, n uint8) []byte { return []byte{byte(t), size, n} }
	floatLE = func(f float32) []byte { return appendF32(nil, f) }
)

// TestCompatibleTypes encodes one ASDU of every type of tables 3 and 4 and
// compares the octets with the structures of clause 7.3.
func TestCompatibleTypes(t *testing.T) {
	cases := []struct {
		name string
		a    *ASDU
		want []byte
	}{
		{"7.3.1.1 ASDU 1", New(CauseSpontaneous, 3, FunDistance, 16, TimeTagged{DPI: DPIOn, Time: t32, SIN: 0}),
			cat([]byte{1, 0x81, 1, 3, 128, 16, 2}, t32Oct, []byte{0})},
		{"7.3.1.2 ASDU 2", New(CauseSpontaneous, 3, FunDistance, 64, RelTimeTagged{DPI: DPIOn, RET: 0x0102, FAN: 0x0304, Time: t32, SIN: 9}),
			cat([]byte{2, 0x81, 1, 3, 128, 64, 2, 2, 1, 4, 3}, t32Oct, []byte{9})},
		{"7.3.1.3 ASDU 3.2", New(CauseCyclic, 3, FunOvercurrent, 145, Measurands{Values: []MEA{{Raw: 1}, {Raw: -1, Error: true}}}),
			[]byte{3, 0x02, 2, 3, 160, 145, 0x08, 0x00, 0xFA, 0xFF}},
		{"7.3.1.4 ASDU 4", New(CauseSpontaneous, 3, FunDistance, 73, RelTimeMeasurand{SCL: 1.5, RET: 7, FAN: 8, Time: t32}),
			cat([]byte{4, 0x81, 1, 3, 128, 73}, floatLE(1.5), []byte{7, 0, 8, 0}, t32Oct)},
		{"7.3.1.5 ASDU 5", New(CauseResetCU, 3, FunDistance, InfResetCU, IdentificationOf(COLWithGeneric, "ACME", [4]byte{1, 2, 3, 4})),
			cat([]byte{5, 0x81, 4, 3, 128, 3, 3}, ascii("ACME    "), []byte{1, 2, 3, 4})},
		{"7.3.1.6 ASDU 6", New(CauseTimeSync, 3, FunGlobal, InfTimeSync, TimeSync{Time: t56}),
			cat([]byte{6, 0x81, 8, 3, 255, 0}, t56Oct)},
		{"7.3.2.2 ASDU 7", New(CauseGI, 3, FunGlobal, InfGIEnd, GIInit{SCN: 42}),
			[]byte{7, 0x81, 9, 3, 255, 0, 42}},
		{"7.3.1.7 ASDU 8", New(CauseGITermination, 3, FunGlobal, InfGIEnd, GITermination{SCN: 42}),
			[]byte{8, 0x81, 10, 3, 255, 0, 42}},
		{"7.3.1.8 ASDU 9", New(CauseCyclic, 3, FunDistance, 148, Measurands{Set2: true, Values: []MEA{{Raw: 4095, Overflow: true}, {Raw: -4096}, {}}}),
			[]byte{9, 0x03, 2, 3, 128, 148, 0xF9, 0x7F, 0x00, 0x80, 0, 0}},
		{"7.3.2.4 ASDU 20", New(CauseGeneralCommand, 3, FunDistance, 16, GeneralCommand{DCO: DPIOff, RII: 77}),
			[]byte{20, 0x81, 20, 3, 128, 16, 1, 77}},
		{"7.3.1.11 ASDU 23", New(CauseDisturbanceData, 3, FunDistance, 0, DisturbanceList{Faults: []RecordedFault{{FAN: 0x0201, SOF: SOFTrip | SOFTest, Time: t56}}}),
			cat([]byte{23, 0x01, 31, 3, 128, 0, 1, 2, 5}, t56Oct)},
		{"7.3.1.11 ASDU 23 empty", New(CauseDisturbanceData, 3, FunDistance, 0, DisturbanceList{}),
			[]byte{23, 0x00, 31, 3, 128, 0}},
		{"7.3.2.6 ASDU 24", New(CauseDisturbanceData, 3, FunDistance, 0, DisturbanceOrder{TOO: TOOSelectFault, TOV: 1, FAN: 0x0102, ACC: 0}),
			[]byte{24, 0x81, 31, 3, 128, 0, 1, 1, 2, 1, 0}},
		{"7.3.2.7 ASDU 25", New(CauseDisturbanceData, 3, FunDistance, 0, DisturbanceOrder{Ack: true, TOO: TOOChannelOK, TOV: 1, FAN: 2, ACC: 3}),
			[]byte{25, 0x81, 31, 3, 128, 0, 66, 1, 2, 0, 3}},
		{"7.3.1.12 ASDU 26", New(CauseDisturbanceData, 3, FunDistance, 0, DisturbanceReady{TOV: 1, FAN: 2, NOF: 3, NOC: 4, NOE: 0x0500, INT: 1000, Time: t32}),
			cat([]byte{26, 0x81, 31, 3, 128, 0, 0, 1, 2, 0, 3, 0, 4, 0, 5, 0xE8, 3}, t32Oct)},
		{"7.3.1.13 ASDU 27", New(CauseDisturbanceData, 3, FunDistance, 0, ChannelReady{TOV: 1, FAN: 2, ACC: ACCIL1, RPV: 1000, RSV: 1, RFA: 2}),
			cat([]byte{27, 0x81, 31, 3, 128, 0, 0, 1, 2, 0, 1}, floatLE(1000), floatLE(1), floatLE(2))},
		{"7.3.1.14 ASDU 28", New(CauseDisturbanceData, 3, FunDistance, 0, TagsReady{FAN: 0x0102}),
			[]byte{28, 0x81, 31, 3, 128, 0, 0, 0, 2, 1}},
		{"7.3.1.15 ASDU 29", New(CauseDisturbanceData, 3, FunDistance, 0, Tags{FAN: 2, TAP: 0x0100, Tags: []Tag{{FunDistance, 84, DPIOn}, {FunDistance, 68, DPIOff}}}),
			[]byte{29, 0x81, 31, 3, 128, 0, 2, 0, 2, 0, 1, 128, 84, 2, 128, 68, 1}},
		{"7.3.1.16 ASDU 30", New(CauseDisturbanceData, 3, FunDistance, 0, DisturbanceValues{TOV: 1, FAN: 2, ACC: 1, NFE: 25, Values: []int16{1, -1}}),
			[]byte{30, 0x81, 31, 3, 128, 0, 0, 1, 2, 0, 1, 2, 25, 0, 1, 0, 0xFF, 0xFF}},
		{"7.3.1.17 ASDU 31", New(CauseDisturbanceData, 3, FunDistance, 0, EndOfTransmission{TOO: TOOEndChannel, TOV: 1, FAN: 2, ACC: 1}),
			[]byte{31, 0x81, 31, 3, 128, 0, 35, 1, 2, 0, 1}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := c.a.Encode()
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, c.want) {
				t.Fatalf("encode\n got % X\nwant % X", got, c.want)
			}
			d, err := Decode(c.want)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(d, c.a) {
				t.Fatalf("decode\n got %#v\nwant %#v", d, c.a)
			}
		})
	}
}

// annexB holds the generic services examples of annex B, with the texts of
// the English edition where the translation translated them, and CA 1, RII 5.
var annexB = []struct {
	name string
	a    *ASDU
	want []byte
}{
	{"B.1.1 read headings", New(CauseGenericRead, 1, FunGeneric, InfReadHeadings, GenericCommand{RII: 5}),
		[]byte{21, 0x81, 42, 1, 254, 240, 5, 0}},
	{"B.1.2 headings", New(CauseGenericReadValid, 1, FunGeneric, InfReadHeadings, GenericData{RII: 5, Sets: []DataSet{
		{GIN{0, 0}, KODDescription, ASCII("System data")},
		{GIN{1, 0}, KODDescription, ASCII("Measurands")},
		{GIN{2, 0}, KODDescription, ASCII("Overcurrent Settings")},
		{GIN{3, 0}, KODDescription, ASCII("Fault records")},
	}}), cat([]byte{10, 0x81, 42, 1, 254, 240, 5, 4},
		[]byte{0, 0, 10}, gdd(DataASCII, 11, 1), ascii("System data"),
		[]byte{1, 0, 10}, gdd(DataASCII, 10, 1), ascii("Measurands"),
		[]byte{2, 0, 10}, gdd(DataASCII, 20, 1), ascii("Overcurrent Settings"),
		[]byte{3, 0, 10}, gdd(DataASCII, 13, 1), ascii("Fault records"))},
	{"B.2.1 read group descriptions", New(CauseGenericRead, 1, FunGeneric, InfReadGroup, GenericCommand{RII: 5, Items: []Item{{GIN{1, 0}, KODDescription}}}),
		[]byte{21, 0x81, 42, 1, 254, 241, 5, 1, 1, 0, 10}},
	{"B.2.2 group descriptions", New(CauseGenericReadValid, 1, FunGeneric, InfReadGroup, GenericData{RII: 5, Sets: []DataSet{
		{GIN{1, 0}, KODDescription, ASCII("Measurands")},
		{GIN{1, 1}, KODDescription, Uints(DataTextNumber, 2, 32768)},
		{GIN{1, 2}, KODDescription, ASCII("Ia")},
	}}), cat([]byte{10, 0x81, 42, 1, 254, 241, 5, 3},
		[]byte{1, 0, 10}, gdd(DataASCII, 10, 1), ascii("Measurands"),
		[]byte{1, 1, 10}, gdd(DataTextNumber, 2, 1), []byte{0x00, 0x80},
		[]byte{1, 2, 10}, gdd(DataASCII, 2, 1), ascii("Ia"))},
	{"B.3.2 group values", New(CauseGenericReadValid, 1, FunGeneric, InfReadGroup, GenericData{RII: 5, Sets: []DataSet{
		{GIN{1, 0}, KODActualValue, Uints(DataUint, 1, 3)},
		{GIN{1, 1}, KODActualValue, Float32s(63.5)},
		{GIN{1, 2}, KODActualValue, Float32s(5.0)},
	}}), cat([]byte{10, 0x81, 42, 1, 254, 241, 5, 3},
		[]byte{1, 0, 1}, gdd(DataUint, 1, 1), []byte{3},
		[]byte{1, 1, 1}, gdd(DataFloat32, 4, 1), floatLE(63.5),
		[]byte{1, 2, 1}, gdd(DataFloat32, 4, 1), floatLE(5))},
	{"B.4.1 read directory", New(CauseGenericRead, 1, FunGeneric, InfReadDirectory, GenericCommand{RII: 5, Items: []Item{{GIN{2, 0x11}, KODNone}}}),
		[]byte{21, 0x81, 42, 1, 254, 243, 5, 1, 2, 0x11, 0}},
	{"B.4.2 directory", New(CauseGenericReadValid, 1, FunGeneric, InfReadDirectory, GenericIdent{RII: 5, GIN: GIN{2, 0x11}, Elements: []DescElement{
		{KODActualValue, Uints(DataUint, 1, 3)},
		{KODDefaultValue, Uints(DataUint, 1, 0)},
		{KODRange, Uints(DataUint, 1, 0, 9, 3)},
		{KODDimension, ASCII("%")},
		{KODValueArray, Uints(DataUint, 1, 0)},
		{KODTextArray, ASCII("OFF")},
		{KODDescription, ASCII("Tap position")},
	}}), cat([]byte{11, 0x81, 42, 1, 254, 243, 5, 2, 0x11, 7},
		[]byte{1}, gdd(DataUint, 1, 1), []byte{3},
		[]byte{2}, gdd(DataUint, 1, 1), []byte{0},
		[]byte{3}, gdd(DataUint, 1, 3), []byte{0, 9, 3},
		[]byte{9}, gdd(DataASCII, 1, 1), ascii("%"),
		[]byte{22}, gdd(DataUint, 1, 1), []byte{0},
		[]byte{21}, gdd(DataASCII, 3, 1), ascii("OFF"),
		[]byte{10}, gdd(DataASCII, 12, 1), ascii("Tap position"))},
	{"B.5.2 structure", New(CauseGenericReadValid, 1, FunGeneric, InfReadEntry, GenericData{RII: 5, Sets: []DataSet{
		{GIN{3, 1}, KODActualValue, mustStructure(ASCII("Lowset trip"), Float32s(10), Value{GDD: GDD{Type: DataTime, Size: 7, Number: 1}, Data: t56Oct})},
	}}), cat([]byte{10, 0x81, 42, 1, 254, 244, 5, 1, 3, 1, 1}, gdd(DataStructure, 31, 1),
		gdd(DataASCII, 11, 1), ascii("Lowset trip"), gdd(DataFloat32, 4, 1), floatLE(10), gdd(DataTime, 7, 1), t56Oct)},
	{"B.6.2 reply code", New(CauseGenericReadInvalid, 1, FunGeneric, InfReadEntry, GenericData{RII: 5, Sets: []DataSet{
		{GIN{3, 1}, KODDefaultValue, Reply(GRCNotExist)},
	}}), []byte{10, 0x81, 43, 1, 254, 244, 5, 1, 3, 1, 2, 22, 1, 1, 2}},
	{"B.7.1 write with confirmation", New(CauseGenericWrite, 1, FunGeneric, InfWriteConfirm, GenericData{RII: 5, Sets: []DataSet{
		{GIN{2, 1}, KODActualValue, Uints(DataIndex, 2, 2)},
	}}), []byte{10, 0x81, 40, 1, 254, 249, 5, 1, 2, 1, 1, 24, 2, 1, 2, 0}},
	{"B.7.2 write confirmation", New(CauseGenericWriteConf, 1, FunGeneric, InfWriteConfirm, GenericData{RII: 5, Sets: []DataSet{
		{GIN{2, 1}, KODActualValue, Uints(DataIndex, 2, 2)},
	}}), []byte{10, 0x81, 44, 1, 254, 249, 5, 1, 2, 1, 1, 24, 2, 1, 2, 0}},
	{"B.8.1 write with execution", New(CauseGenericWrite, 1, FunGeneric, InfWriteExecute, GenericData{RII: 5, Sets: []DataSet{
		{GIN{2, 0x11}, KODActualValue, Uints(DataUint, 1, 4)},
	}}), []byte{10, 0x81, 40, 1, 254, 250, 5, 1, 2, 0x11, 1, 3, 1, 1, 4}},
	{"B.8.2 write refused", New(CauseGenericWriteNack, 1, FunGeneric, InfWriteExecute, GenericData{RII: 5, Sets: []DataSet{
		{GIN{2, 1}, KODNone, Reply(GRCAck)},
		{GIN{2, 0x11}, KODNone, mustStructure(Reply(GRCErrorDescription), ASCII("Setting to invalid position"))},
	}}), cat([]byte{10, 0x81, 41, 1, 254, 250, 5, 2, 2, 1, 0, 22, 1, 1, 0, 2, 0x11, 0}, gdd(DataStructure, 34, 1),
		gdd(DataGRC, 1, 1), []byte{11}, gdd(DataASCII, 27, 1), ascii("Setting to invalid position"))},
}

func mustStructure(f ...Value) Value {
	v, err := Structure(f...)
	if err != nil {
		panic(err)
	}
	return v
}

// TestAnnexB checks the generic services examples of annex B.
func TestAnnexB(t *testing.T) {
	for _, c := range annexB {
		t.Run(c.name, func(t *testing.T) {
			got, err := c.a.Encode()
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, c.want) {
				t.Fatalf("encode\n got % X\nwant % X", got, c.want)
			}
			d, err := Decode(got)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(d, c.a) {
				t.Fatalf("decode\n got %#v\nwant %#v", d, c.a)
			}
		})
	}
}

func TestGenericValues(t *testing.T) {
	// B.5.2: the structure's fields, and the time in the third.
	st := annexB[7].a.Body.(GenericData).Sets[0].Value
	f, err := st.Fields()
	if err != nil || len(f) != 3 {
		t.Fatalf("fields %v %v", f, err)
	}
	if f[0].Text() != "Lowset trip" {
		t.Fatal(f[0].Text())
	}
	if v, err := f[1].Float(0); err != nil || v != 10 {
		t.Fatal(v, err)
	}
	tm, err := cp.ParseCP56(f[2].Data, nil)
	if err != nil || !tm.Equal(t56.Time) {
		t.Fatal(tm, err)
	}
	// B.4.2: range 0, 9, 3.
	r := Uints(DataUint, 1, 0, 9, 3)
	if v, _ := r.Uint(1); v != 9 {
		t.Fatal(v)
	}
	if _, err := r.Uint(3); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if v, err := Ints(2, -2, 300).Int(0); err != nil || v != -2 {
		t.Fatal(v, err)
	}
	if v, _ := Ints(2, -2, 300).Int(1); v != 300 {
		t.Fatal(v)
	}
	if v, _ := Ints(8, math.MinInt64).Int(0); v != math.MinInt64 {
		t.Fatal(v)
	}
	if v, err := Float64s(0.25).Float(0); err != nil || v != 0.25 {
		t.Fatal(v, err)
	}
	if _, err := Uints(DataUint, 4, 1).Float(0); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := Float32s(1).Fields(); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	bits := Value{GDD: GDD{Type: DataBitString, Size: 7, Number: 2}, Data: []byte{0, 0}}
	if bits.DataLen() != 2 {
		t.Fatal(bits.DataLen())
	}
	if _, err := bits.Uint(0); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := Float64s(1).Uint(0); err != nil {
		t.Fatal(err)
	}
	if _, err := Float64s(1).Int(5); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	// A structure field whose data disagrees with its description.
	if _, err := Structure(Value{GDD: GDD{Type: DataUint, Size: 2, Number: 1}}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := Structure(ASCII(strings.Repeat("x", 200)), ASCII(strings.Repeat("y", 60))); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	broken := Value{GDD: GDD{Type: DataStructure, Size: 4, Number: 1}, Data: []byte{1, 9, 1, 0}}
	if _, err := broken.Fields(); !errors.Is(err, ErrShort) {
		t.Fatal(err)
	}
}

// TestRaw checks that private and reserved type identifications and
// private causes keep every octet.
func TestRaw(t *testing.T) {
	for _, b := range [][]byte{
		{200, 0x05, 1, 3, 17, 99, 1, 2, 3},          // private type
		{12, 0x81, 1, 3, 128, 1},                    // reserved type of the compatible range
		{1, 0x81, 64, 3, 128, 16, 0xFF, 0xFF, 0xFF}, // ASDU 1 with a private cause and another structure
		{7, 0x81, 70, 3, 255, 0, 5},                 // clause 7.2: type 7 with cause > 63
	} {
		a, err := Decode(b)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := a.Body.(Raw); !ok {
			t.Fatalf("% X decoded as %T", b, a.Body)
		}
		got, err := a.Encode()
		if err != nil || !bytes.Equal(got, b) {
			t.Fatalf("% X re-encoded as % X (%v)", b, got, err)
		}
		if a.Body.Type() != 0 {
			t.Fatal(a.Body.Type())
		}
	}
	// DecodeTyped interprets type 7 with a private cause.
	a, err := DecodeTyped([]byte{7, 0x81, 70, 3, 255, 0, 5}, nil)
	if err != nil || a.Body != (GIInit{SCN: 5}) {
		t.Fatal(a, err)
	}
}

func TestDecodeErrors(t *testing.T) {
	cases := []struct {
		b   []byte
		err error
	}{
		{[]byte{1, 0x81, 1, 3, 128}, ErrShort},
		{[]byte{1, 0x81, 1, 3, 128, 16, 2, 0, 0, 0}, ErrShort},
		{[]byte{1, 0x81, 1, 3, 128, 16, 2, 0, 0, 0, 0, 0, 9}, ErrTrailing},
		{[]byte{3, 0x05, 2, 3, 128, 144, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, ErrInvalid},
		{[]byte{3, 0x00, 2, 3, 128, 144}, ErrInvalid},
		{[]byte{9, 0x0A, 2, 3, 128, 148}, ErrInvalid},
		{append([]byte{23, 0x09, 31, 3, 128, 0}, make([]byte, 90)...), ErrInvalid},
		{[]byte{29, 0x81, 31, 3, 128, 0, 0, 0, 26, 0, 0}, ErrInvalid},
		{[]byte{30, 0x81, 31, 3, 128, 0, 0, 1, 0, 0, 1, 26, 0, 0}, ErrInvalid},
		{[]byte{10, 0x81, 42, 3, 254, 244, 0, 1, 1, 1, 1, 3, 2, 1, 0}, ErrShort},
		{[]byte{10, 0x81, 42, 3, 254, 244, 0, 1, 1, 1}, ErrShort},
		{[]byte{11, 0x81, 42, 3, 254, 243, 0, 1, 1, 1, 1, 3, 1, 1}, ErrShort},
		{[]byte{21, 0x81, 42, 3, 254, 244, 0, 2, 1, 1, 1}, ErrShort},
		{make([]byte, MaxSize+1), ErrTooLong},
	}
	for _, c := range cases {
		if _, err := Decode(c.b); !errors.Is(err, c.err) {
			t.Errorf("% X: %v, want %v", c.b, err, c.err)
		}
	}
}

func TestEncodeErrors(t *testing.T) {
	big := make([]MEA, 10)
	sets := make([]DataSet, 64)
	for i := range sets {
		sets[i].Value = Reply(GRCAck)
	}
	cases := []*ASDU{
		{Cause: 1},
		New(2, 1, 128, 144, Measurands{}),
		New(2, 1, 128, 144, Measurands{Values: big[:5]}),
		New(2, 1, 128, 148, Measurands{Set2: true, Values: big}),
		New(42, 1, 254, 244, GenericData{Sets: sets}),
		New(42, 1, 254, 244, GenericData{Sets: []DataSet{{Value: Value{GDD: GDD{Type: DataUint, Size: 2, Number: 1}}}}}),
		New(42, 1, 254, 244, GenericData{Sets: []DataSet{{Value: Value{GDD: GDD{Type: DataUint, Size: 0, Number: 200}}}}}),
		New(42, 1, 254, 243, GenericIdent{Elements: make([]DescElement, 64)}),
		New(42, 1, 254, 243, GenericIdent{Elements: []DescElement{{Value: Value{GDD: GDD{Type: DataUint, Size: 1, Number: 1}}}}}),
		New(42, 1, 254, 244, GenericCommand{Items: make([]Item, 256)}),
		New(42, 1, 254, 244, GenericCommand{Items: make([]Item, 83)}),
		New(31, 1, 128, 0, DisturbanceList{Faults: make([]RecordedFault, 9)}),
		New(31, 1, 128, 0, Tags{Tags: make([]Tag, 26)}),
		New(31, 1, 128, 0, DisturbanceValues{Values: make([]int16, 26)}),
		New(1, 1, 200, 1, Raw{Data: make([]byte, 248)}),
	}
	for i, a := range cases {
		if _, err := a.Encode(); err == nil {
			t.Errorf("case %d: encoded %v", i, a)
		}
	}
}

func TestMEA(t *testing.T) {
	for _, c := range []struct {
		v    float64
		want MEA
	}{
		{0.5, MEA{Raw: 2048}},
		{-1, MEA{Raw: -4096}},
		{1.5, MEA{Raw: 4095, Overflow: true}},
		{-1.5, MEA{Raw: -4096, Overflow: true}},
	} {
		if got := MEAOf(c.v); got != c.want {
			t.Errorf("MEAOf(%v) = %+v", c.v, got)
		}
	}
	if MEAOf(0.25).Value() != 0.25 {
		t.Fatal(MEAOf(0.25).Value())
	}
	// RES (bit 3) is ignored.
	if m := meaOf(0x0004); m != (MEA{}) {
		t.Fatal(m)
	}
}

func TestStrings(t *testing.T) {
	for _, c := range []struct{ got, want string }{
		{TypeTimeTagged.String(), "<1> time-tagged message"},
		{TypeID(12).String(), "<12> reserved"},
		{TypeID(0).String(), "<0> reserved"},
		{TypeID(40).String(), "<40> private"},
		{CauseGI.String(), "<9> general interrogation"},
		{Cause(13).String(), "<13> reserved"},
		{Cause(64).String(), "<64> private"},
		{FunDistance.String(), "<128> distance protection"},
		{FunOvercurrent.String(), "<160> overcurrent protection"},
		{FunTransformerDiff.String(), "<176> transformer differential protection"},
		{FunLineDiff.String(), "<192> line differential protection"},
		{FunGeneric.String(), "<254> generic function type"},
		{FunGlobal.String(), "<255> global function type"},
		{FunctionType(129).String(), "<129> not used"},
		{FunctionType(17).String(), "<17> private"},
		{DPIOn.String(), "ON"},
		{DPIOff.String(), "OFF"},
		{DPI(3).String(), "DPI(3)"},
		{GIN{2, 0x11}.String(), "0211H"},
		{StateDown.String(), "down"},
		{StateUp.String(), "up"},
		{StateClosed.String(), "closed"},
		{State(9).String(), "unknown"},
		{New(1, 3, 128, 16, TimeTagged{}).String(), "<1> time-tagged message <1> spontaneous CA=3 FUN=128 INF=16"},
		{IdentificationOf(2, "VERYLONGNAME", [4]byte{}).Manufacturer(), "VERYLONG"},
	} {
		if c.got != c.want {
			t.Errorf("%q, want %q", c.got, c.want)
		}
	}
	if !TypeTags.Compatible() || TypeID(32).Compatible() || TypeID(12).Defined() {
		t.Fatal("type ranges")
	}
	if v := NewVSQ(3, true); v.Number() != 3 || !v.SQ() {
		t.Fatal(v)
	}
	for f, want := range map[FunctionType]bool{0: false, 127: false, 128: true, 129: true, 130: false, 144: true, 145: true, 146: false, 240: true, 241: true, 253: false, 254: true, 255: true} {
		if f.Compatible() != want {
			t.Errorf("FUN %d compatible %v", f, !want)
		}
	}
}

// TestTimeZone checks that time tags go through the given location.
func TestTimeZone(t *testing.T) {
	loc := time.FixedZone("UTC+2", 2*3600)
	a := New(CauseTimeSync, 1, FunGlobal, 0, TimeSync{Time: cp.Timestamp{Time: time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)}})
	b, err := a.AppendIn(nil, loc)
	if err != nil {
		t.Fatal(err)
	}
	if b[9] != 12 {
		t.Fatalf("hour %d", b[9])
	}
	d, err := DecodeIn(b, loc)
	if err != nil || !d.Body.(TimeSync).Time.Equal(a.Body.(TimeSync).Time.Time) {
		t.Fatal(d, err)
	}
}

// TestRoundTrip decodes, encodes and decodes every example again.
func TestRoundTrip(t *testing.T) {
	for _, c := range annexB {
		b, _ := c.a.Encode()
		roundTrip(t, b)
	}
}

func roundTrip(t *testing.T, b []byte) {
	t.Helper()
	a, err := Decode(b)
	if err != nil {
		return
	}
	e, err := a.Encode()
	if err != nil {
		t.Fatalf("% X decoded but does not encode: %v", b, err)
	}
	a2, err := Decode(e)
	if err != nil {
		t.Fatalf("re-encoded % X does not decode: %v", e, err)
	}
	// Out-of-range time fields are normalized on the first pass
	// (package cp); from then on the octets are stable.
	e2, err := a2.Encode()
	if err != nil || !bytes.Equal(e, e2) {
		t.Fatalf("round trip of % X: % X, then % X (%v)", b, e, e2, err)
	}
	if _, raw := a.Body.(Raw); raw && !bytes.Equal(b, e) {
		t.Fatalf("raw ASDU % X re-encoded as % X", b, e)
	}
}

func FuzzDecode(f *testing.F) {
	for _, c := range annexB {
		b, _ := c.a.Encode()
		f.Add(b)
	}
	f.Add(cat([]byte{1, 0x81, 1, 3, 128, 16, 2}, t32Oct, []byte{0}))
	f.Add([]byte{3, 0x02, 2, 3, 160, 145, 0x08, 0x00, 0xFA, 0xFF})
	f.Add(cat([]byte{23, 0x01, 31, 3, 128, 0, 1, 2, 5}, t56Oct))
	f.Add([]byte{29, 0x81, 31, 3, 128, 0, 2, 0, 2, 0, 1, 128, 84, 2, 128, 68, 1})
	f.Add([]byte{30, 0x81, 31, 3, 128, 0, 0, 1, 2, 0, 1, 2, 25, 0, 1, 0, 0xFF, 0xFF})
	f.Add([]byte{200, 0x05, 1, 3, 17, 99, 1, 2, 3})
	f.Fuzz(func(t *testing.T, b []byte) {
		roundTrip(t, b)
	})
}
