// SPDX-License-Identifier: MIT

package iec102

import "fmt"

// Quality is the quality descriptor octet that follows each counter
// reading (Beckhoff TS650x manual, section 4.25.8): IV (bit 7), CA (bit
// 6), CY (bit 5) and a sequence number (bits 0-4). The REE profile gives
// bits 0-4 other meanings (package ree); the octet is kept as received.
type Quality uint8

// Bits of the quality descriptor (Beckhoff TS650x manual, sections 4.25.8
// and 4.26.32).
const (
	QualityInvalid  Quality = 0x80 // IV: the counter reading is not valid
	QualityAdjusted Quality = 0x40 // CA: the counter was adjusted
	QualityCarry    Quality = 0x20 // CY: carry (counter overflow) in the period
)

// QualityOf returns the descriptor with sequence number seq (0..31) and
// the given flags.
func QualityOf(seq uint8, flags Quality) Quality { return Quality(seq&0x1F) | flags&0xE0 }

// IV reports the invalid flag.
func (q Quality) IV() bool { return q&QualityInvalid != 0 }

// CA reports the adjusted flag.
func (q Quality) CA() bool { return q&QualityAdjusted != 0 }

// CY reports the carry flag.
func (q Quality) CY() bool { return q&QualityCarry != 0 }

// Seq returns the sequence number, bits 0-4.
func (q Quality) Seq() uint8 { return uint8(q & 0x1F) }

// IntegratedTotal is one information object of M_IT_TA_2 .. M_IT_TM_2:
// the information object address, the counter reading, the quality
// descriptor and, when the station sends one, the signature octet
// (Beckhoff TS650x manual, section 4.25.2 ff.: "Signature (optional)").
type IntegratedTotal struct {
	Addr      uint8   // integrated total address (information object address)
	Value     uint32  // counter reading CR4, CR3 or CR2, unsigned little-endian
	Quality   Quality // quality descriptor
	Signature uint8   // signature octet; encoded only when the body is Signed
}

// IntegratedTotals is the body of M_IT_TA_2 .. M_IT_TM_2: integrated
// totals of one integration period with the common time tag (time a) at
// the end of the ASDU (Beckhoff TS650x manual, sections 4.25.2 to
// 4.25.13).
type IntegratedTotals struct {
	// T is the type identification, M_IT_TA_2 .. M_IT_TM_2; it fixes the
	// counter width (4, 3 or 2 octets).
	T      TypeID
	Totals []IntegratedTotal
	// Signed reports objects with the optional signature octet. On decode
	// it is derived from the length; the manual does not define the
	// signature's computation and this package does not check it.
	Signed bool
	// Time is the end of the integration period (time a).
	Time TimeA
}

// Type returns T.
func (b IntegratedTotals) Type() TypeID { return b.T }

// Objects returns the number of totals.
func (b IntegratedTotals) Objects() int { return len(b.Totals) }

// Sequence marks a body with a variable number of objects.
func (IntegratedTotals) Sequence() {}

// AppendTo appends the totals and the time tag.
func (b IntegratedTotals) AppendTo(out []byte, _ Params) ([]byte, error) {
	w, ok := b.T.IntegratedTotalsType()
	if !ok {
		return nil, fmt.Errorf("%w: %s is not an integrated totals type", ErrInvalid, b.T)
	}
	for _, t := range b.Totals {
		if w < 4 && t.Value >= 1<<(8*w) {
			return nil, fmt.Errorf("%w: counter reading %d does not fit %d octets", ErrInvalid, t.Value, w)
		}
		out = AppendUint(append(out, t.Addr), t.Value, w)
		out = append(out, byte(t.Quality))
		if b.Signed {
			out = append(out, t.Signature)
		}
	}
	return b.Time.AppendTo(out), nil
}

func decodeTotals(a *ASDU, data []byte) (Body, error) {
	w, _ := a.Type.IntegratedTotalsType()
	n := a.VSQ.Number()
	plain, signed := n*(w+2)+SizeTimeA, n*(w+3)+SizeTimeA
	b := IntegratedTotals{T: a.Type}
	switch len(data) {
	case plain:
	case signed:
		b.Signed = true
	default:
		return nil, fmt.Errorf("%w: %d octets for %d object(s): want %d, or %d with signatures", ErrInvalid, len(data), n, plain, signed)
	}
	r := NewReader(data)
	b.Totals = make([]IntegratedTotal, n)
	for i := range b.Totals {
		t := &b.Totals[i]
		t.Addr = r.U8()
		t.Value = r.U32(w)
		t.Quality = Quality(r.U8())
		if b.Signed {
			t.Signature = r.U8()
		}
	}
	b.Time = r.TimeA()
	return b, r.Done()
}

// SinglePoint is one information object of M_SP_TA_2: single-point
// address (SPA), single-point information (SPI, bit 0), qualifier (SPQ,
// bits 1-7) and time b (Beckhoff TS650x manual, sections 4.25.1, 4.26.1,
// 4.26.2).
type SinglePoint struct {
	Addr uint8 // SPA: 0..127 compatible range, 128..255 private
	On   bool  // SPI
	// Qualifier is SPQ, 0..127: 0 not specified, 1..127 vendor specific.
	Qualifier uint8
	Time      TimeB
}

// SinglePoints is the body of M_SP_TA_2, single-point information with
// time tag (Beckhoff TS650x manual, section 4.25.1).
type SinglePoints struct{ Points []SinglePoint }

// Type returns M_SP_TA_2.
func (SinglePoints) Type() TypeID { return M_SP_TA_2 }

// Objects returns the number of points.
func (b SinglePoints) Objects() int { return len(b.Points) }

// Sequence marks a body with a variable number of objects.
func (SinglePoints) Sequence() {}

// AppendTo appends the points.
func (b SinglePoints) AppendTo(out []byte, _ Params) ([]byte, error) {
	for _, p := range b.Points {
		v := p.Qualifier << 1
		if p.On {
			v |= 1
		}
		out = p.Time.AppendTo(append(out, p.Addr, v))
	}
	return out, nil
}

func decodeSinglePoints(a *ASDU, data []byte) (Body, error) {
	r := NewReader(data)
	b := SinglePoints{Points: make([]SinglePoint, a.VSQ.Number())}
	for i := range b.Points {
		p := &b.Points[i]
		p.Addr = r.U8()
		v := r.U8()
		p.On, p.Qualifier = v&1 != 0, v>>1
		p.Time = r.TimeB()
	}
	return b, r.Done()
}

// ProductSpec is the body of P_MP_NA_2, the manufacturer and product
// specification of the integrated total DTE: one octet of standard
// edition, the manufacturer code and a four-octet product code.
//
// The layout is not in the Beckhoff manual; it is the one of
// gisce/iec870ree (P_MP_NA_2) and its test vector, which read the first
// octet as month (bits 0-3) and year since 2000 (bits 4-7) of the standard
// edition. Confidence is low; see docs/iec102/INTEROPERABILITY.md.
type ProductSpec struct {
	Standard     uint8
	Manufacturer uint8
	Product      uint32
}

// Type returns P_MP_NA_2.
func (ProductSpec) Type() TypeID { return P_MP_NA_2 }

// Objects returns 1.
func (ProductSpec) Objects() int { return 1 }

// AppendTo appends the six octets.
func (b ProductSpec) AppendTo(out []byte, _ Params) ([]byte, error) {
	return AppendUint(append(out, b.Standard, b.Manufacturer), b.Product, 4), nil
}

// SystemTime is the body of M_TI_TA_2, the current system time of the
// integrated total DTE, in time b. The layout is not in the Beckhoff
// manual; gisce/iec870ree and daiiniel/IEC60870-5-102 both read one time b.
type SystemTime struct{ Time TimeB }

// Type returns M_TI_TA_2.
func (SystemTime) Type() TypeID { return M_TI_TA_2 }

// Objects returns 1.
func (SystemTime) Objects() int { return 1 }

// AppendTo appends the time.
func (b SystemTime) AppendTo(out []byte, _ Params) ([]byte, error) { return b.Time.AppendTo(out), nil }

// Read is the body of the read commands C_RD_NA_2 .. C_CI_NU_2 (types
// 100..123, Beckhoff TS650x manual, sections 4.22.6 to 4.22.29). Which
// fields travel depends on T:
//
//   - C_RD_NA_2, C_SP_NA_2, C_TI_NA_2 and the "oldest integration period"
//     reads (104, 108, 112, 116): nothing (the record address selects);
//   - C_SP_NB_2: From and To;
//   - the "oldest period and range of addresses" reads (105, 109, 113,
//     117): First and Last;
//   - the "specific past period" reads (106, 110, 114, 118): From, the
//     time of the period;
//   - the "specific past period and range of addresses" reads (107, 111,
//     115, 119): First, Last and From;
//   - the time range reads C_CI_NR_2 .. C_CI_NU_2: First, Last, From, To.
type Read struct {
	T           TypeID
	First, Last uint8 // integrated total addresses, from and to
	From, To    TimeA
}

// Type returns T.
func (b Read) Type() TypeID { return b.T }

// Objects returns 0 for a read without information object, 1 otherwise:
// the number of objects of the Beckhoff layouts (nObj).
func (b Read) Objects() int {
	if readLen(b.T) == 0 {
		return 0
	}
	return 1
}

// readLen returns the length of the object of a read command, -1 for a
// type that is not one.
func readLen(t TypeID) int {
	switch t {
	case C_RD_NA_2, C_SP_NA_2, C_TI_NA_2:
		return 0
	case C_SP_NB_2:
		return 2 * SizeTimeA
	default:
	}
	switch t.Selection() {
	case SelectOldest:
		return 0
	case SelectOldestRange:
		return 2
	case SelectPeriod:
		return SizeTimeA
	case SelectPeriodRange:
		return 2 + SizeTimeA
	case SelectTimeRange:
		return 2 + 2*SizeTimeA
	default:
		return -1
	}
}

// AppendTo appends the fields T carries.
func (b Read) AppendTo(out []byte, _ Params) ([]byte, error) {
	switch {
	case readLen(b.T) < 0:
		return nil, fmt.Errorf("%w: %s is not a read command", ErrInvalid, b.T)
	case b.T == C_SP_NB_2:
		return b.To.AppendTo(b.From.AppendTo(out)), nil
	}
	sel := b.T.Selection()
	if sel == SelectOldestRange || sel == SelectPeriodRange || sel == SelectTimeRange {
		out = append(out, b.First, b.Last)
	}
	if sel == SelectPeriod || sel == SelectPeriodRange || sel == SelectTimeRange {
		out = b.From.AppendTo(out)
	}
	if sel == SelectTimeRange {
		out = b.To.AppendTo(out)
	}
	return out, nil
}

func decodeRead(a *ASDU, data []byte) (Body, error) {
	b := Read{T: a.Type}
	r := NewReader(data)
	if a.Type == C_SP_NB_2 {
		b.From, b.To = r.TimeA(), r.TimeA()
		return b, r.Done()
	}
	sel := a.Type.Selection()
	if sel == SelectOldestRange || sel == SelectPeriodRange || sel == SelectTimeRange {
		b.First, b.Last = r.U8(), r.U8()
	}
	if sel == SelectPeriod || sel == SelectPeriodRange || sel == SelectTimeRange {
		b.From = r.TimeA()
	}
	if sel == SelectTimeRange {
		b.To = r.TimeA()
	}
	return b, r.Done()
}

// decodeStandard decodes the body of a standard type; nil for a type
// without a known layout.
func decodeStandard(a *ASDU, data []byte) (Body, error) {
	switch {
	case a.Type == M_SP_TA_2 && !a.VSQ.SQ():
		return decodeSinglePoints(a, data)
	case a.Type >= M_IT_TA_2 && a.Type <= M_IT_TM_2 && !a.VSQ.SQ():
		return decodeTotals(a, data)
	case a.Type == P_MP_NA_2:
		r := NewReader(data)
		b := ProductSpec{Standard: r.U8(), Manufacturer: r.U8(), Product: r.U32(4)}
		return b, r.Done()
	case a.Type == M_TI_TA_2:
		r := NewReader(data)
		b := SystemTime{Time: r.TimeB()}
		return b, r.Done()
	case readLen(a.Type) >= 0:
		return decodeRead(a, data)
	}
	return nil, nil
}
