// SPDX-License-Identifier: MIT

package iec102

import (
	"errors"
	"fmt"
)

// Codec errors. They are wrapped with detail; test with errors.Is.
var (
	// ErrShort reports an ASDU that ends before its last field.
	ErrShort = errors.New("iec102: ASDU truncated")
	// ErrTrailing reports octets after the last field of a type this
	// package interprets.
	ErrTrailing = errors.New("iec102: octets after the information objects")
	// ErrInvalid reports a field out of range: an address size other than 1
	// or 2, more objects than the variable structure qualifier can count,
	// a length that fits no layout of the type.
	ErrInvalid = errors.New("iec102: invalid field")
	// ErrTooLong reports an ASDU that does not fit one FT 1.2 frame.
	ErrTooLong = errors.New("iec102: ASDU too long")
)

// MaxSize is the largest ASDU this codec encodes or accepts: 255 octets of
// FT 1.2 length minus the control field and a one-octet link address
// (Beckhoff TS650x manual, section 9.1.6: asduSize 253 for 101/102/103).
// With a two-octet link address one frame carries 252; the stations split
// their answers accordingly.
const MaxSize = 253

// TypeID is the type identification, the first octet of the data unit
// identifier.
type TypeID uint8

// Type identifications of the standard range 0..127 (Beckhoff TS650x
// manual, section 4.2 and 9.1.10, which list them as defined by
// IEC 60870-5-102:1996). The names are the standard mnemonics.
const (
	M_SP_TA_2 TypeID = 1  // single-point information with time tag
	M_IT_TA_2 TypeID = 2  // accounting integrated totals, 4 octets each
	M_IT_TB_2 TypeID = 3  // accounting integrated totals, 3 octets each
	M_IT_TC_2 TypeID = 4  // accounting integrated totals, 2 octets each
	M_IT_TD_2 TypeID = 5  // periodical reset accounting integrated totals, 4 octets each
	M_IT_TE_2 TypeID = 6  // periodical reset accounting integrated totals, 3 octets each
	M_IT_TF_2 TypeID = 7  // periodical reset accounting integrated totals, 2 octets each
	M_IT_TG_2 TypeID = 8  // operational integrated totals, 4 octets each
	M_IT_TH_2 TypeID = 9  // operational integrated totals, 3 octets each
	M_IT_TI_2 TypeID = 10 // operational integrated totals, 2 octets each
	M_IT_TK_2 TypeID = 11 // periodical reset operational integrated totals, 4 octets each
	M_IT_TL_2 TypeID = 12 // periodical reset operational integrated totals, 3 octets each
	M_IT_TM_2 TypeID = 13 // periodical reset operational integrated totals, 2 octets each

	M_EI_NA_2 TypeID = 70 // end of initialization
	P_MP_NA_2 TypeID = 71 // manufacturer and product specification of the integrated total DTE
	M_TI_TA_2 TypeID = 72 // current system time of the integrated total DTE

	C_RD_NA_2 TypeID = 100 // read manufacturer and product specification
	C_SP_NA_2 TypeID = 101 // read record of single-point information with time tag
	C_SP_NB_2 TypeID = 102 // read record of single-point information with time tag of a selected time range
	C_TI_NA_2 TypeID = 103 // read current system time of the integrated total DTE
	C_CI_NA_2 TypeID = 104 // read accounting integrated totals of the oldest integration period
	C_CI_NB_2 TypeID = 105 // ... oldest integration period and a selected range of addresses
	C_CI_NC_2 TypeID = 106 // ... of a specific past integration period
	C_CI_ND_2 TypeID = 107 // ... specific past integration period and a selected range of addresses
	C_CI_NE_2 TypeID = 108 // read periodical reset accounting integrated totals of the oldest integration period
	C_CI_NF_2 TypeID = 109 // ... oldest integration period and a selected range of addresses
	C_CI_NG_2 TypeID = 110 // ... of a specific past integration period
	C_CI_NH_2 TypeID = 111 // ... specific past integration period and a selected range of addresses
	C_CI_NI_2 TypeID = 112 // read operational integrated totals of the oldest integration period
	C_CI_NK_2 TypeID = 113 // ... oldest integration period and a selected range of addresses
	C_CI_NL_2 TypeID = 114 // ... of a specific past integration period
	C_CI_NM_2 TypeID = 115 // ... specific past integration period and a selected range of addresses
	C_CI_NN_2 TypeID = 116 // read periodical reset operational integrated totals of the oldest integration period
	C_CI_NO_2 TypeID = 117 // ... oldest integration period and a selected range of addresses
	C_CI_NP_2 TypeID = 118 // ... of a specific past integration period
	C_CI_NQ_2 TypeID = 119 // ... specific past integration period and a selected range of addresses
	C_CI_NR_2 TypeID = 120 // read accounting integrated totals of a selected time range and range of addresses
	C_CI_NS_2 TypeID = 121 // read periodical reset accounting integrated totals of a selected time range and range of addresses
	C_CI_NT_2 TypeID = 122 // read operational integrated totals of a selected time range and range of addresses
	C_CI_NU_2 TypeID = 123 // read periodical reset operational integrated totals of a selected time range and range of addresses
)

var typeNames = map[TypeID]string{
	M_SP_TA_2: "M_SP_TA_2", M_IT_TA_2: "M_IT_TA_2", M_IT_TB_2: "M_IT_TB_2", M_IT_TC_2: "M_IT_TC_2",
	M_IT_TD_2: "M_IT_TD_2", M_IT_TE_2: "M_IT_TE_2", M_IT_TF_2: "M_IT_TF_2", M_IT_TG_2: "M_IT_TG_2",
	M_IT_TH_2: "M_IT_TH_2", M_IT_TI_2: "M_IT_TI_2", M_IT_TK_2: "M_IT_TK_2", M_IT_TL_2: "M_IT_TL_2",
	M_IT_TM_2: "M_IT_TM_2", M_EI_NA_2: "M_EI_NA_2", P_MP_NA_2: "P_MP_NA_2", M_TI_TA_2: "M_TI_TA_2",
	C_RD_NA_2: "C_RD_NA_2", C_SP_NA_2: "C_SP_NA_2", C_SP_NB_2: "C_SP_NB_2", C_TI_NA_2: "C_TI_NA_2",
	C_CI_NA_2: "C_CI_NA_2", C_CI_NB_2: "C_CI_NB_2", C_CI_NC_2: "C_CI_NC_2", C_CI_ND_2: "C_CI_ND_2",
	C_CI_NE_2: "C_CI_NE_2", C_CI_NF_2: "C_CI_NF_2", C_CI_NG_2: "C_CI_NG_2", C_CI_NH_2: "C_CI_NH_2",
	C_CI_NI_2: "C_CI_NI_2", C_CI_NK_2: "C_CI_NK_2", C_CI_NL_2: "C_CI_NL_2", C_CI_NM_2: "C_CI_NM_2",
	C_CI_NN_2: "C_CI_NN_2", C_CI_NO_2: "C_CI_NO_2", C_CI_NP_2: "C_CI_NP_2", C_CI_NQ_2: "C_CI_NQ_2",
	C_CI_NR_2: "C_CI_NR_2", C_CI_NS_2: "C_CI_NS_2", C_CI_NT_2: "C_CI_NT_2", C_CI_NU_2: "C_CI_NU_2",
}

// String returns the mnemonic of a standard type identification, or
// "TypeID(n)".
func (t TypeID) String() string {
	if s, ok := typeNames[t]; ok {
		return s
	}
	return fmt.Sprintf("TypeID(%d)", uint8(t))
}

// Standard reports a type identification of the standard range that the
// Beckhoff manual (section 4.2) lists as defined: 1..13, 70..72 and
// 100..123. Types 128..255 are outside the standard range; the Spanish
// REE profile assigns many of them (package
// [github.com/otfabric/go-iec60870-5/iec102/ree]).
func (t TypeID) Standard() bool {
	_, ok := typeNames[t]
	return ok
}

// Private reports a type identification above the standard range
// (128..255; Beckhoff TS650x manual, section 4.2 lists 124..127 as
// "reserved (standard area)").
func (t TypeID) Private() bool { return t >= 128 }

// IntegratedTotalsType reports whether t carries integrated totals
// (M_IT_TA_2 .. M_IT_TM_2) and their counter width in octets (4, 3 or 2).
func (t TypeID) IntegratedTotalsType() (width int, ok bool) {
	if t < M_IT_TA_2 || t > M_IT_TM_2 {
		return 0, false
	}
	return 4 - int(t-M_IT_TA_2)%3, true
}

// Family is the kind of integrated totals a type carries or a read
// command asks for.
type Family uint8

// Families of integrated totals, in the order of the type identifications.
const (
	FamilyNone             Family = iota
	FamilyAccounting              // M_IT_TA_2..TC_2; C_CI_NA_2..ND_2, NR_2
	FamilyResetAccounting         // M_IT_TD_2..TF_2; C_CI_NE_2..NH_2, NS_2
	FamilyOperational             // M_IT_TG_2..TI_2; C_CI_NI_2..NM_2, NT_2
	FamilyResetOperational        // M_IT_TK_2..TM_2; C_CI_NN_2..NQ_2, NU_2
)

// String returns the family's name.
func (f Family) String() string {
	switch f {
	case FamilyAccounting:
		return "accounting"
	case FamilyResetAccounting:
		return "periodical reset accounting"
	case FamilyOperational:
		return "operational"
	case FamilyResetOperational:
		return "periodical reset operational"
	default:
		return "none"
	}
}

// Family returns the family of an integrated totals type (2..13) or of a
// read command (104..123), FamilyNone for other types.
func (t TypeID) Family() Family {
	switch {
	case t >= M_IT_TA_2 && t <= M_IT_TM_2:
		return Family(1 + (t-M_IT_TA_2)/3)
	case t >= C_CI_NA_2 && t <= C_CI_NQ_2:
		return Family(1 + (t-C_CI_NA_2)/4)
	case t >= C_CI_NR_2 && t <= C_CI_NU_2:
		return Family(1 + t - C_CI_NR_2)
	}
	return FamilyNone
}

// Selection is what a read command of integrated totals selects
// (Beckhoff TS650x manual, sections 4.22.10 to 4.22.29).
type Selection uint8

// Selections.
const (
	SelectNone        Selection = iota
	SelectOldest                // the oldest integration period, all addresses (length 0)
	SelectOldestRange           // the oldest period, an address range (length 2)
	SelectPeriod                // a specific past period by its time (length 5)
	SelectPeriodRange           // a specific past period and an address range (length 7)
	SelectTimeRange             // a time range and an address range (length 12)
)

// Selection returns what a read command of integrated totals (104..123)
// selects, SelectNone for other types.
func (t TypeID) Selection() Selection {
	switch {
	case t >= C_CI_NA_2 && t <= C_CI_NQ_2:
		return Selection(1 + (t-C_CI_NA_2)%4)
	case t >= C_CI_NR_2 && t <= C_CI_NU_2:
		return SelectTimeRange
	}
	return SelectNone
}

// VSQ is the variable structure qualifier: SQ (bit 7) and the number of
// information objects (bits 0-6), Beckhoff TS650x manual, section 9.1.9.
type VSQ uint8

// SQ reports the sequence bit.
func (v VSQ) SQ() bool { return v&0x80 != 0 }

// Number returns the number of information objects.
func (v VSQ) Number() int { return int(v & 0x7F) }

// Cause is the cause of transmission octet: the cause 0..63 (bits 0-5),
// P/N (bit 6, negative confirmation) and T (bit 7, test), Beckhoff TS650x
// manual, sections 3.2 and 9.1.9.
type Cause uint8

// Causes of transmission (Beckhoff TS650x manual, section 9.1.11,
// E_IEC870_5_102COTType). daiiniel/IEC60870-5-102 names 13..18 slightly
// differently (docs/iec102/INTEROPERABILITY.md); the numbers agree.
const (
	CauseSpontaneous       Cause = 3  // spontaneous
	CauseInitialized       Cause = 4  // initialization
	CauseRequest           Cause = 5  // request or requested
	CauseActivation        Cause = 6  // activation
	CauseActivationCon     Cause = 7  // activation confirmation
	CauseDeactivation      Cause = 8  // deactivation
	CauseDeactivationCon   Cause = 9  // deactivation confirmation
	CauseActivationTerm    Cause = 10 // activation termination
	CauseNoRecord          Cause = 13 // requested record (list) not available
	CauseUnknownType       Cause = 14 // requested ASDU type not available / unknown
	CauseUnknownRecordNum  Cause = 15 // record number unknown
	CauseUnknownRecordAddr Cause = 16 // address specification (record address) unknown
	CauseNoObject          Cause = 17 // requested information object not available
	CauseNoPeriod          Cause = 18 // requested integration period not available
)

// Bits of the cause octet.
const (
	CauseNegative Cause = 0x40 // P/N: negative confirmation
	CauseTest     Cause = 0x80 // T: test
)

// Code returns the cause without the P/N and T bits.
func (c Cause) Code() Cause { return c & 0x3F }

// Negative reports the P/N bit.
func (c Cause) Negative() bool { return c&CauseNegative != 0 }

// Test reports the T bit.
func (c Cause) Test() bool { return c&CauseTest != 0 }

// Refusal reports a negative answer: the P/N bit, or one of the causes
// 13..18 by which a station reports that it cannot serve a request.
func (c Cause) Refusal() bool {
	return c.Negative() || (c.Code() >= CauseNoRecord && c.Code() <= CauseNoPeriod)
}

var causeNames = map[Cause]string{
	CauseSpontaneous: "spontaneous", CauseInitialized: "initialized", CauseRequest: "request",
	CauseActivation: "activation", CauseActivationCon: "activation confirmation",
	CauseDeactivation: "deactivation", CauseDeactivationCon: "deactivation confirmation",
	CauseActivationTerm: "activation termination", CauseNoRecord: "record not available",
	CauseUnknownType: "ASDU type not available", CauseUnknownRecordNum: "unknown record number",
	CauseUnknownRecordAddr: "unknown record address", CauseNoObject: "information object not available",
	CauseNoPeriod: "integration period not available",
}

// String returns the cause's name with "<n>" and the flags, for example
// "<7> activation confirmation".
func (c Cause) String() string {
	s := fmt.Sprintf("<%d>", uint8(c.Code()))
	if n, ok := causeNames[c.Code()]; ok {
		s += " " + n
	}
	if c.Negative() {
		s += " negative"
	}
	if c.Test() {
		s += " test"
	}
	return s
}

// Record addresses (list addresses) of Beckhoff TS650x manual, section
// 9.1.8. 128..255 are for particular applications (private range); the
// REE profile uses 134..139 for its contracts.
const (
	RecordDefault           uint8 = 0  // default, if no other value is specified
	RecordAccountingPeriod  uint8 = 1  // counter states from the beginning of the accounting period
	RecordPeriod1           uint8 = 11 // counter states from measurement (integration) period 1
	RecordPeriod2           uint8 = 12 // ... period 2
	RecordPeriod3           uint8 = 13 // ... period 3
	RecordDaily1            uint8 = 21 // daily values from measurement period 1 (22, 23: periods 2, 3)
	RecordMonthly1          uint8 = 31 // monthly values from measurement period 1 (32, 33)
	RecordYearly1           uint8 = 41 // yearly values from measurement period 1 (42, 43)
	RecordOldestSingle      uint8 = 50 // oldest single message
	RecordAllSingle         uint8 = 51 // complete list with single messages
	RecordSinglePart1       uint8 = 52 // partial list 1 with single messages (53..55: lists 2..4)
	RecordPrivateRangeStart uint8 = 128
)
