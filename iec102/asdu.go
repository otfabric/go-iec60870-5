// SPDX-License-Identifier: MIT

package iec102

import (
	"fmt"
	"time"
)

// DefaultCommonAddrSize is the size of the common address of ASDU when
// [Params] leaves it zero: two octets, as in the Beckhoff example
// configuration (TS650x manual, section 9.3), the REE profile and the
// recorded traffic.
const DefaultCommonAddrSize = 2

// Params are the system parameters of the codec.
type Params struct {
	// CommonAddrSize is the size of the common address of ASDU (the REE
	// profile calls it the measuring point address): 1 or 2 octets
	// (Beckhoff TS650x manual, section 3.2). Zero means
	// [DefaultCommonAddrSize].
	CommonAddrSize int
	// Location is the time zone of TimeA and TimeB conversions done on
	// behalf of the caller; the wire values are always kept as fields.
	// Nil means UTC.
	Location *time.Location
	// Profile, when set, interprets type identifications outside the
	// standard range (or reinterprets standard ones) before the standard
	// codec does. Without a profile they decode as [Raw].
	Profile Profile
}

// Validate reports invalid parameters.
func (p Params) Validate() error {
	switch p.CommonAddrSize {
	case 0, 1, 2:
		return nil
	}
	return fmt.Errorf("%w: common address size %d, want 1 or 2", ErrInvalid, p.CommonAddrSize)
}

func (p Params) caSize() int {
	if p.CommonAddrSize == 0 {
		return DefaultCommonAddrSize
	}
	return p.CommonAddrSize
}

// HeaderSize returns the size of the data unit identifier and the record
// address: type, VSQ, cause, common address, record address.
func (p Params) HeaderSize() int { return 4 + p.caSize() }

// Profile interprets ASDUs of a profile of IEC 60870-5-102, such as the
// Spanish REE profile in package
// [github.com/otfabric/go-iec60870-5/iec102/ree].
type Profile interface {
	// DecodeBody returns the body of a, whose header fields are set, from
	// the octets after the record address. It returns nil and no error for
	// a type identification it does not interpret.
	DecodeBody(a *ASDU, data []byte, p Params) (Body, error)
}

// ASDU is an application service data unit of IEC 60870-5-102 (Beckhoff
// TS650x manual, section 3.2): the data unit identifier, the record
// address and the information objects, the [Body].
//
//	type identification | VSQ | cause of transmission | common address (1 or 2 octets)
//	record address | information objects ... | [common time tag]
type ASDU struct {
	// Type is the type identification. Encode takes it from the Body,
	// except for a [Raw] body.
	Type TypeID
	// VSQ is the variable structure qualifier. Encode writes it as given,
	// except that a [Sequence] body sets the number of objects to its own
	// count. [New] sets it from the body.
	VSQ VSQ
	// Cause is the cause of transmission.
	Cause Cause
	// CommonAddr is the common address of ASDU; the REE profile calls it
	// the measuring point address.
	CommonAddr uint16
	// Record is the record address (Beckhoff: "record address or list
	// address of accounting period", section 9.1.8).
	Record uint8
	// Body holds the information objects.
	Body Body
}

// New returns an ASDU with Type and VSQ taken from body.
func New(cause Cause, ca uint16, record uint8, body Body) *ASDU {
	return &ASDU{Type: body.Type(), VSQ: VSQ(body.Objects() & 0x7F), Cause: cause, CommonAddr: ca, Record: record, Body: body}
}

// String describes the ASDU, for example
// "M_IT_TK_2 <5> request CA=1 record=11".
func (a *ASDU) String() string {
	return fmt.Sprintf("%s %s CA=%d record=%d", a.Type, a.Cause, a.CommonAddr, a.Record)
}

// Mirrors reports whether a is the mirror of request r: the same type
// identification, common address and record address, as a station's
// confirmation, termination or refusal of a request is.
func (a *ASDU) Mirrors(r *ASDU) bool {
	return a.Type == r.Type && a.CommonAddr == r.CommonAddr && a.Record == r.Record
}

// Body is the information objects of an ASDU after the record address.
// The concrete types of this package are listed in the package
// documentation; [Raw] holds any other. A [Profile] adds its own.
type Body interface {
	// Type returns the type identification of the body.
	Type() TypeID
	// Objects returns the number of information objects [New] writes into
	// the VSQ.
	Objects() int
	// AppendTo appends the encoded information objects.
	AppendTo(b []byte, p Params) ([]byte, error)
}

// Sequence is implemented by a body that holds a variable number of
// information objects: Encode writes its Objects into the VSQ.
type Sequence interface {
	Body
	Sequence()
}

// Raw is a body this package does not interpret: a type identification
// outside the standard range without a [Profile] that knows it, a standard
// type whose layout no source documents (M_EI_NA_2), a list with the SQ bit
// set, or a refusal whose objects do not fit the type. Data holds every
// octet after the record address; with the Type and VSQ of the ASDU it
// encodes back exactly as received.
type Raw struct{ Data []byte }

// Type returns 0: the type identification of a raw body is the ASDU's.
func (Raw) Type() TypeID { return 0 }

// Objects returns 0; the VSQ of a raw ASDU is written as given.
func (Raw) Objects() int { return 0 }

// AppendTo appends Data.
func (r Raw) AppendTo(b []byte, _ Params) ([]byte, error) { return append(b, r.Data...), nil }

// Encode returns the octets of a.
func (a *ASDU) Encode(p Params) ([]byte, error) { return a.AppendTo(nil, p) }

// AppendTo appends the octets of a to b. It fails with [ErrTooLong] for
// an ASDU longer than [MaxSize].
func (a *ASDU) AppendTo(b []byte, p Params) ([]byte, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	if a.Body == nil {
		return nil, fmt.Errorf("%w: ASDU without body", ErrInvalid)
	}
	start := len(b)
	t, vsq := a.Type, a.VSQ
	if _, raw := a.Body.(Raw); !raw {
		t = a.Body.Type()
	}
	if s, ok := a.Body.(Sequence); ok {
		n := s.Objects()
		if n > 0x7F {
			return nil, fmt.Errorf("%w: %d information objects, the VSQ counts at most 127", ErrInvalid, n)
		}
		vsq = vsq&0x80 | VSQ(n)
	}
	b = append(b, byte(t), byte(vsq), byte(a.Cause), byte(a.CommonAddr))
	if p.caSize() == 2 {
		b = append(b, byte(a.CommonAddr>>8))
	} else if a.CommonAddr > 0xFF {
		return nil, fmt.Errorf("%w: common address %d does not fit one octet", ErrInvalid, a.CommonAddr)
	}
	b = append(b, a.Record)
	b, err := a.Body.AppendTo(b, p)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", t, err)
	}
	if len(b)-start > MaxSize {
		return nil, fmt.Errorf("%w: %d octets, at most %d", ErrTooLong, len(b)-start, MaxSize)
	}
	return b, nil
}

// Decode decodes an ASDU.
//
// A type identification that neither p.Profile nor this package
// interprets decodes as [Raw]. So does a refusal (see [Cause.Refusal])
// whose objects do not fit the layout of its type, since stations need not
// mirror the request's objects in a refusal.
func Decode(b []byte, p Params) (*ASDU, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	hs := p.HeaderSize()
	if len(b) < hs {
		return nil, fmt.Errorf("%w: %d octets, the data unit identifier and record address need %d", ErrShort, len(b), hs)
	}
	if len(b) > MaxSize {
		return nil, fmt.Errorf("%w: %d octets", ErrTooLong, len(b))
	}
	a := &ASDU{Type: TypeID(b[0]), VSQ: VSQ(b[1]), Cause: Cause(b[2]), CommonAddr: uint16(b[3])}
	if p.caSize() == 2 {
		a.CommonAddr |= uint16(b[4]) << 8
	}
	a.Record = b[hs-1]
	data := b[hs:]
	var body Body
	var err error
	if p.Profile != nil {
		body, err = p.Profile.DecodeBody(a, data, p)
	}
	if body == nil && err == nil {
		body, err = decodeStandard(a, data)
	}
	switch {
	case err != nil && a.Cause.Refusal():
		body = nil
	case err != nil:
		return nil, fmt.Errorf("%s: %w", a.Type, err)
	}
	if body == nil {
		body = Raw{Data: append([]byte{}, data...)}
	}
	a.Body = body
	return a, nil
}

// Reader consumes octets of an information object field by field; the
// first shortfall sets Err and later reads return zero values. It is for
// [Profile] implementations.
type Reader struct {
	b   []byte
	Err error
}

// NewReader returns a Reader of b.
func NewReader(b []byte) *Reader { return &Reader{b: b} }

// Len returns the number of octets left.
func (r *Reader) Len() int { return len(r.b) }

// Take consumes n octets.
func (r *Reader) Take(n int) []byte {
	if r.Err != nil {
		return make([]byte, n)
	}
	if len(r.b) < n {
		r.Err = fmt.Errorf("%w: need %d octet(s), have %d", ErrShort, n, len(r.b))
		r.b = nil
		return make([]byte, n)
	}
	v := r.b[:n:n]
	r.b = r.b[n:]
	return v
}

// U8 consumes one octet.
func (r *Reader) U8() uint8 { return r.Take(1)[0] }

// U32 consumes a little-endian unsigned integer of n octets (1..4).
func (r *Reader) U32(n int) uint32 {
	var v uint32
	for i, c := range r.Take(n) {
		v |= uint32(c) << (8 * i)
	}
	return v
}

// TimeA consumes a [TimeA].
func (r *Reader) TimeA() TimeA { return parseTimeA(r.Take(SizeTimeA)) }

// TimeB consumes a [TimeB].
func (r *Reader) TimeB() TimeB { return parseTimeB(r.Take(SizeTimeB)) }

// Done returns Err, or [ErrTrailing] when octets are left.
func (r *Reader) Done() error {
	if r.Err != nil {
		return r.Err
	}
	if len(r.b) > 0 {
		return fmt.Errorf("%w: %d octet(s)", ErrTrailing, len(r.b))
	}
	return nil
}

// AppendUint appends v as a little-endian unsigned integer of n octets.
func AppendUint(b []byte, v uint32, n int) []byte {
	for i := 0; i < n; i++ {
		b = append(b, byte(v>>(8*i)))
	}
	return b
}
