// SPDX-License-Identifier: MIT

package iec101

import (
	"errors"
	"testing"

	"github.com/otfabric/go-iec60870-5/asdu"
)

// TestValidate traces the IEC 60870-5-101:2003 profile checks: reserved
// types (7.2.1.1), cause 0 (7.2.3), common address 0 and the global
// address (7.2.4), the system types with one object at address 0 (7.3.3,
// 7.3.4) and time-tagged information as an interrogation answer (7.3.1).
func TestValidate(t *testing.T) {
	p := DefaultParams().ASDU
	now := asdu.Now()
	cases := []struct {
		name string
		a    *asdu.ASDU
		want asdu.Cause // 0 with ok: accepted
		ok   bool
	}{
		{"single point", asdu.New(asdu.CauseInterrogatedStation, 1, asdu.SinglePoint{IOA: 1}), 0, true},
		{"interrogation to all", asdu.New(asdu.CauseActivation, 255, asdu.Interrogation{Qualifier: asdu.QOIStation}), 0, true},
		{"counter with time, spontaneous", asdu.New(asdu.CauseSpontaneous, 1, asdu.IntegratedTotal{IOA: 1, Time: now}), 0, true},
		{"private type", &asdu.ASDU{Type: 200, Cause: asdu.CauseSpontaneous, CommonAddr: 1}, 0, true},
		{"104 command with time", asdu.New(asdu.CauseActivation, 1, asdu.SingleCommand{IOA: 1, Time: now}), asdu.CauseUnknownType, false},
		{"104 test command with time", &asdu.ASDU{Type: asdu.C_TS_TA_1, Cause: asdu.CauseActivation, CommonAddr: 1}, asdu.CauseUnknownType, false},
		{"reserved 128", &asdu.ASDU{Type: 128, Cause: asdu.CauseSpontaneous, CommonAddr: 1}, asdu.CauseUnknownType, false},
		{"cause 0", asdu.New(0, 1, asdu.SinglePoint{IOA: 1}), asdu.CauseUnknownCause, false},
		{"common address 0", asdu.New(asdu.CauseSpontaneous, 0, asdu.SinglePoint{IOA: 1}), asdu.CauseUnknownCommonAddr, false},
		{"read to all", asdu.New(asdu.CauseRequest, 255, asdu.Read{IOA: 1}), asdu.CauseUnknownCommonAddr, false},
		{"interrogation at IOA 5", asdu.New(asdu.CauseActivation, 1, asdu.Interrogation{IOA: 5, Qualifier: asdu.QOIStation}), asdu.CauseUnknownIOA, false},
		{"two interrogations", asdu.New(asdu.CauseActivation, 1, asdu.Interrogation{Qualifier: 20}, asdu.Interrogation{Qualifier: 20}), 0, false},
		{"time tag in an interrogation answer", asdu.New(asdu.CauseInterrogatedStation+3, 1, asdu.SinglePoint{IOA: 1, Time: now}), asdu.CauseUnknownCause, false},
	}
	for _, c := range cases {
		err := Validate(c.a, p)
		if c.ok {
			if err != nil {
				t.Errorf("%s: %v", c.name, err)
			}
			continue
		}
		var pe *ProfileError
		if !errors.Is(err, ErrProfile) || !errors.As(err, &pe) || pe.Cause != c.want || pe.Error() == "" {
			t.Errorf("%s: %v, want cause %v", c.name, err, c.want)
		}
	}
	if !Defined101(asdu.M_ME_ND_1) || Defined101(22) || Defined101(asdu.F_SC_NB_1) {
		t.Error("Defined101")
	}
}
