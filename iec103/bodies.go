// SPDX-License-Identifier: MIT

package iec103

import (
	"fmt"
	"time"

	"github.com/otfabric/go-iec60870-5/cp"
)

// Element limits of the compatible ASDUs.
const (
	MaxMeasurands1  = 4  // ASDU 3: up to four MEA (clause 7.3.1.3)
	MaxMeasurands2  = 9  // ASDU 9: up to nine MEA (clause 7.3.1.8)
	MaxDisturbances = 8  // ASDU 23: at most eight recorded disturbances (clause 7.3.1.11)
	MaxTags         = 25 // ASDU 29: at most 25 tags (clause 7.3.1.15)
	MaxValues       = 25 // ASDU 30: at most 25 values (clause 7.2.6.14, 7.3.1.16)
)

// TimeTagged is ASDU 1, the time-tagged message (clause 7.3.1.1): DPI,
// CP32Time2a, SIN. In an acknowledgement of a command DPI is irrelevant and
// SIN carries the RII of the command; in an answer to a general
// interrogation SIN carries its SCN (clause 7.2.6.23).
type TimeTagged struct {
	DPI  DPI
	Time cp.Timestamp
	SIN  uint8
}

// Type returns [TypeTimeTagged].
func (TimeTagged) Type() TypeID { return TypeTimeTagged }
func (TimeTagged) vsq() VSQ     { return VSQSingle }
func (m TimeTagged) append(b []byte, loc *time.Location) ([]byte, error) {
	b = append(b, byte(m.DPI&3))
	return append(appendCP32(b, m.Time, loc), m.SIN), nil
}

// RelTimeTagged is ASDU 2, the time-tagged message with relative time
// (clause 7.3.1.2): DPI, RET, FAN, CP32Time2a, SIN.
type RelTimeTagged struct {
	DPI  DPI
	RET  uint16 // relative time in ms (clause 7.2.6.15)
	FAN  uint16 // fault number (clause 7.2.6.6)
	Time cp.Timestamp
	SIN  uint8
}

// Type returns [TypeRelTimeTagged].
func (RelTimeTagged) Type() TypeID { return TypeRelTimeTagged }
func (RelTimeTagged) vsq() VSQ     { return VSQSingle }
func (m RelTimeTagged) append(b []byte, loc *time.Location) ([]byte, error) {
	b = appendU16(appendU16(append(b, byte(m.DPI&3)), m.RET), m.FAN)
	return append(appendCP32(b, m.Time, loc), m.SIN), nil
}

// Measurands is ASDU 3 (measurands I, clause 7.3.1.3; Set2 false) or ASDU 9
// (measurands II, clause 7.3.1.8; Set2 true). The meaning of the values
// depends on the information number (table 14); ASDU 9 always starts with
// IL1, so fewer values leave out the last ones.
type Measurands struct {
	Set2   bool
	Values []MEA
}

// Type returns [TypeMeasurands1] or [TypeMeasurands2].
func (m Measurands) Type() TypeID {
	if m.Set2 {
		return TypeMeasurands2
	}
	return TypeMeasurands1
}

func (m Measurands) vsq() VSQ { return NewVSQ(len(m.Values), false) }

func (m Measurands) max() int {
	if m.Set2 {
		return MaxMeasurands2
	}
	return MaxMeasurands1
}

func (m Measurands) append(b []byte, _ *time.Location) ([]byte, error) {
	if len(m.Values) == 0 || len(m.Values) > m.max() {
		return nil, fmt.Errorf("%w: %d measurands, %s carries 1..%d", ErrInvalid, len(m.Values), m.Type(), m.max())
	}
	for _, v := range m.Values {
		b = appendU16(b, v.word())
	}
	return b, nil
}

// RelTimeMeasurand is ASDU 4, the time-tagged measurand with relative time
// (clause 7.3.1.4): SCL, RET, FAN, CP32Time2a.
type RelTimeMeasurand struct {
	SCL  float32 // short-circuit location, reactance in primary ohms (clause 7.2.6.20)
	RET  uint16
	FAN  uint16
	Time cp.Timestamp
}

// Type returns [TypeRelTimeMeasurand].
func (RelTimeMeasurand) Type() TypeID { return TypeRelTimeMeasurand }
func (RelTimeMeasurand) vsq() VSQ     { return VSQSingle }
func (m RelTimeMeasurand) append(b []byte, loc *time.Location) ([]byte, error) {
	b = appendU16(appendU16(appendF32(b, m.SCL), m.RET), m.FAN)
	return appendCP32(b, m.Time, loc), nil
}

// Identification is ASDU 5 (clause 7.3.1.5): the compatibility level, eight
// ASCII characters naming the manufacturer (blank filled with 20H) and four
// octets of free use for the software identification.
type Identification struct {
	COL      uint8
	ASCII    [8]byte
	Software [4]byte
}

// Manufacturer returns the eight characters as a string.
func (m Identification) Manufacturer() string { return string(m.ASCII[:]) }

// IdentificationOf returns an identification with name padded with blanks
// (clause 7.3.1.5) or cut to eight characters.
func IdentificationOf(col uint8, name string, software [4]byte) Identification {
	id := Identification{COL: col, Software: software}
	for i := range id.ASCII {
		id.ASCII[i] = ' '
		if i < len(name) {
			id.ASCII[i] = name[i]
		}
	}
	return id
}

// Type returns [TypeIdentification].
func (Identification) Type() TypeID { return TypeIdentification }
func (Identification) vsq() VSQ     { return VSQSingle }
func (m Identification) append(b []byte, _ *time.Location) ([]byte, error) {
	return append(append(append(b, m.COL), m.ASCII[:]...), m.Software[:]...), nil
}

// TimeSync is ASDU 6, time synchronization (clauses 7.3.1.6 and 7.3.2.1):
// CP56Time2a. In the control direction the IV bit is irrelevant.
type TimeSync struct{ Time cp.Timestamp }

// Type returns [TypeTimeSync].
func (TimeSync) Type() TypeID { return TypeTimeSync }
func (TimeSync) vsq() VSQ     { return VSQSingle }
func (m TimeSync) append(b []byte, loc *time.Location) ([]byte, error) {
	return appendCP56(b, m.Time, loc), nil
}

// GIInit is ASDU 7, the initiation of general interrogation (clause
// 7.3.2.2), with its scan number.
type GIInit struct{ SCN uint8 }

// Type returns [TypeGIInit].
func (GIInit) Type() TypeID { return TypeGIInit }
func (GIInit) vsq() VSQ     { return VSQSingle }
func (m GIInit) append(b []byte, _ *time.Location) ([]byte, error) {
	return append(b, m.SCN), nil
}

// GITermination is ASDU 8, the termination of general interrogation
// (clause 7.3.1.7), with the scan number of the initiation.
type GITermination struct{ SCN uint8 }

// Type returns [TypeGITermination].
func (GITermination) Type() TypeID { return TypeGITermination }
func (GITermination) vsq() VSQ     { return VSQSingle }
func (m GITermination) append(b []byte, _ *time.Location) ([]byte, error) {
	return append(b, m.SCN), nil
}

// GenericData is ASDU 10, generic data (clauses 7.3.1.9 and 7.3.2.3): RII,
// NGD and the data sets. NGD's number is len(Sets), at most [MaxCounted].
type GenericData struct {
	RII  uint8
	NGD  Counted
	Sets []DataSet
}

// Type returns [TypeGenericData].
func (GenericData) Type() TypeID { return TypeGenericData }
func (GenericData) vsq() VSQ     { return VSQSingle }
func (m GenericData) append(b []byte, _ *time.Location) ([]byte, error) {
	if len(m.Sets) > MaxCounted {
		return nil, fmt.Errorf("%w: %d data sets, NGD carries at most %d", ErrInvalid, len(m.Sets), MaxCounted)
	}
	b = append(b, m.RII, m.NGD.octet(len(m.Sets)))
	var err error
	for _, s := range m.Sets {
		b = append(b, s.GIN.Group, s.GIN.Entry, byte(s.KOD))
		if b, err = appendValue(b, s.Value); err != nil {
			return nil, err
		}
	}
	return b, nil
}

// GenericIdent is ASDU 11, generic identification (clause 7.3.1.10): RII,
// GIN, NDE and the description elements of that GIN. NDE's number is
// len(Elements), 1..[MaxCounted] (clause 7.2.6.35).
type GenericIdent struct {
	RII      uint8
	GIN      GIN
	NDE      Counted
	Elements []DescElement
}

// Type returns [TypeGenericIdent].
func (GenericIdent) Type() TypeID { return TypeGenericIdent }
func (GenericIdent) vsq() VSQ     { return VSQSingle }
func (m GenericIdent) append(b []byte, _ *time.Location) ([]byte, error) {
	if len(m.Elements) == 0 || len(m.Elements) > MaxCounted {
		return nil, fmt.Errorf("%w: %d description elements, NDE carries 1..%d (clause 7.2.6.35)", ErrInvalid, len(m.Elements), MaxCounted)
	}
	b = append(b, m.RII, m.GIN.Group, m.GIN.Entry, m.NDE.octet(len(m.Elements)))
	var err error
	for _, e := range m.Elements {
		b = append(b, byte(e.KOD))
		if b, err = appendValue(b, e.Value); err != nil {
			return nil, err
		}
	}
	return b, nil
}

// GeneralCommand is ASDU 20, the general command (clause 7.3.2.4): DCO and
// RII. The RII returns as SIN of the acknowledgement (clause 7.4.4).
type GeneralCommand struct {
	DCO DPI
	RII uint8
}

// Type returns [TypeGeneralCommand].
func (GeneralCommand) Type() TypeID { return TypeGeneralCommand }
func (GeneralCommand) vsq() VSQ     { return VSQSingle }
func (m GeneralCommand) append(b []byte, _ *time.Location) ([]byte, error) {
	return append(b, byte(m.DCO&3), m.RII), nil
}

// GenericCommand is ASDU 21, the generic command (clause 7.3.2.5): RII,
// NOG and the generic identifiers. NOG is len(Items).
type GenericCommand struct {
	RII   uint8
	Items []Item
}

// Type returns [TypeGenericCommand].
func (GenericCommand) Type() TypeID { return TypeGenericCommand }
func (GenericCommand) vsq() VSQ     { return VSQSingle }
func (m GenericCommand) append(b []byte, _ *time.Location) ([]byte, error) {
	if len(m.Items) > 255 {
		return nil, fmt.Errorf("%w: %d generic identifiers, NOG carries at most 255", ErrInvalid, len(m.Items))
	}
	b = append(b, m.RII, byte(len(m.Items)))
	for _, it := range m.Items {
		b = append(b, it.GIN.Group, it.GIN.Entry, byte(it.KOD))
	}
	return b, nil
}

// RecordedFault is one entry of the list of recorded disturbances: FAN,
// SOF and the CP56Time2a of the fault (clause 7.3.1.11).
type RecordedFault struct {
	FAN  uint16
	SOF  SOF
	Time cp.Timestamp
}

// DisturbanceList is ASDU 23, the list of recorded disturbances (clause
// 7.3.1.11), at most [MaxDisturbances]; an empty list is an empty
// directory.
type DisturbanceList struct{ Faults []RecordedFault }

// Type returns [TypeDisturbanceList].
func (DisturbanceList) Type() TypeID { return TypeDisturbanceList }
func (m DisturbanceList) vsq() VSQ   { return NewVSQ(len(m.Faults), false) }
func (m DisturbanceList) append(b []byte, loc *time.Location) ([]byte, error) {
	if len(m.Faults) > MaxDisturbances {
		return nil, fmt.Errorf("%w: %d recorded disturbances, at most %d", ErrInvalid, len(m.Faults), MaxDisturbances)
	}
	for _, f := range m.Faults {
		b = appendCP56(append(appendU16(b, f.FAN), byte(f.SOF)), f.Time, loc)
	}
	return b, nil
}

// DisturbanceOrder is ASDU 24, the order for disturbance data transmission
// (Ack false, clause 7.3.2.6), or ASDU 25, its acknowledgement (Ack true,
// clause 7.3.2.7): TOO, TOV, FAN, ACC.
type DisturbanceOrder struct {
	Ack bool
	TOO TOO
	TOV uint8
	FAN uint16
	ACC uint8
}

// Type returns [TypeDisturbanceOrder] or [TypeDisturbanceAck].
func (m DisturbanceOrder) Type() TypeID {
	if m.Ack {
		return TypeDisturbanceAck
	}
	return TypeDisturbanceOrder
}

func (DisturbanceOrder) vsq() VSQ { return VSQSingle }
func (m DisturbanceOrder) append(b []byte, _ *time.Location) ([]byte, error) {
	return append(appendU16(append(b, byte(m.TOO), m.TOV), m.FAN), m.ACC), nil
}

// DisturbanceReady is ASDU 26, ready for transmission of disturbance data
// (clause 7.3.1.12): TOV, FAN, NOF, NOC, NOE, INT and the time tag of the
// first value.
type DisturbanceReady struct {
	TOV  uint8
	FAN  uint16
	NOF  uint16 // network fault number (clause 7.2.6.12)
	NOC  uint8  // number of channels (clause 7.2.6.10)
	NOE  uint16 // values per channel (clause 7.2.6.11)
	INT  uint16 // interval between values in µs (clause 7.2.6.7)
	Time cp.Timestamp
}

// Type returns [TypeDisturbanceReady].
func (DisturbanceReady) Type() TypeID { return TypeDisturbanceReady }
func (DisturbanceReady) vsq() VSQ     { return VSQSingle }
func (m DisturbanceReady) append(b []byte, loc *time.Location) ([]byte, error) {
	b = append(b, 0, m.TOV) // not used
	b = appendU16(append(appendU16(appendU16(b, m.FAN), m.NOF), m.NOC), m.NOE)
	return appendCP32(appendU16(b, m.INT), m.Time, loc), nil
}

// ChannelReady is ASDU 27, ready for transmission of a channel (clause
// 7.3.1.13): TOV, FAN, ACC, RPV, RSV, RFA.
type ChannelReady struct {
	TOV uint8
	FAN uint16
	ACC uint8
	RPV float32 // rated primary value (clause 7.2.6.17)
	RSV float32 // rated secondary value (clause 7.2.6.18)
	RFA float32 // reference factor: relative value / secondary value (clause 7.2.6.16)
}

// Type returns [TypeChannelReady].
func (ChannelReady) Type() TypeID { return TypeChannelReady }
func (ChannelReady) vsq() VSQ     { return VSQSingle }
func (m ChannelReady) append(b []byte, _ *time.Location) ([]byte, error) {
	b = append(appendU16(append(b, 0, m.TOV), m.FAN), m.ACC)
	return appendF32(appendF32(appendF32(b, m.RPV), m.RSV), m.RFA), nil
}

// TagsReady is ASDU 28, ready for transmission of tags (clause 7.3.1.14):
// only the FAN is significant.
type TagsReady struct{ FAN uint16 }

// Type returns [TypeTagsReady].
func (TagsReady) Type() TypeID { return TypeTagsReady }
func (TagsReady) vsq() VSQ     { return VSQSingle }
func (m TagsReady) append(b []byte, _ *time.Location) ([]byte, error) {
	return appendU16(append(b, 0, 0), m.FAN), nil
}

// Tag is the state of one binary signal in a disturbance record: its FUN
// and INF and the DPI (clause 7.3.1.15).
type Tag struct {
	Fun FunctionType
	Inf InfoNumber
	DPI DPI
}

// Tags is ASDU 29, the transmission of tags (clause 7.3.1.15): FAN, NOT,
// TAP and up to [MaxTags] tags, all at position TAP.
type Tags struct {
	FAN  uint16
	TAP  uint16 // tag position: values from the first of the record (clause 7.2.6.25)
	Tags []Tag
}

// Type returns [TypeTags].
func (Tags) Type() TypeID { return TypeTags }
func (Tags) vsq() VSQ     { return VSQSingle }
func (m Tags) append(b []byte, _ *time.Location) ([]byte, error) {
	if len(m.Tags) > MaxTags {
		return nil, fmt.Errorf("%w: %d tags, at most %d", ErrInvalid, len(m.Tags), MaxTags)
	}
	b = appendU16(append(appendU16(b, m.FAN), byte(len(m.Tags))), m.TAP)
	for _, t := range m.Tags {
		b = append(b, byte(t.Fun), byte(t.Inf), byte(t.DPI&3))
	}
	return b, nil
}

// DisturbanceValues is ASDU 30, the transmission of disturbance values
// (clause 7.3.1.16): TOV, FAN, ACC, NDV, NFE and up to [MaxValues] SDV.
// SDV is a fraction F16 (clause 7.2.6.22), held as the integer value
// (divide by 32768).
type DisturbanceValues struct {
	TOV    uint8
	FAN    uint16
	ACC    uint8
	NFE    uint16 // number of the first value in this ASDU (clause 7.2.6.9)
	Values []int16
}

// Type returns [TypeDisturbanceValues].
func (DisturbanceValues) Type() TypeID { return TypeDisturbanceValues }
func (DisturbanceValues) vsq() VSQ     { return VSQSingle }
func (m DisturbanceValues) append(b []byte, _ *time.Location) ([]byte, error) {
	if len(m.Values) > MaxValues {
		return nil, fmt.Errorf("%w: %d disturbance values, at most %d", ErrInvalid, len(m.Values), MaxValues)
	}
	b = append(appendU16(append(b, 0, m.TOV), m.FAN), m.ACC, byte(len(m.Values)))
	b = appendU16(b, m.NFE)
	for _, v := range m.Values {
		b = appendU16(b, uint16(v))
	}
	return b, nil
}

// EndOfTransmission is ASDU 31, the end of transmission (clause 7.3.1.17):
// TOO (32..40), TOV, FAN, ACC.
type EndOfTransmission struct {
	TOO TOO
	TOV uint8
	FAN uint16
	ACC uint8
}

// Type returns [TypeEndOfTransmission].
func (EndOfTransmission) Type() TypeID { return TypeEndOfTransmission }
func (EndOfTransmission) vsq() VSQ     { return VSQSingle }
func (m EndOfTransmission) append(b []byte, _ *time.Location) ([]byte, error) {
	return append(appendU16(append(b, byte(m.TOO), m.TOV), m.FAN), m.ACC), nil
}

// checkVSQ checks the variable structure qualifier against clause 7.2.2
// and the structures of clause 7.3: the ASDUs with a sequence of elements
// (3, 9 and 23) have SQ = 0 and the number of elements, every other
// compatible ASDU has SQ = 1 and number 1 (VSQ = 81H).
//
// On receive a single-object ASDU with SQ = 0 and number 1 (VSQ = 01H) is
// accepted: the ARIEC103 test vectors and Wireshark's generated IEC 103
// frames use it, and the object is unambiguous. Encode writes the
// conformant 81H. A number other than 1 is refused.
func checkVSQ(t TypeID, vsq VSQ) error {
	switch t {
	case TypeMeasurands1, TypeMeasurands2, TypeDisturbanceList:
		if vsq.SQ() {
			return fmt.Errorf("%w: VSQ %02XH: %s has SQ = 0 (clause 7.3)", ErrInvalid, uint8(vsq), t)
		}
	default:
		if vsq&0x7F != 1 {
			return fmt.Errorf("%w: VSQ %02XH: %s has VSQ 81H (clause 7.3)", ErrInvalid, uint8(vsq), t)
		}
	}
	return nil
}

// decodeBody reads the body of a type identification of tables 3 and 4.
// The "not used" octets of ASDUs 26..30 are skipped whatever their value.
func decodeBody(t TypeID, vsq VSQ, r *reader) (Body, error) {
	if err := checkVSQ(t, vsq); err != nil {
		return nil, err
	}
	switch t {
	case TypeTimeTagged:
		return TimeTagged{DPI: r.dpi(), Time: r.cp32(), SIN: r.u8()}, nil
	case TypeRelTimeTagged:
		return RelTimeTagged{DPI: r.dpi(), RET: r.u16(), FAN: r.u16(), Time: r.cp32(), SIN: r.u8()}, nil
	case TypeMeasurands1, TypeMeasurands2:
		m := Measurands{Set2: t == TypeMeasurands2}
		n := vsq.Number()
		if n == 0 || n > m.max() {
			return nil, fmt.Errorf("%w: VSQ announces %d measurands, 1..%d allowed", ErrInvalid, n, m.max())
		}
		for range n {
			m.Values = append(m.Values, meaOf(r.u16()))
		}
		return m, nil
	case TypeRelTimeMeasurand:
		return RelTimeMeasurand{SCL: r.f32(), RET: r.u16(), FAN: r.u16(), Time: r.cp32()}, nil
	case TypeIdentification:
		var m Identification
		m.COL = r.u8()
		copy(m.ASCII[:], r.take(8))
		copy(m.Software[:], r.take(4))
		return m, nil
	case TypeTimeSync:
		return TimeSync{Time: r.cp56()}, nil
	case TypeGIInit:
		return GIInit{SCN: r.u8()}, nil
	case TypeGITermination:
		return GITermination{SCN: r.u8()}, nil
	case TypeGenericData:
		m := GenericData{RII: r.u8()}
		ngd, n := countedOf(r.u8())
		m.NGD = ngd
		for range n {
			s := DataSet{GIN: r.gin(), KOD: KOD(r.u8())}
			v, err := r.value()
			if err != nil {
				return nil, err
			}
			s.Value = v
			m.Sets = append(m.Sets, s)
		}
		return m, nil
	case TypeGenericIdent:
		m := GenericIdent{RII: r.u8(), GIN: r.gin()}
		nde, n := countedOf(r.u8())
		m.NDE = nde
		if r.err == nil && n == 0 {
			return nil, fmt.Errorf("%w: NDE 0, clause 7.2.6.35 requires 1..%d", ErrInvalid, MaxCounted)
		}
		for range n {
			e := DescElement{KOD: KOD(r.u8())}
			v, err := r.value()
			if err != nil {
				return nil, err
			}
			e.Value = v
			m.Elements = append(m.Elements, e)
		}
		return m, nil
	case TypeGeneralCommand:
		return GeneralCommand{DCO: r.dpi(), RII: r.u8()}, nil
	case TypeGenericCommand:
		m := GenericCommand{RII: r.u8()}
		n := int(r.u8())
		if r.err == nil && 3*n > r.len() {
			return nil, fmt.Errorf("%w: NOG %d needs %d octets, %d left", ErrShort, n, 3*n, r.len())
		}
		for range n {
			m.Items = append(m.Items, Item{GIN: r.gin(), KOD: KOD(r.u8())})
		}
		return m, nil
	case TypeDisturbanceList:
		n := vsq.Number()
		if n > MaxDisturbances {
			return nil, fmt.Errorf("%w: VSQ announces %d disturbances, at most %d", ErrInvalid, n, MaxDisturbances)
		}
		m := DisturbanceList{}
		for range n {
			m.Faults = append(m.Faults, RecordedFault{FAN: r.u16(), SOF: SOF(r.u8()), Time: r.cp56()})
		}
		return m, nil
	case TypeDisturbanceOrder, TypeDisturbanceAck:
		return DisturbanceOrder{Ack: t == TypeDisturbanceAck, TOO: TOO(r.u8()), TOV: r.u8(), FAN: r.u16(), ACC: r.u8()}, nil
	case TypeDisturbanceReady:
		r.take(1)
		return DisturbanceReady{TOV: r.u8(), FAN: r.u16(), NOF: r.u16(), NOC: r.u8(), NOE: r.u16(), INT: r.u16(), Time: r.cp32()}, nil
	case TypeChannelReady:
		r.take(1)
		return ChannelReady{TOV: r.u8(), FAN: r.u16(), ACC: r.u8(), RPV: r.f32(), RSV: r.f32(), RFA: r.f32()}, nil
	case TypeTagsReady:
		r.take(2)
		return TagsReady{FAN: r.u16()}, nil
	case TypeTags:
		m := Tags{FAN: r.u16()}
		n := int(r.u8())
		m.TAP = r.u16()
		if n > MaxTags {
			return nil, fmt.Errorf("%w: NOT %d, at most %d", ErrInvalid, n, MaxTags)
		}
		for range n {
			m.Tags = append(m.Tags, Tag{Fun: FunctionType(r.u8()), Inf: InfoNumber(r.u8()), DPI: r.dpi()})
		}
		return m, nil
	case TypeDisturbanceValues:
		r.take(1)
		m := DisturbanceValues{TOV: r.u8(), FAN: r.u16(), ACC: r.u8()}
		n := int(r.u8())
		m.NFE = r.u16()
		if n > MaxValues {
			return nil, fmt.Errorf("%w: NDV %d, at most %d", ErrInvalid, n, MaxValues)
		}
		for range n {
			m.Values = append(m.Values, int16(r.u16()))
		}
		return m, nil
	case TypeEndOfTransmission:
		return EndOfTransmission{TOO: TOO(r.u8()), TOV: r.u8(), FAN: r.u16(), ACC: r.u8()}, nil
	default:
		return Raw{Data: append([]byte{}, r.take(r.len())...)}, nil
	}
}
