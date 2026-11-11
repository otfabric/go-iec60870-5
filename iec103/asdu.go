// SPDX-License-Identifier: MIT

package iec103

import (
	"encoding/binary"
	"fmt"
	"math"
	"time"

	"github.com/otfabric/go-iec60870-5/cp"
)

// ASDU is an application service data unit of IEC 60870-5-103 (clause
// 7.1): the data unit identifier, the information object identifier and
// one information object, the [Body].
type ASDU struct {
	// Type is the type identification. Encode takes it from the Body,
	// except for a [Raw] body.
	Type TypeID
	// VSQ is the variable structure qualifier as received. Encode writes
	// the qualifier the Body defines (the number of its elements), except
	// for a [Raw] body.
	VSQ VSQ
	// Cause is the cause of transmission.
	Cause Cause
	// CommonAddr is the common address of ASDU (clause 7.2.4), normally
	// the link address of the protection equipment; 255 is global.
	CommonAddr uint8
	// Fun and Inf are the information object identifier (clause 7.2.5).
	// ASDUs 23 to 31 do not use the information number (it is "not used",
	// clause 7.3.1.11 ff.); it is kept as received and written as given.
	Fun FunctionType
	Inf InfoNumber
	// Body holds the information elements.
	Body Body
}

// New returns an ASDU with Type and VSQ taken from body.
func New(cause Cause, ca uint8, fun FunctionType, inf InfoNumber, body Body) *ASDU {
	return &ASDU{Type: body.Type(), VSQ: body.vsq(), Cause: cause, CommonAddr: ca, Fun: fun, Inf: inf, Body: body}
}

// String describes the ASDU, for example
// "<1> time-tagged message <1> spontaneous CA=3 FUN=128 INF=16".
func (a *ASDU) String() string {
	return fmt.Sprintf("%s %s CA=%d FUN=%d INF=%d", a.Type, a.Cause, a.CommonAddr, a.Fun, a.Inf)
}

// Body is the information elements of an ASDU after the information
// object identifier. The concrete types are listed in the package
// documentation; [Raw] holds any other.
type Body interface {
	// Type returns the type identification of the body.
	Type() TypeID
	vsq() VSQ
	append(b []byte, loc *time.Location) ([]byte, error)
}

// Raw is a body this package does not interpret: a type identification of
// the private or reserved range, or a private cause of transmission. Data
// holds every octet after the information number; with the Type and VSQ
// of the ASDU it encodes back exactly as received.
type Raw struct{ Data []byte }

// Type returns 0: the type identification of a raw body is the ASDU's.
func (Raw) Type() TypeID { return 0 }
func (Raw) vsq() VSQ     { return 0 }
func (r Raw) append(b []byte, _ *time.Location) ([]byte, error) {
	return append(b, r.Data...), nil
}

// Encode returns the octets of a, with time tags in UTC.
func (a *ASDU) Encode() ([]byte, error) { return a.AppendIn(nil, nil) }

// AppendIn appends the octets of a to b, with time tags in loc (nil means
// UTC). It fails with [ErrTooLong] for an ASDU longer than [MaxSize].
func (a *ASDU) AppendIn(b []byte, loc *time.Location) ([]byte, error) {
	if a.Body == nil {
		return nil, fmt.Errorf("%w: ASDU without body", ErrInvalid)
	}
	start := len(b)
	t, vsq := a.Type, a.VSQ
	if _, raw := a.Body.(Raw); !raw {
		t, vsq = a.Body.Type(), a.Body.vsq()
	}
	b = append(b, byte(t), byte(vsq), byte(a.Cause), a.CommonAddr, byte(a.Fun), byte(a.Inf))
	b, err := a.Body.append(b, loc)
	if err != nil {
		return nil, err
	}
	if len(b)-start > MaxSize {
		return nil, fmt.Errorf("%w: %d octets, at most %d", ErrTooLong, len(b)-start, MaxSize)
	}
	return b, nil
}

// headerSize is the data unit identifier and the information object
// identifier.
const headerSize = 6

// Decode decodes an ASDU with time tags in UTC.
func Decode(b []byte) (*ASDU, error) { return DecodeIn(b, nil) }

// DecodeIn decodes an ASDU with time tags in loc (nil means UTC). A type
// identification outside tables 3 and 4, or a cause of transmission of the
// private range, gives a [Raw] body; see [DecodeTyped] to interpret a
// private cause with the compatible structure.
func DecodeIn(b []byte, loc *time.Location) (*ASDU, error) {
	return decode(b, loc, false)
}

// DecodeTyped decodes like [DecodeIn] but interprets a type identification
// of tables 3 and 4 with its compatible structure whatever the cause of
// transmission, for stations whose private causes keep that structure.
func DecodeTyped(b []byte, loc *time.Location) (*ASDU, error) {
	return decode(b, loc, true)
}

func decode(b []byte, loc *time.Location, anyCause bool) (*ASDU, error) {
	if len(b) < headerSize {
		return nil, fmt.Errorf("%w: %d octets, the identifiers need %d", ErrShort, len(b), headerSize)
	}
	if len(b) > MaxSize {
		return nil, fmt.Errorf("%w: %d octets", ErrTooLong, len(b))
	}
	a := &ASDU{Type: TypeID(b[0]), VSQ: VSQ(b[1]), Cause: Cause(b[2]), CommonAddr: b[3], Fun: FunctionType(b[4]), Inf: InfoNumber(b[5])}
	r := &reader{b: b[headerSize:], loc: loc}
	if !a.Type.Defined() || (!a.Cause.Compatible() && !anyCause) {
		a.Body = Raw{Data: append([]byte{}, r.b...)}
		return a, nil
	}
	body, err := decodeBody(a.Type, a.VSQ, r)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", a.Type, err)
	}
	if r.err != nil {
		return nil, fmt.Errorf("%s: %w", a.Type, r.err)
	}
	if r.len() > 0 {
		return nil, fmt.Errorf("%s: %w: %d octet(s)", a.Type, ErrTrailing, r.len())
	}
	a.Body = body
	return a, nil
}

// reader consumes octets; the first shortfall sets err and later reads
// return zero values.
type reader struct {
	b   []byte
	err error
	loc *time.Location
}

func (r *reader) len() int { return len(r.b) }

func (r *reader) take(n int) []byte {
	if r.err != nil {
		return make([]byte, n)
	}
	if len(r.b) < n {
		r.err = fmt.Errorf("%w: need %d octet(s), have %d", ErrShort, n, len(r.b))
		r.b = nil
		return make([]byte, n)
	}
	v := r.b[:n:n]
	r.b = r.b[n:]
	return v
}

func (r *reader) u8() uint8    { return r.take(1)[0] }
func (r *reader) u16() uint16  { return binary.LittleEndian.Uint16(r.take(2)) }
func (r *reader) f32() float32 { return math.Float32frombits(binary.LittleEndian.Uint32(r.take(4))) }
func (r *reader) dpi() DPI     { return DPI(r.u8() & 0x03) }

func (r *reader) cp32() cp.Timestamp {
	t, _ := cp.ParseCP32(r.take(cp.Size32), r.loc) // length guaranteed by take
	return t
}

func (r *reader) cp56() cp.Timestamp {
	t, _ := cp.ParseCP56(r.take(cp.Size56), r.loc)
	return t
}

// value reads a GDD and its GID.
func (r *reader) value() (Value, error) {
	g := gddOf(r.take(3))
	if r.err != nil {
		return Value{}, r.err
	}
	n := g.DataLen()
	if n > r.len() {
		return Value{}, fmt.Errorf("%w: GID of %d octet(s), %d left", ErrShort, n, r.len())
	}
	return Value{GDD: g, Data: append([]byte{}, r.take(n)...)}, nil
}

func (r *reader) gin() GIN { v := r.take(2); return GIN{Group: v[0], Entry: v[1]} }

func appendU16(b []byte, v uint16) []byte { return binary.LittleEndian.AppendUint16(b, v) }
func appendF32(b []byte, v float32) []byte {
	return binary.LittleEndian.AppendUint32(b, math.Float32bits(v))
}
