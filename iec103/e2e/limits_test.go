// SPDX-License-Identifier: MIT

package e2e

import (
	"testing"

	"github.com/otfabric/go-iec60870-5/iec103"
)

// TestDisturbanceTooLarge selects records whose NOC or NOE does not fit
// its field (one and two octets, clauses 7.2.6.10 and 7.2.6.11): the
// server ends the transfer with TOO 34 instead of announcing a truncated
// count.
func TestDisturbanceTooLarge(t *testing.T) {
	r := start(t, options{noInit: true, handler: true})
	for name, chs := range map[string][]iec103.Channel{
		"NOC": make([]iec103.Channel, 256),
		"NOE": {{ACC: 1, Values: make([]int16, 1<<16)}},
	} {
		r.rec.rec.Channels = chs
		if e := r.order(t, iec103.DisturbanceOrder{TOO: iec103.TOOSelectFault, FAN: 7}); e.TOO != iec103.TOOEndDataAbortProt {
			t.Fatalf("%s: %+v", name, e)
		}
	}
}
