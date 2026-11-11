// SPDX-License-Identifier: MIT

package iec101

import (
	"errors"
	"fmt"

	"github.com/otfabric/go-iec60870-5/asdu"
)

// ErrProfile reports an ASDU that the IEC 60870-5-101 profile does not
// allow; the error is a [*ProfileError]. Test with errors.Is.
var ErrProfile = errors.New("iec101: not allowed by the IEC 60870-5-101 profile")

// ProfileError is why [Validate] refused an ASDU.
type ProfileError struct {
	// Cause is the cause of transmission with which a controlled station
	// mirrors a refused control-direction ASDU (IEC 60870-5-101:2003
	// 7.2.3): unknown type identification, cause of transmission, common
	// address or information object address. Zero when no "unknown ..."
	// cause fits; the ASDU is then dropped.
	Cause asdu.Cause
	// Reason describes the violation.
	Reason string
}

func (e *ProfileError) Error() string { return ErrProfile.Error() + ": " + e.Reason }

// Is makes errors.Is(err, ErrProfile) hold.
func (e *ProfileError) Is(target error) bool { return target == ErrProfile }

func refuse(cause asdu.Cause, format string, a ...any) error {
	return &ProfileError{Cause: cause, Reason: fmt.Sprintf(format, a...)}
}

// Defined101 reports whether IEC 60870-5-101:2003 7.2.1.1 defines type t:
// 1 to 21, 30 to 40, 45 to 51, 70, 100 to 106, 110 to 113 and 120 to 126,
// or whether t is in the private range 136 to 255. The other values are
// reserved; among them are types the codec knows from IEC 60870-5-104
// (58 to 64, 107, 127) and IEC TS 60870-5-7 (the security types, which
// this IEC 101 implementation does not carry).
func Defined101(t asdu.TypeID) bool {
	switch {
	case t >= 1 && t <= 21, t >= 30 && t <= 40, t >= 45 && t <= 51, t == 70,
		t >= 100 && t <= 106, t >= 110 && t <= 113, t >= 120 && t <= 126, t >= 136:
		return true
	}
	return false
}

// Validate checks an ASDU against the rules of the IEC 60870-5-101 profile
// that the codec, which IEC 60870-5-104 shares, does not enforce:
//
//   - the type is defined by IEC 60870-5-101 ([Defined101]);
//   - the cause of transmission is not 0 (7.2.3, table 14);
//   - the common address is not 0, and the global address is used only by
//     C_IC_NA_1, C_CI_NA_1, C_CS_NA_1 and C_RP_NA_1 (7.2.4);
//   - end of initialization and the system commands except read
//     (M_EI_NA_1, C_IC_NA_1, C_CI_NA_1, C_CS_NA_1, C_TS_NA_1, C_RP_NA_1,
//     C_CD_NA_1) carry one object, SQ = 0, with information object
//     address 0 (7.3.3, 7.3.4);
//   - information with a time tag is not sent as the answer to an
//     interrogation, causes 20 to 36 (7.3.1: the causes listed for the
//     time-tagged types).
//
// The client and server apply it when [Params].Strict is set.
func Validate(a *asdu.ASDU, p asdu.Params) error {
	if !Defined101(a.Type) {
		return refuse(asdu.CauseUnknownType, "type %s is reserved in IEC 60870-5-101", a.Type)
	}
	if a.Cause == 0 {
		return refuse(asdu.CauseUnknownCause, "cause of transmission 0")
	}
	if a.CommonAddr == 0 {
		return refuse(asdu.CauseUnknownCommonAddr, "common address 0")
	}
	if p.IsBroadcast(a.CommonAddr) {
		switch a.Type {
		case asdu.C_IC_NA_1, asdu.C_CI_NA_1, asdu.C_CS_NA_1, asdu.C_RP_NA_1:
		default:
			return refuse(asdu.CauseUnknownCommonAddr, "global common address with %s", a.Type)
		}
	}
	switch a.Type {
	case asdu.M_EI_NA_1, asdu.C_IC_NA_1, asdu.C_CI_NA_1, asdu.C_CS_NA_1, asdu.C_TS_NA_1, asdu.C_RP_NA_1, asdu.C_CD_NA_1:
		if a.Sequence || len(a.Objects) != 1 {
			return refuse(0, "%s with SQ=%v and %d objects, want one object with SQ=0", a.Type, a.Sequence, len(a.Objects))
		}
		if ioa := a.Objects[0].Address(); ioa != 0 {
			return refuse(asdu.CauseUnknownIOA, "%s with information object address %d, want 0", a.Type, ioa)
		}
	}
	if a.Type.HasTimeTag() && !a.Type.InControlDirection() &&
		a.Cause >= asdu.CauseInterrogatedStation && a.Cause <= asdu.CauseInterrogatedStation+16 {
		return refuse(asdu.CauseUnknownCause, "%s with cause %s: interrogated information has no time tag", a.Type, a.Cause)
	}
	return nil
}
