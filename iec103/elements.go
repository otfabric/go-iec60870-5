// SPDX-License-Identifier: MIT

package iec103

import "fmt"

// FunctionType is the function type (FUN), the first octet of the
// information object identifier (clause 7.2.5.1).
type FunctionType uint8

// Function types of table 7. The other values of the compatible ranges are
// "not used"; the private ranges are free for manufacturers.
const (
	FunDistance        FunctionType = 128 // distance protection, t(z)
	FunOvercurrent     FunctionType = 160 // overcurrent protection, I>>
	FunTransformerDiff FunctionType = 176 // transformer differential protection, ΔIT
	FunLineDiff        FunctionType = 192 // line differential protection, ΔIL
	FunGeneric         FunctionType = 254 // generic function type, GEN
	FunGlobal          FunctionType = 255 // global function type, GLB
)

// Compatible reports a function type of a compatible range of clause
// 7.2.5.1: 128..129, 144..145, 160..161, 176..177, 192..193, 208..209,
// 224..225, 240..241 and 254..255. The others (0..127 and the gaps) are
// private.
func (f FunctionType) Compatible() bool {
	if f >= 254 {
		return true
	}
	return f >= 128 && f%16 <= 1
}

// String returns the name table 7 gives, for example "<128> distance
// protection", or "<17> private".
func (f FunctionType) String() string {
	switch f {
	case FunDistance:
		return "<128> distance protection"
	case FunOvercurrent:
		return "<160> overcurrent protection"
	case FunTransformerDiff:
		return "<176> transformer differential protection"
	case FunLineDiff:
		return "<192> line differential protection"
	case FunGeneric:
		return "<254> generic function type"
	case FunGlobal:
		return "<255> global function type"
	}
	if f.Compatible() {
		return fmt.Sprintf("<%d> not used", uint8(f))
	}
	return fmt.Sprintf("<%d> private", uint8(f))
}

// InfoNumber is the information number (INF), the second octet of the
// information object identifier (clause 7.2.5.2). Its meaning depends on
// the function type and the direction; see [Catalog].
type InfoNumber uint8

// DPI is double-point information (clause 7.2.6.5) and, in the control
// direction, the double command DCO (clause 7.2.6.4): 1 = OFF, 2 = ON; 0
// and 3 are "not used".
type DPI uint8

// Double-point and double-command values.
const (
	DPIOff DPI = 1
	DPIOn  DPI = 2
)

// String returns "OFF", "ON" or "DPI(n)".
func (d DPI) String() string {
	switch d {
	case DPIOff:
		return "OFF"
	case DPIOn:
		return "ON"
	}
	return fmt.Sprintf("DPI(%d)", uint8(d))
}

// MEA is a measurand with quality descriptor (clause 7.2.6.8): CP16{OV,
// ER, RES, MVAL}. MVAL is a 13-bit two's complement fraction F13 in
// -1..+1-2^-12, held here as the integer Raw in -4096..4095 (the value is
// Raw/4096). Full scale (the largest MVAL) stands for 1.2 or 2.4 times the
// rated value, so the rated value is Raw 3413 or 1707.
type MEA struct {
	Raw      int16 // MVAL, -4096..4095
	Overflow bool  // OV
	Error    bool  // ER: MVAL is not a valid value
}

// MEAOf returns the measurand for v (clamped to the range of MVAL; a value
// outside it sets OV, as clause 7.2.6.8 requires on overflow).
func MEAOf(v float64) MEA {
	r := v * 4096
	switch {
	case r > 4095:
		return MEA{Raw: 4095, Overflow: true}
	case r < -4096:
		return MEA{Raw: -4096, Overflow: true}
	}
	return MEA{Raw: int16(r)}
}

// Value returns MVAL as a fraction of the full scale.
func (m MEA) Value() float64 { return float64(m.Raw) / 4096 }

func (m MEA) word() uint16 {
	w := uint16(m.Raw) << 3
	if m.Overflow {
		w |= 1
	}
	if m.Error {
		w |= 2
	}
	return w
}

func meaOf(w uint16) MEA {
	return MEA{Raw: int16(w) >> 3, Overflow: w&1 != 0, Error: w&2 != 0}
}

// GIN is a generic identification number (clause 7.2.6.31): the group in
// the first octet and the entry in the second. Entry 0 identifies the group
// itself (clause 7.4.8.2).
type GIN struct {
	Group uint8
	Entry uint8
}

// String returns the GIN as the standard writes it, for example "0211H".
func (g GIN) String() string { return fmt.Sprintf("%02X%02XH", g.Group, g.Entry) }

// KOD is the kind of description (clause 7.2.6.34).
type KOD uint8

// Kinds of description.
const (
	KODNone           KOD = 0  // no kind of description specified
	KODActualValue    KOD = 1  // actual value
	KODDefaultValue   KOD = 2  // default value
	KODRange          KOD = 3  // range (minimum, maximum, step size)
	KODPrecision      KOD = 5  // precision (n, m)
	KODFactor         KOD = 6  // factor
	KODReference      KOD = 7  // % reference
	KODEnumeration    KOD = 8  // enumeration
	KODDimension      KOD = 9  // dimension
	KODDescription    KOD = 10 // description
	KODPassword       KOD = 12 // password entry
	KODReadOnly       KOD = 13 // read only
	KODWriteOnly      KOD = 14 // write only
	KODFunInf         KOD = 19 // corresponding function type and information number
	KODEvent          KOD = 20 // corresponding event
	KODTextArray      KOD = 21 // enumerated text array
	KODValueArray     KOD = 22 // enumerated value array
	KODRelatedEntries KOD = 23 // related entries
)

// GRC is the generic reply code (clause 7.2.6.36).
type GRC uint8

// Generic reply codes.
const (
	GRCAck              GRC = 0  // acknowledged
	GRCInvalidGIN       GRC = 1  // invalid generic identification number
	GRCNotExist         GRC = 2  // data requested does not exist
	GRCNotAvailable     GRC = 3  // data not available, try again later
	GRCVerifyError      GRC = 4  // verify error during change of setting
	GRCOutOfRange       GRC = 5  // setting out of range during change
	GRCEntryTooLarge    GRC = 6  // entry too large
	GRCTooManyCommands  GRC = 7  // too many commands
	GRCReadOnly         GRC = 8  // entry is read only
	GRCPasswordProtect  GRC = 9  // setting is password protected
	GRCLocalSetting     GRC = 10 // local setting in progress
	GRCErrorDescription GRC = 11 // error with description following
)

// Counted is the NGD (clause 7.2.6.30) and NDE (clause 7.2.6.35) octet
// without its number, which the encoder takes from the number of data
// sets or description elements: COUNT toggles from one ASDU with the same
// RII to the next, CONT reports that more ASDUs with the same RII follow.
type Counted struct {
	Count bool // COUNT, bit 7
	Cont  bool // CONT, bit 8
}

func (c Counted) octet(n int) byte {
	b := byte(n & 0x3F)
	if c.Count {
		b |= 0x40
	}
	if c.Cont {
		b |= 0x80
	}
	return b
}

func countedOf(b byte) (Counted, int) {
	return Counted{Count: b&0x40 != 0, Cont: b&0x80 != 0}, int(b & 0x3F)
}

// MaxCounted is the largest number NGD and NDE carry (6 bits).
const MaxCounted = 63

// SOF is the status of fault (clause 7.2.6.24).
type SOF uint8

// Bits of SOF.
const (
	SOFTrip SOF = 0x01 // TP: recorded fault with trip
	SOFTM   SOF = 0x02 // TM: disturbance data being transmitted
	SOFTest SOF = 0x04 // TEST: recorded during test mode
	SOFOTEV SOF = 0x08 // OTEV: recording initiated by another event than start/pick-up
)

// TOO is the type of order (clause 7.2.6.26).
type TOO uint8

// Types of order. 1..31 go with ASDU 24, 32..63 with ASDU 31, 64..95 with
// ASDU 25 (clause 7.2.6.26).
const (
	TOOSelectFault        TOO = 1  // selection of fault
	TOORequestData        TOO = 2  // request for disturbance data
	TOOAbortData          TOO = 3  // abortion of disturbance data
	TOORequestChannel     TOO = 8  // channel request
	TOOAbortChannel       TOO = 9  // abortion of channel
	TOORequestTags        TOO = 16 // request for tags
	TOOAbortTags          TOO = 17 // abortion of tags
	TOORequestList        TOO = 24 // request for list of recorded disturbances
	TOOEndData            TOO = 32 // end of disturbance data transmission without abortion
	TOOEndDataAbortCtrl   TOO = 33 // end of disturbance data transmission, aborted by the control system
	TOOEndDataAbortProt   TOO = 34 // end of disturbance data transmission, aborted by the protection equipment
	TOOEndChannel         TOO = 35 // end of channel transmission without abortion
	TOOEndChannelAbortCtl TOO = 36 // end of channel transmission, aborted by the control system
	TOOEndChannelAbortPrt TOO = 37 // end of channel transmission, aborted by the protection equipment
	TOOEndTags            TOO = 38 // end of tag transmission without abortion
	TOOEndTagsAbortCtrl   TOO = 39 // end of tag transmission, aborted by the control system
	TOOEndTagsAbortProt   TOO = 40 // end of tag transmission, aborted by the protection equipment
	TOODataOK             TOO = 64 // disturbance data transmitted successfully (positive)
	TOODataFailed         TOO = 65 // disturbance data transmitted not successfully (negative)
	TOOChannelOK          TOO = 66 // channel transmission successful (positive)
	TOOChannelFailed      TOO = 67 // channel transmission not successful (negative)
	TOOTagsOK             TOO = 68 // tags transmitted successfully (positive)
	TOOTagsFailed         TOO = 69 // tags transmitted not successfully (negative)
)

// TOVInstantaneous is the only type of disturbance values clause 7.2.6.27
// defines: instantaneous values.
const TOVInstantaneous uint8 = 1

// Actual channels (ACC, clause 7.2.6.1). 0 is "global", used in ASDU 24, 25
// and 31 when no channel is concerned.
const (
	ACCGlobal uint8 = 0
	ACCIL1    uint8 = 1 // IL1
	ACCIL2    uint8 = 2 // IL2
	ACCIL3    uint8 = 3 // IL3
	ACCIN     uint8 = 4 // IN
	ACCVL1E   uint8 = 5 // VL1E
	ACCVL2E   uint8 = 6 // VL2E
	ACCVL3E   uint8 = 7 // VL3E
	ACCVEN    uint8 = 8 // VEN
)

// Compatibility levels (COL, clause 7.2.6.3).
const (
	COLWithoutGeneric uint8 = 2 // compatible without generic services
	COLWithGeneric    uint8 = 3 // compatible with generic services
)
