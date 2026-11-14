// SPDX-License-Identifier: MIT

// Package ree is the Spanish REE profile of IEC 60870-5-102: the type
// identifications of the private range (128..255) that Spanish fiscal
// metering ("Protocolo de comunicaciones entre registradores y
// concentradores", Red Eléctrica de España) assigns, the meaning it gives
// to the low bits of the integrated totals' quality descriptor and the
// session procedure (access key and end of session).
//
// None of it is part of IEC 60870-5-102. It is applied only where the
// caller asks for it: set [Profile] as iec102.Params.Profile to decode the
// bodies below, and use [Login], [Logout] and [SetTime] on a client. The
// generic packages never send or expect an REE ASDU on their own. On a
// server, [Session] answers the session requests; its session state
// follows the last access key received, so a C_AC_NA_2 with a wrong key
// closes an open session.
//
// We hold no REE document. The sources are, as for package iec102: the
// names the Beckhoff TS650x manual lists for 128..190 (section 4.2, without
// layouts), the implementations gisce/iec870ree and daiiniel/IEC60870-5-102
// (read for comparison only), the test vectors of gisce/iec870ree and the
// recorded traffic of an Actaris SL762B meter (session start with
// C_AC_NA_2, read with C_CI_NU_2). docs/iec102/INTEROPERABILITY.md lists
// each type with its source and confidence.
//
// Typed bodies: [AccessKey] (C_AC_NA_2), [Request] (C_FS_NA_2, C_TA_VC_2,
// C_TA_VM_2, C_RM_NA_2, C_PC_NA_2, C_DF_NA_2, C_CB_NT_2, C_CB_UN_2),
// [SetClock] (C_CS_TA_2), [TariffInfo] (M_TA_VC_2, M_TA_VM_2),
// [ContractedPowers] (M_PC_NA_2) and [SpecialDays] (M_DF_NA_2). Every
// other REE type stays [iec102.Raw].
package ree

import (
	"fmt"

	"github.com/otfabric/go-iec60870-5/iec102"
)

// Type identifications of the REE profile. Names and numbers as listed in
// the Beckhoff TS650x manual, section 4.2 and 9.1.10 (which marks 128,
// 130, 131, 144..149, 180, 184..186, 188 with "-"); the extension types
// 150..163 are named after gisce/iec870ree only.
const (
	M_DS_TA_2 iec102.TypeID = 128 // (no description in the manual)
	P_ME_NA_2 iec102.TypeID = 129 // parameters of the measuring point
	M_DS_TB_2 iec102.TypeID = 130
	M_CH_TA_2 iec102.TypeID = 131
	C_PK_2    iec102.TypeID = 132 // load private key
	C_TA_VC_2 iec102.TypeID = 133 // read tariff information (current values)
	C_TA_VM_2 iec102.TypeID = 134 // read tariff information (stored values)
	M_TA_VC_2 iec102.TypeID = 135 // tariff information (current values)
	M_TA_VM_2 iec102.TypeID = 136 // tariff information (stored values)
	C_TA_CP_2 iec102.TypeID = 137 // close accounting period
	M_IB_TG_2 iec102.TypeID = 139 // block of operational integrated totals (absolute values)
	M_IB_TK_2 iec102.TypeID = 140 // block of periodical reset operational integrated totals (increments)
	C_RM_NA_2 iec102.TypeID = 141 // read configuration data of the meter device
	M_RM_NA_2 iec102.TypeID = 142 // configuration of the meter device
	C_MR_NA_2 iec102.TypeID = 143 // change configuration data of the meter device
	C_PC_NA_2 iec102.TypeID = 144 // read contracted powers (gisce)
	M_PC_NA_2 iec102.TypeID = 145 // contracted powers (gisce)
	C_MC_NA_2 iec102.TypeID = 146
	C_DF_NA_2 iec102.TypeID = 147 // read special days (gisce)
	M_DF_NA_2 iec102.TypeID = 148 // special days (gisce)
	C_MF_NA_2 iec102.TypeID = 149
	P_TA_IN_2 iec102.TypeID = 150 // extension: read tariff configuration objects (gisce)
	R_TA_IN_2 iec102.TypeID = 151 // extension: tariff configuration objects (gisce)
	P_RM_R2_2 iec102.TypeID = 156 // extension: read configuration objects (gisce)
	R_RM_R2_2 iec102.TypeID = 157 // extension: configuration objects (gisce)
	P_IN_VA_2 iec102.TypeID = 162 // extension: read instantaneous values (gisce; sent in the innoWatt log)
	R_IN_VA_2 iec102.TypeID = 163 // extension: instantaneous values (gisce)
	C_DS_TA_2 iec102.TypeID = 180
	C_CS_TA_2 iec102.TypeID = 181 // change date and time (time synchronization)
	C_PI_NA_2 iec102.TypeID = 182 // read parameters of the measuring point
	C_AC_NA_2 iec102.TypeID = 183 // start session and send access key
	C_DS_TB_2 iec102.TypeID = 184
	C_CH_TA_2 iec102.TypeID = 185
	C_MH_TA_2 iec102.TypeID = 186
	C_FS_NA_2 iec102.TypeID = 187 // finish session
	C_MP_NA_2 iec102.TypeID = 188
	C_CB_NT_2 iec102.TypeID = 189 // read a block of operational integrated totals of a period and an address
	C_CB_UN_2 iec102.TypeID = 190 // read a block of periodical reset operational integrated totals of a period and an address
)

var names = map[iec102.TypeID]string{
	M_DS_TA_2: "M_DS_TA_2", P_ME_NA_2: "P_ME_NA_2", M_DS_TB_2: "M_DS_TB_2", M_CH_TA_2: "M_CH_TA_2",
	C_PK_2: "C_PK_2", C_TA_VC_2: "C_TA_VC_2", C_TA_VM_2: "C_TA_VM_2", M_TA_VC_2: "M_TA_VC_2",
	M_TA_VM_2: "M_TA_VM_2", C_TA_CP_2: "C_TA_CP_2", M_IB_TG_2: "M_IB_TG_2", M_IB_TK_2: "M_IB_TK_2",
	C_RM_NA_2: "C_RM_NA_2", M_RM_NA_2: "M_RM_NA_2", C_MR_NA_2: "C_MR_NA_2", C_PC_NA_2: "C_PC_NA_2",
	M_PC_NA_2: "M_PC_NA_2", C_MC_NA_2: "C_MC_NA_2", C_DF_NA_2: "C_DF_NA_2", M_DF_NA_2: "M_DF_NA_2",
	C_MF_NA_2: "C_MF_NA_2", P_TA_IN_2: "P_TA_IN_2", R_TA_IN_2: "R_TA_IN_2", P_RM_R2_2: "P_RM_R2_2",
	R_RM_R2_2: "R_RM_R2_2", P_IN_VA_2: "P_IN_VA_2", R_IN_VA_2: "R_IN_VA_2", C_DS_TA_2: "C_DS_TA_2",
	C_CS_TA_2: "C_CS_TA_2", C_PI_NA_2: "C_PI_NA_2", C_AC_NA_2: "C_AC_NA_2", C_DS_TB_2: "C_DS_TB_2",
	C_CH_TA_2: "C_CH_TA_2", C_MH_TA_2: "C_MH_TA_2", C_FS_NA_2: "C_FS_NA_2", C_MP_NA_2: "C_MP_NA_2",
	C_CB_NT_2: "C_CB_NT_2", C_CB_UN_2: "C_CB_UN_2",
}

// Name returns the REE mnemonic of t, or the standard name from
// iec102.TypeID.String.
func Name(t iec102.TypeID) string {
	if s, ok := names[t]; ok {
		return s
	}
	return t.String()
}

// Integrated total addresses of the REE profile (gisce/iec870ree,
// daiiniel/IEC60870-5-102 ObjectsDirections; address 1 labelled "active
// import" in the innoWatt log).
const (
	AddrActiveImport uint8 = 1 // active energy, import
	AddrActiveExport uint8 = 2 // active energy, export
	AddrReactiveQ1   uint8 = 3 // reactive energy, quadrant 1
	AddrReactiveQ2   uint8 = 4
	AddrReactiveQ3   uint8 = 5
	AddrReactiveQ4   uint8 = 6 // reactive energy, quadrant 4
	AddrReserved7    uint8 = 7
	AddrReserved8    uint8 = 8
)

// Record addresses of the REE profile (gisce/iec870ree protocol.py,
// daiiniel/IEC60870-5-102 RegisterDirections); 11, 12 and 21 coincide with
// the standard list addresses for measurement periods 1 and 2 and daily
// values (Beckhoff TS650x manual, section 9.1.8).
const (
	RecordLoadProfile   uint8 = 11  // hourly load profile (integration period 1)
	RecordQuarterHour   uint8 = 12  // quarter-hour load profile (integration period 2)
	RecordDailyBilling  uint8 = 21  // daily billing closures
	RecordContract1     uint8 = 134 // contract I tariff information (135, 136: II, III)
	RecordLatentContr1  uint8 = 137 // latent contract I (138, 139: II, III)
	RecordEventsPowerOn uint8 = 52  // events: power on and voltage (53..55, 128..133: other event lists)
)

// Quality is the integrated total quality descriptor as the REE profile
// reads it: IV, CA and CY as in the standard (bits 7, 6, 5), and instead of
// the sequence number VH (bit 4, time verification), MP (bit 3, parameters
// modified), IN (bit 2, intrusion), AL (bit 1, period incomplete/alarm)
// and a reserved bit 0. Source: daiiniel/IEC60870-5-102 Qualifier
// (encoder) and the innoWatt log's labels "iv,ca,cy,vh,mp,in,al,res"; the
// Beckhoff manual (section 4.25.8) has a sequence number in bits 0-4. The
// meaning of each REE bit beyond its name is not documented here.
type Quality iec102.Quality

// Bits of the REE quality descriptor.
const (
	QualityIV Quality = 0x80
	QualityCA Quality = 0x40
	QualityCY Quality = 0x20
	QualityVH Quality = 0x10
	QualityMP Quality = 0x08
	QualityIN Quality = 0x04
	QualityAL Quality = 0x02
)

// Has reports whether all bits of f are set.
func (q Quality) Has(f Quality) bool { return q&f == f }

// Objects of the REE requests with an information object: 1.
// gisce/iec870ree writes a variable structure qualifier of 1 for its
// requests with information elements and 0 for those without (C_FS_NA_2,
// C_TA_VC_2, ...: seen on the wire in the iec102/interop suite, "BB 00 06"
// for C_FS_NA_2), as the Beckhoff TS650x manual does for the standard
// requests (section 4.22, nObj); daiiniel/IEC60870-5-102 writes 1 for
// C_AC_NA_2 and C_CB_NT_2; the recorded traffic mirrors C_AC_NA_2 with 1.
const requestObjects = 1

// AccessKey is the body of C_AC_NA_2, start of session with the access key
// of the measuring point: four octets little-endian (gisce, daiiniel and
// the recorded confirmation "01 00 00 00").
type AccessKey struct{ Key uint32 }

// Type returns C_AC_NA_2.
func (AccessKey) Type() iec102.TypeID { return C_AC_NA_2 }

// Objects returns 1.
func (AccessKey) Objects() int { return requestObjects }

// AppendTo appends the key.
func (b AccessKey) AppendTo(out []byte, _ iec102.Params) ([]byte, error) {
	return iec102.AppendUint(out, b.Key, 4), nil
}

// SetClock is the body of C_CS_TA_2, change date and time: one time b
// (gisce C_CS_TA_2 and its test vector; daiiniel C_CS_TA_2 encodes the
// first two octets differently, see package iec102, TimeB).
type SetClock struct{ Time iec102.TimeB }

// Type returns C_CS_TA_2.
func (SetClock) Type() iec102.TypeID { return C_CS_TA_2 }

// Objects returns 1.
func (SetClock) Objects() int { return requestObjects }

// AppendTo appends the time.
func (b SetClock) AppendTo(out []byte, _ iec102.Params) ([]byte, error) {
	return b.Time.AppendTo(out), nil
}

// Request is the body of the REE requests whose information object is
// empty or a time range: C_FS_NA_2, C_TA_VC_2, C_RM_NA_2, C_PC_NA_2,
// C_DF_NA_2 (nothing), C_TA_VM_2 (From, To) and C_CB_NT_2, C_CB_UN_2
// (Object, From, To). Layouts from gisce/iec870ree and, for C_CB_NT_2,
// daiiniel/IEC60870-5-102.
type Request struct {
	T iec102.TypeID
	// Object selects the block of a C_CB_NT_2 or C_CB_UN_2: 9 totals 1-8,
	// 10 totals 1-6, 11 totals 1, 3 and 6 (daiiniel ObjectsDirections).
	Object   uint8
	From, To iec102.TimeA
}

// Type returns T.
func (b Request) Type() iec102.TypeID { return b.T }

// Objects returns 0 for the requests without information elements
// (C_FS_NA_2, C_TA_VC_2, C_RM_NA_2, C_PC_NA_2, C_DF_NA_2) and 1 for those
// with a time range or an object.
func (b Request) Objects() int {
	if obj, times, ok := requestLayout(b.T); ok && !obj && times == 0 {
		return 0
	}
	return requestObjects
}

func requestLayout(t iec102.TypeID) (object bool, times int, ok bool) {
	switch t {
	case C_FS_NA_2, C_TA_VC_2, C_RM_NA_2, C_PC_NA_2, C_DF_NA_2:
		return false, 0, true
	case C_TA_VM_2:
		return false, 2, true
	case C_CB_NT_2, C_CB_UN_2:
		return true, 2, true
	default:
		return false, 0, false
	}
}

// AppendTo appends the fields T carries.
func (b Request) AppendTo(out []byte, _ iec102.Params) ([]byte, error) {
	obj, times, ok := requestLayout(b.T)
	if !ok {
		return nil, fmt.Errorf("%w: %s is not an REE request with a known layout", iec102.ErrInvalid, Name(b.T))
	}
	if obj {
		out = append(out, b.Object)
	}
	if times == 2 {
		out = b.To.AppendTo(b.From.AppendTo(out))
	}
	return out, nil
}

// TariffRecord is one information object of M_TA_VC_2 and M_TA_VM_2
// (63 octets; layout of gisce/iec870ree M_TA_VX_2 and its test vector):
// the contract's register address, absolute and incremental energies with
// their quality descriptors, the maximum demand with its time, the excess
// power and the start and end of the billing period.
type TariffRecord struct {
	Addr                           uint8
	ActiveAbs, ActiveInc           uint32
	ActiveQual                     uint8
	ReactiveIndAbs, ReactiveIndInc uint32
	ReactiveIndQual                uint8
	ReactiveCapAbs, ReactiveCapInc uint32
	ReactiveCapQual                uint8
	Reserved7                      uint32
	Reserved7Qual                  uint8
	Reserved8                      uint32
	Reserved8Qual                  uint8
	MaxPower                       uint32
	MaxPowerTime                   iec102.TimeA
	MaxPowerQual                   uint8
	ExcessPower                    uint32
	ExcessPowerQual                uint8
	Start, End                     iec102.TimeA
}

const tariffRecordSize = 63

// TariffInfo is the body of M_TA_VC_2 and M_TA_VM_2.
type TariffInfo struct {
	T       iec102.TypeID
	Records []TariffRecord
}

// Type returns T.
func (b TariffInfo) Type() iec102.TypeID { return b.T }

// Objects returns the number of records.
func (b TariffInfo) Objects() int { return len(b.Records) }

// Sequence marks a body with a variable number of objects.
func (TariffInfo) Sequence() {}

// AppendTo appends the records.
func (b TariffInfo) AppendTo(out []byte, _ iec102.Params) ([]byte, error) {
	u := iec102.AppendUint
	for _, r := range b.Records {
		out = append(out, r.Addr)
		out = append(u(u(out, r.ActiveAbs, 4), r.ActiveInc, 4), r.ActiveQual)
		out = append(u(u(out, r.ReactiveIndAbs, 4), r.ReactiveIndInc, 4), r.ReactiveIndQual)
		out = append(u(u(out, r.ReactiveCapAbs, 4), r.ReactiveCapInc, 4), r.ReactiveCapQual)
		out = append(u(out, r.Reserved7, 4), r.Reserved7Qual)
		out = append(u(out, r.Reserved8, 4), r.Reserved8Qual)
		out = append(r.MaxPowerTime.AppendTo(u(out, r.MaxPower, 4)), r.MaxPowerQual)
		out = append(u(out, r.ExcessPower, 4), r.ExcessPowerQual)
		out = r.End.AppendTo(r.Start.AppendTo(out))
	}
	return out, nil
}

// ContractedPower is one contracted power of M_PC_NA_2.
type ContractedPower struct {
	Addr  uint8  // tariff period
	Power uint32 // kW (gisce)
}

// ContractedPowers is the body of M_PC_NA_2: the contracted powers and a
// time a (gisce/iec870ree M_PC_NA_2).
type ContractedPowers struct {
	Powers []ContractedPower
	Time   iec102.TimeA
}

// Type returns M_PC_NA_2.
func (ContractedPowers) Type() iec102.TypeID { return M_PC_NA_2 }

// Objects returns the number of powers.
func (b ContractedPowers) Objects() int { return len(b.Powers) }

// Sequence marks a body with a variable number of objects.
func (ContractedPowers) Sequence() {}

// AppendTo appends the powers and the time.
func (b ContractedPowers) AppendTo(out []byte, _ iec102.Params) ([]byte, error) {
	for _, p := range b.Powers {
		out = iec102.AppendUint(append(out, p.Addr), p.Power, 4)
	}
	return b.Time.AppendTo(out), nil
}

// SpecialDays is the body of M_DF_NA_2: one time a per special day
// (gisce/iec870ree M_DF_NA_2).
type SpecialDays struct{ Days []iec102.TimeA }

// Type returns M_DF_NA_2.
func (SpecialDays) Type() iec102.TypeID { return M_DF_NA_2 }

// Objects returns the number of days.
func (b SpecialDays) Objects() int { return len(b.Days) }

// Sequence marks a body with a variable number of objects.
func (SpecialDays) Sequence() {}

// AppendTo appends the days.
func (b SpecialDays) AppendTo(out []byte, _ iec102.Params) ([]byte, error) {
	for _, d := range b.Days {
		out = d.AppendTo(out)
	}
	return out, nil
}

// Profile decodes the REE bodies of this package; set it as
// iec102.Params.Profile. Types it does not know are left to the standard
// codec (and so become iec102.Raw).
var Profile iec102.Profile = profile{}

type profile struct{}

// DecodeBody implements iec102.Profile.
func (profile) DecodeBody(a *iec102.ASDU, data []byte, _ iec102.Params) (iec102.Body, error) {
	r := iec102.NewReader(data)
	n := a.VSQ.Number()
	var b iec102.Body
	switch a.Type {
	case C_AC_NA_2:
		b = AccessKey{Key: r.U32(4)}
	case C_CS_TA_2:
		b = SetClock{Time: r.TimeB()}
	case C_FS_NA_2, C_TA_VC_2, C_RM_NA_2, C_PC_NA_2, C_DF_NA_2, C_TA_VM_2, C_CB_NT_2, C_CB_UN_2:
		obj, times, _ := requestLayout(a.Type)
		q := Request{T: a.Type}
		if obj {
			q.Object = r.U8()
		}
		if times == 2 {
			q.From, q.To = r.TimeA(), r.TimeA()
		}
		b = q
	case M_TA_VC_2, M_TA_VM_2:
		if len(data) != n*tariffRecordSize {
			return nil, fmt.Errorf("%w: %d octets for %d tariff record(s) of %d", iec102.ErrInvalid, len(data), n, tariffRecordSize)
		}
		t := TariffInfo{T: a.Type, Records: make([]TariffRecord, n)}
		for i := range t.Records {
			x := &t.Records[i]
			x.Addr = r.U8()
			x.ActiveAbs, x.ActiveInc, x.ActiveQual = r.U32(4), r.U32(4), r.U8()
			x.ReactiveIndAbs, x.ReactiveIndInc, x.ReactiveIndQual = r.U32(4), r.U32(4), r.U8()
			x.ReactiveCapAbs, x.ReactiveCapInc, x.ReactiveCapQual = r.U32(4), r.U32(4), r.U8()
			x.Reserved7, x.Reserved7Qual = r.U32(4), r.U8()
			x.Reserved8, x.Reserved8Qual = r.U32(4), r.U8()
			x.MaxPower, x.MaxPowerTime, x.MaxPowerQual = r.U32(4), r.TimeA(), r.U8()
			x.ExcessPower, x.ExcessPowerQual = r.U32(4), r.U8()
			x.Start, x.End = r.TimeA(), r.TimeA()
		}
		b = t
	case M_PC_NA_2:
		c := ContractedPowers{Powers: make([]ContractedPower, n)}
		for i := range c.Powers {
			c.Powers[i] = ContractedPower{Addr: r.U8(), Power: r.U32(4)}
		}
		c.Time = r.TimeA()
		b = c
	case M_DF_NA_2:
		d := SpecialDays{Days: make([]iec102.TimeA, n)}
		for i := range d.Days {
			d.Days[i] = r.TimeA()
		}
		b = d
	default:
		return nil, nil
	}
	if err := r.Done(); err != nil {
		return nil, err
	}
	return b, nil
}
