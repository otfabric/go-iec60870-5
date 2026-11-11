// SPDX-License-Identifier: MIT

package iec103

import (
	"errors"
	"fmt"
)

// Codec errors. They are wrapped with detail; test with errors.Is.
var (
	// ErrShort reports an ASDU that ends before its last field.
	ErrShort = errors.New("iec103: ASDU truncated")
	// ErrTrailing reports octets after the last field of a compatible
	// type identification.
	ErrTrailing = errors.New("iec103: octets after the information object")
	// ErrInvalid reports a field out of the range the standard defines,
	// for example more elements than an ASDU may carry, or generic data
	// whose length does not match its descriptor.
	ErrInvalid = errors.New("iec103: invalid field")
	// ErrTooLong reports an ASDU that does not fit one FT 1.2 frame.
	ErrTooLong = errors.New("iec103: ASDU too long")
)

// MaxSize is the largest ASDU one FT 1.2 frame carries: 255 octets of
// L minus the control field and the one-octet link address (clause 6.1
// note 2, clause 6.2.3).
const MaxSize = 253

// TypeID is the type identification, the first octet of the data unit
// identifier (clause 7.2.1).
type TypeID uint8

// Type identifications of the compatible range (clause 7.2.1, tables 3 and
// 4). Types 6 and 10 exist in both directions with the same structure.
const (
	TypeTimeTagged        TypeID = 1  // time-tagged message
	TypeRelTimeTagged     TypeID = 2  // time-tagged message with relative time
	TypeMeasurands1       TypeID = 3  // measurands I
	TypeRelTimeMeasurand  TypeID = 4  // time-tagged measurands with relative time
	TypeIdentification    TypeID = 5  // identification
	TypeTimeSync          TypeID = 6  // time synchronization
	TypeGIInit            TypeID = 7  // general interrogation (control direction)
	TypeGITermination     TypeID = 8  // general interrogation termination
	TypeMeasurands2       TypeID = 9  // measurands II
	TypeGenericData       TypeID = 10 // generic data
	TypeGenericIdent      TypeID = 11 // generic identification
	TypeGeneralCommand    TypeID = 20 // general command (control direction)
	TypeGenericCommand    TypeID = 21 // generic command (control direction)
	TypeDisturbanceList   TypeID = 23 // list of recorded disturbances
	TypeDisturbanceOrder  TypeID = 24 // order for disturbance data transmission (control direction)
	TypeDisturbanceAck    TypeID = 25 // acknowledgement for disturbance data transmission (control direction)
	TypeDisturbanceReady  TypeID = 26 // ready for transmission of disturbance data
	TypeChannelReady      TypeID = 27 // ready for transmission of a channel
	TypeTagsReady         TypeID = 28 // ready for transmission of tags
	TypeTags              TypeID = 29 // transmission of tags
	TypeDisturbanceValues TypeID = 30 // transmission of disturbance values
	TypeEndOfTransmission TypeID = 31 // end of transmission
)

var typeNames = map[TypeID]string{
	TypeTimeTagged: "time-tagged message", TypeRelTimeTagged: "time-tagged message with relative time",
	TypeMeasurands1: "measurands I", TypeRelTimeMeasurand: "time-tagged measurands with relative time",
	TypeIdentification: "identification", TypeTimeSync: "time synchronization",
	TypeGIInit: "general interrogation", TypeGITermination: "general interrogation termination",
	TypeMeasurands2: "measurands II", TypeGenericData: "generic data", TypeGenericIdent: "generic identification",
	TypeGeneralCommand: "general command", TypeGenericCommand: "generic command",
	TypeDisturbanceList: "list of recorded disturbances", TypeDisturbanceOrder: "order for disturbance data transmission",
	TypeDisturbanceAck: "acknowledgement for disturbance data transmission", TypeDisturbanceReady: "ready for transmission of disturbance data",
	TypeChannelReady: "ready for transmission of a channel", TypeTagsReady: "ready for transmission of tags",
	TypeTags: "transmission of tags", TypeDisturbanceValues: "transmission of disturbance values",
	TypeEndOfTransmission: "end of transmission",
}

// Compatible reports a type identification of the compatible range 1..31
// (clause 7.2.1). Values of that range that tables 3 and 4 do not define
// are reserved; [TypeID.Defined] tells them apart.
func (t TypeID) Compatible() bool { return t >= 1 && t <= 31 }

// Defined reports a type identification tables 3 or 4 define.
func (t TypeID) Defined() bool { _, ok := typeNames[t]; return ok }

// String returns the name of the type identification, for example
// "<1> time-tagged message", or "<200> private".
func (t TypeID) String() string {
	if n, ok := typeNames[t]; ok {
		return fmt.Sprintf("<%d> %s", uint8(t), n)
	}
	if t.Compatible() || t == 0 {
		return fmt.Sprintf("<%d> reserved", uint8(t))
	}
	return fmt.Sprintf("<%d> private", uint8(t))
}

// VSQ is the variable structure qualifier (clause 7.2.2): the number of
// information elements in bits 1..7 and SQ in bit 8.
type VSQ uint8

// NewVSQ returns the qualifier for n elements with the SQ bit.
func NewVSQ(n int, sq bool) VSQ {
	v := VSQ(n & 0x7F)
	if sq {
		v |= 0x80
	}
	return v
}

// Number returns the number of information elements.
func (v VSQ) Number() int { return int(v & 0x7F) }

// SQ reports SQ = 1: a single information element or combination of
// elements is addressed (clause 7.2.2).
func (v VSQ) SQ() bool { return v&0x80 != 0 }

// VSQSingle is the qualifier of every ASDU with one information object:
// SQ = 1, number = 1 (0x81, clauses 7.3.1 and 7.3.2).
const VSQSingle VSQ = 0x81

// Cause is the cause of transmission, the third octet of the data unit
// identifier (clause 7.2.3).
type Cause uint8

// Causes of transmission, monitor direction (table 5).
const (
	CauseSpontaneous        Cause = 1  // spontaneous
	CauseCyclic             Cause = 2  // cyclic
	CauseResetFCB           Cause = 3  // reset frame count bit (FCB)
	CauseResetCU            Cause = 4  // reset communication unit (CU)
	CauseStartRestart       Cause = 5  // start / restart
	CausePowerOn            Cause = 6  // power on
	CauseTestMode           Cause = 7  // test mode
	CauseTimeSync           Cause = 8  // time synchronization (both directions)
	CauseGI                 Cause = 9  // general interrogation (both directions: initiation in control direction)
	CauseGITermination      Cause = 10 // termination of general interrogation
	CauseLocalOperation     Cause = 11 // local operation
	CauseRemoteOperation    Cause = 12 // remote operation
	CauseCommandAck         Cause = 20 // positive acknowledgement of command; control direction: general command
	CauseCommandNack        Cause = 21 // negative acknowledgement of command
	CauseDisturbanceData    Cause = 31 // transmission of disturbance data (both directions)
	CauseGenericWriteAck    Cause = 40 // positive acknowledgement of generic write command; control direction: generic write command
	CauseGenericWriteNack   Cause = 41 // negative acknowledgement of generic write command
	CauseGenericReadValid   Cause = 42 // valid data response to generic read command; control direction: generic read command
	CauseGenericReadInvalid Cause = 43 // invalid data response to generic read command
	CauseGenericWriteConf   Cause = 44 // generic write confirmation
)

// Control-direction aliases of the causes table 6 defines.
const (
	CauseGeneralCommand = CauseCommandAck       // 20: general command
	CauseGenericWrite   = CauseGenericWriteAck  // 40: generic write command
	CauseGenericRead    = CauseGenericReadValid // 42: generic read command
)

var causeNames = map[Cause]string{
	CauseSpontaneous: "spontaneous", CauseCyclic: "cyclic", CauseResetFCB: "reset FCB", CauseResetCU: "reset CU",
	CauseStartRestart: "start/restart", CausePowerOn: "power on", CauseTestMode: "test mode",
	CauseTimeSync: "time synchronization", CauseGI: "general interrogation", CauseGITermination: "termination of general interrogation",
	CauseLocalOperation: "local operation", CauseRemoteOperation: "remote operation",
	CauseCommandAck: "positive command acknowledgement", CauseCommandNack: "negative command acknowledgement",
	CauseDisturbanceData: "disturbance data", CauseGenericWriteAck: "positive generic write acknowledgement",
	CauseGenericWriteNack: "negative generic write acknowledgement", CauseGenericReadValid: "valid generic read response",
	CauseGenericReadInvalid: "invalid generic read response", CauseGenericWriteConf: "generic write confirmation",
}

// Compatible reports a cause of the compatible range 1..63 (clause 7.2.3).
func (c Cause) Compatible() bool { return c >= 1 && c <= 63 }

// String returns the name of the cause (monitor direction wording), for
// example "<9> general interrogation".
func (c Cause) String() string {
	if n, ok := causeNames[c]; ok {
		return fmt.Sprintf("<%d> %s", uint8(c), n)
	}
	if c.Compatible() || c == 0 {
		return fmt.Sprintf("<%d> reserved", uint8(c))
	}
	return fmt.Sprintf("<%d> private", uint8(c))
}

// CommonAddrGlobal is the global common address of ASDU (clause 7.2.4).
const CommonAddrGlobal uint8 = 255
