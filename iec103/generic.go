// SPDX-License-Identifier: MIT

package iec103

import (
	"encoding/binary"
	"fmt"
	"math"
)

// DataType is the DATATYPE of a generic data description (clause
// 7.2.6.32).
type DataType uint8

// Data types.
const (
	DataNone        DataType = 0  // no data
	DataASCII       DataType = 1  // OS8ASCII
	DataBitString   DataType = 2  // packed bit string BS1; DATASIZE counts bits
	DataUint        DataType = 3  // unsigned integer UI
	DataInt         DataType = 4  // integer I
	DataUFixed      DataType = 5  // unsigned fixed point UF
	DataFixed       DataType = 6  // fixed point F
	DataFloat32     DataType = 7  // R32.23, IEEE 754 short real
	DataFloat64     DataType = 8  // R64.53, IEEE 754 real
	DataDPI         DataType = 9  // double point information
	DataSPI         DataType = 10 // single point information
	DataDPITransErr DataType = 11 // double point with transient and error
	DataMEA         DataType = 12 // measurand with quality descriptor
	DataTime        DataType = 14 // CP56Time2a, possibly shortened (clause 7.2.6.29)
	DataGIN         DataType = 15 // generic identification number
	DataRelTime     DataType = 16 // relative time
	DataFunInf      DataType = 17 // function type and information number
	DataTimeTagged  DataType = 18 // time-tagged message CP48{DPI, RES, TIME, SIN}
	DataRelTagged   DataType = 19 // time-tagged message with relative time CP80
	DataRelMeasure  DataType = 20 // time-tagged measurand with relative time CP96
	DataTextNumber  DataType = 21 // external text number
	DataGRC         DataType = 22 // generic reply code
	DataStructure   DataType = 23 // data structure CPii{(GDD, GID)}
	DataIndex       DataType = 24 // index
)

// GDD is the generic data description (clause 7.2.6.32): CP24{DATATYPE,
// DATASIZE, NUMBER, CONT}.
type GDD struct {
	Type DataType
	// Size is DATASIZE, 1..255: octets of one element, or bits for
	// [DataBitString].
	Size uint8
	// Number is NUMBER, 1..127: the elements in this ASDU.
	Number uint8
	// Cont is CONT: the data continue in the next ASDU with the same RII.
	Cont bool
}

// DataLen returns the length of the GID the description announces
// (clause 7.2.6.33: DATASIZE times NUMBER). For a packed bit string
// DATASIZE counts bits and the elements are packed into octets without
// gaps (clause 7.2.6.32), so the length is the number of octets the bits
// fill; that rounding is our reading of the "seven and two elements take
// two octets" example.
func (g GDD) DataLen() int {
	n := int(g.Size) * int(g.Number)
	if g.Type == DataBitString {
		return (n + 7) / 8
	}
	return n
}

func (g GDD) append(b []byte) []byte {
	n := g.Number & 0x7F
	if g.Cont {
		n |= 0x80
	}
	return append(b, byte(g.Type), g.Size, n)
}

func gddOf(b []byte) GDD {
	return GDD{Type: DataType(b[0]), Size: b[1], Number: b[2] & 0x7F, Cont: b[2]&0x80 != 0}
}

// check reports a description outside the ranges of clause 7.2.6.32:
// DATASIZE 1..255, NUMBER 1..127.
func (g GDD) check() error {
	if g.Size == 0 || g.Number == 0 || g.Number > 127 {
		return fmt.Errorf("%w: GDD DATASIZE %d NUMBER %d, clause 7.2.6.32 requires 1..255 and 1..127", ErrInvalid, g.Size, g.Number)
	}
	return nil
}

// Value is generic data: a description and the data it describes (GDD and
// GID, clauses 7.2.6.32 and 7.2.6.33). Data holds [GDD.DataLen] octets.
type Value struct {
	GDD
	Data []byte
}

// octet returns n as a GDD field of one octet (DATASIZE, NUMBER), or 0
// when n does not fit: 0 is outside both fields' ranges (clause 7.2.6.32),
// so encoding the value fails instead of announcing a truncated count.
func octet(n int) uint8 {
	if n < 0 || n > math.MaxUint8 {
		return 0
	}
	return uint8(n)
}

// ASCII returns an OS8ASCII value (DATASIZE = length, NUMBER = 1).
func ASCII(s string) Value {
	return Value{GDD: GDD{Type: DataASCII, Size: octet(len(s)), Number: 1}, Data: []byte(s)}
}

// Uints returns values of type t (DataUint, DataIndex, DataTextNumber or
// any other unsigned type) of size octets each, least significant octet
// first.
func Uints(t DataType, size int, vs ...uint64) Value {
	v := Value{GDD: GDD{Type: t, Size: octet(size), Number: octet(len(vs))}}
	for _, x := range vs {
		for i := range size {
			v.Data = append(v.Data, byte(x>>(8*i)&0xFF))
		}
	}
	return v
}

// Ints returns two's complement integers (DataInt) of size octets each.
func Ints(size int, vs ...int64) Value {
	u := make([]uint64, len(vs))
	for i, x := range vs {
		// The two's complement bit pattern of x, taken in two masked
		// halves so no conversion changes a value.
		u[i] = uint64(x>>32&0xFFFFFFFF)<<32 | uint64(x&0xFFFFFFFF)
	}
	v := Uints(DataInt, size, u...)
	return v
}

// Float32s returns R32.23 values.
func Float32s(vs ...float32) Value {
	v := Value{GDD: GDD{Type: DataFloat32, Size: 4, Number: octet(len(vs))}}
	for _, x := range vs {
		v.Data = binary.LittleEndian.AppendUint32(v.Data, math.Float32bits(x))
	}
	return v
}

// Float64s returns R64.53 values.
func Float64s(vs ...float64) Value {
	v := Value{GDD: GDD{Type: DataFloat64, Size: 8, Number: octet(len(vs))}}
	for _, x := range vs {
		v.Data = binary.LittleEndian.AppendUint64(v.Data, math.Float64bits(x))
	}
	return v
}

// Reply returns a generic reply code value (DataGRC, one octet).
func Reply(c GRC) Value {
	return Value{GDD: GDD{Type: DataGRC, Size: 1, Number: 1}, Data: []byte{byte(c)}}
}

// Structure returns a data structure (DataStructure, clause 7.2.6.32 type
// 23): the fields one after the other as GDD and GID, with DATASIZE their
// total length and NUMBER 1, as annex B.5.2 shows.
func Structure(fields ...Value) (Value, error) {
	var d []byte
	for _, f := range fields {
		var err error
		if d, err = appendValue(d, f); err != nil {
			return Value{}, fmt.Errorf("structure field: %w", err)
		}
	}
	if len(d) == 0 || len(d) > 255 {
		return Value{}, fmt.Errorf("%w: structure of %d octets, DATASIZE is 1..255", ErrInvalid, len(d))
	}
	return Value{GDD: GDD{Type: DataStructure, Size: octet(len(d)), Number: 1}, Data: d}, nil
}

// Text returns the data as a string (for DataASCII).
func (v Value) Text() string { return string(v.Data) }

// Uint returns element i as an unsigned integer, least significant octet
// first. It fails for elements wider than 8 octets and bit strings.
func (v Value) Uint(i int) (uint64, error) {
	e, err := v.element(i)
	if err != nil {
		return 0, err
	}
	var x uint64
	for j := len(e) - 1; j >= 0; j-- {
		x = x<<8 | uint64(e[j])
	}
	return x, nil
}

// Int returns element i as a two's complement integer.
func (v Value) Int(i int) (int64, error) {
	x, err := v.Uint(i)
	if err != nil {
		return 0, err
	}
	bits := 8 * uint(v.Size)
	if bits < 64 && x&(1<<(bits-1)) != 0 {
		x |= ^uint64(0) << bits
	}
	// Reassemble the two's complement value from its sign bit and the
	// 63 bits below it, so no conversion changes a value.
	n := int64(x & math.MaxInt64)
	if x&(1<<63) != 0 {
		n += math.MinInt64
	}
	return n, nil
}

// Float returns element i of a DataFloat32 or DataFloat64 value.
func (v Value) Float(i int) (float64, error) {
	e, err := v.element(i)
	if err != nil {
		return 0, err
	}
	switch {
	case v.Type == DataFloat32 && len(e) == 4:
		return float64(math.Float32frombits(binary.LittleEndian.Uint32(e))), nil
	case v.Type == DataFloat64 && len(e) == 8:
		return math.Float64frombits(binary.LittleEndian.Uint64(e)), nil
	}
	return 0, fmt.Errorf("%w: data type %d size %d is not a real", ErrInvalid, v.Type, v.Size)
}

func (v Value) element(i int) ([]byte, error) {
	if v.Type == DataBitString || v.Size == 0 || v.Size > 8 {
		return nil, fmt.Errorf("%w: data type %d size %d has no numeric elements", ErrInvalid, v.Type, v.Size)
	}
	if i < 0 || i >= int(v.Number) || (i+1)*int(v.Size) > len(v.Data) {
		return nil, fmt.Errorf("%w: element %d of %d", ErrInvalid, i, v.Number)
	}
	return v.Data[i*int(v.Size) : (i+1)*int(v.Size)], nil
}

// Fields returns the fields of a DataStructure value.
func (v Value) Fields() ([]Value, error) {
	if v.Type != DataStructure {
		return nil, fmt.Errorf("%w: data type %d is not a structure", ErrInvalid, v.Type)
	}
	var out []Value
	r := reader{b: v.Data}
	for r.len() > 0 {
		f, err := r.value()
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, nil
}

// DataSet is one data set of generic data (clause 7.4.8.3, figure 12): GIN,
// KOD, GDD and GID.
type DataSet struct {
	GIN   GIN
	KOD   KOD
	Value Value
}

// DescElement is one description element of a generic identification
// (ASDU 11): KOD, GDD and GID.
type DescElement struct {
	KOD   KOD
	Value Value
}

// Item is one generic identifier of a generic command (ASDU 21): GIN and
// KOD (clause 7.2.6.37).
type Item struct {
	GIN GIN
	KOD KOD
}

func appendValue(b []byte, v Value) ([]byte, error) {
	if len(v.Data) != v.DataLen() {
		return nil, fmt.Errorf("%w: GID of %d octets, GDD announces %d", ErrInvalid, len(v.Data), v.DataLen())
	}
	if err := v.check(); err != nil {
		return nil, err
	}
	return append(v.append(b), v.Data...), nil
}
