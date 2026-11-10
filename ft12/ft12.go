// SPDX-License-Identifier: MIT

// Package ft12 encodes and decodes the FT 1.2 frame format of
// IEC 60870-5-1 (subclause 6.2.4.2), the serial framing shared by
// IEC 60870-5-101, -102 and -103:
//
//   - single character: E5h
//   - fixed length: 10h, C, A, CS, 16h
//   - variable length: 68h, L, L, 68h, C, A, user data, CS, 16h
//
// C is the link control field and A the link address of 0, 1 or 2 octets
// (a system parameter; 2-octet addresses are little-endian). CS is the
// arithmetic sum modulo 256 of C, A and the user data. L counts C, A and
// the user data and is at most 255.
//
// This package knows the octets only; the meaning of the control field and
// the link procedures of IEC 60870-5-2 are in package link.
//
// FT 1.2 also requires that a frame is sent without gaps between its
// characters and that a receiver discards a frame with a gap (IEC 60870-5-1
// 6.2.4.2, rule R2/R4). A [Decoder] on a generic io.Reader cannot see
// character timing: it validates structure and checksum, and leaves idle
// line detection to the transport (see link.Config).
package ft12

import (
	"errors"
	"fmt"
)

// Start, stop and single control characters.
const (
	StartFixed    byte = 0x10
	StartVariable byte = 0x68
	Stop          byte = 0x16
	SingleChar    byte = 0xE5
)

// MaxLength is the largest L of a variable length frame.
const MaxLength = 255

// Errors. They are wrapped with detail; test with errors.Is.
var (
	// ErrInvalidParams reports a link address size other than 0, 1 or 2.
	ErrInvalidParams = errors.New("ft12: invalid parameters")
	// ErrMalformed reports octets that are not an FT 1.2 frame: a wrong
	// start or stop character, the two length octets differ, the length is
	// too small for the control and address fields.
	ErrMalformed = errors.New("ft12: malformed frame")
	// ErrChecksum reports a frame whose checksum does not match.
	ErrChecksum = errors.New("ft12: checksum mismatch")
	// ErrTooLong reports user data that does not fit one frame.
	ErrTooLong = errors.New("ft12: frame too long")
	// ErrInvalidAddress reports a link address wider than the configured
	// address field.
	ErrInvalidAddress = errors.New("ft12: link address out of range")
	// ErrShort reports a buffer that ends before the frame does.
	ErrShort = errors.New("ft12: truncated frame")
)

// Kind is the frame format.
type Kind uint8

// Frame formats.
const (
	// KindSingle is the single control character E5h.
	KindSingle Kind = iota + 1
	// KindFixed is the fixed length frame without user data.
	KindFixed
	// KindVariable is the variable length frame with user data.
	KindVariable
)

// String returns "single", "fixed" or "variable".
func (k Kind) String() string {
	switch k {
	case KindSingle:
		return "single"
	case KindFixed:
		return "fixed"
	case KindVariable:
		return "variable"
	}
	return fmt.Sprintf("Kind(%d)", uint8(k))
}

// Params are the FT 1.2 system parameters both stations agree on.
type Params struct {
	// AddrSize is the size of the link address field in octets: 0, 1 or
	// 2. IEC 60870-5-101 allows 0 for balanced transmission only;
	// IEC 60870-5-103 uses 1.
	AddrSize int
}

// Validate reports whether the parameters are allowed.
func (p Params) Validate() error {
	if p.AddrSize < 0 || p.AddrSize > 2 {
		return fmt.Errorf("%w: link address size %d, want 0, 1 or 2", ErrInvalidParams, p.AddrSize)
	}
	return nil
}

// MaxAddr returns the largest link address the address field holds.
func (p Params) MaxAddr() uint16 {
	switch p.AddrSize {
	case 1:
		return 0xFF
	case 2:
		return 0xFFFF
	}
	return 0
}

// MaxData returns the largest user data a variable length frame carries.
func (p Params) MaxData() int { return MaxLength - 1 - p.AddrSize }

// Frame is one FT 1.2 frame.
type Frame struct {
	Kind Kind
	// Control is the link control field C (not used by KindSingle).
	Control byte
	// Addr is the link address A (not used by KindSingle, and 0 when the
	// address size is 0).
	Addr uint16
	// Data is the user data of a KindVariable frame: the ASDU. A decoded
	// frame's Data aliases the decoder's buffer until the next decode;
	// copy it to keep it.
	Data []byte
}

// String describes the frame for logs.
func (f Frame) String() string {
	switch f.Kind {
	case KindSingle:
		return "E5"
	case KindFixed:
		return fmt.Sprintf("fixed C=%02X A=%d", f.Control, f.Addr)
	case KindVariable:
		return fmt.Sprintf("variable C=%02X A=%d len=%d", f.Control, f.Addr, len(f.Data))
	}
	return fmt.Sprintf("Frame(kind %d)", uint8(f.Kind))
}

// Size returns the encoded size of f under p.
func (f Frame) Size(p Params) int {
	switch f.Kind {
	case KindSingle:
		return 1
	case KindFixed:
		return 4 + p.AddrSize
	default:
		return 7 + p.AddrSize + len(f.Data)
	}
}

func checksum(b []byte) byte {
	var s byte
	for _, c := range b {
		s += c
	}
	return s
}

// Append appends the encoding of f under p to b.
func (f Frame) Append(b []byte, p Params) ([]byte, error) {
	if err := p.Validate(); err != nil {
		return b, err
	}
	switch f.Kind {
	case KindSingle:
		return append(b, SingleChar), nil
	case KindFixed, KindVariable:
	default:
		return b, fmt.Errorf("%w: unknown kind %d", ErrMalformed, f.Kind)
	}
	if f.Addr > p.MaxAddr() {
		return b, fmt.Errorf("%w: %d does not fit %d octet(s)", ErrInvalidAddress, f.Addr, p.AddrSize)
	}
	if f.Kind == KindVariable {
		if len(f.Data) > p.MaxData() {
			return b, fmt.Errorf("%w: %d octets of user data, at most %d", ErrTooLong, len(f.Data), p.MaxData())
		}
		l := byte(1 + p.AddrSize + len(f.Data))
		b = append(b, StartVariable, l, l, StartVariable)
	} else {
		b = append(b, StartFixed)
	}
	body := len(b)
	b = append(b, f.Control)
	switch p.AddrSize {
	case 1:
		b = append(b, byte(f.Addr))
	case 2:
		b = append(b, byte(f.Addr), byte(f.Addr>>8))
	}
	if f.Kind == KindVariable {
		b = append(b, f.Data...)
	}
	return append(b, checksum(b[body:]), Stop), nil
}

// Encode returns the encoding of f under p.
func (f Frame) Encode(p Params) ([]byte, error) { return f.Append(make([]byte, 0, f.Size(p)), p) }

// Decode decodes the frame at the start of b and returns it with the
// number of octets it occupies. It returns [ErrShort] when b ends before
// the frame does (more octets may complete it), and [ErrMalformed] or
// [ErrChecksum] when b does not start with a valid frame.
func Decode(b []byte, p Params) (Frame, int, error) {
	if err := p.Validate(); err != nil {
		return Frame{}, 0, err
	}
	if len(b) == 0 {
		return Frame{}, 0, ErrShort
	}
	switch b[0] {
	case SingleChar:
		return Frame{Kind: KindSingle}, 1, nil
	case StartFixed:
		n := 4 + p.AddrSize
		if len(b) < n {
			return Frame{}, 0, ErrShort
		}
		f, err := body(KindFixed, b[1:n-2], b[n-2], b[n-1], p)
		return f, n, err
	case StartVariable:
		if len(b) < 4 {
			return Frame{}, 0, ErrShort
		}
		if b[1] != b[2] || b[3] != StartVariable {
			return Frame{}, 0, fmt.Errorf("%w: variable frame header % X", ErrMalformed, b[:4])
		}
		l := int(b[1])
		if l < 1+p.AddrSize {
			return Frame{}, 0, fmt.Errorf("%w: length %d below control and address size", ErrMalformed, l)
		}
		n := 6 + l
		if len(b) < n {
			return Frame{}, 0, ErrShort
		}
		f, err := body(KindVariable, b[4:4+l], b[n-2], b[n-1], p)
		return f, n, err
	}
	return Frame{}, 0, fmt.Errorf("%w: start character %02X", ErrMalformed, b[0])
}

func body(k Kind, ca []byte, cs, stop byte, p Params) (Frame, error) {
	if stop != Stop {
		return Frame{}, fmt.Errorf("%w: stop character %02X", ErrMalformed, stop)
	}
	if got := checksum(ca); got != cs {
		return Frame{}, fmt.Errorf("%w: computed %02X, frame has %02X", ErrChecksum, got, cs)
	}
	f := Frame{Kind: k, Control: ca[0]}
	switch p.AddrSize {
	case 1:
		f.Addr = uint16(ca[1])
	case 2:
		f.Addr = uint16(ca[1]) | uint16(ca[2])<<8
	}
	if k == KindVariable {
		f.Data = ca[1+p.AddrSize:]
	}
	return f, nil
}
