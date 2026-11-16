// SPDX-License-Identifier: MIT

package e2e_test

import (
	"fmt"
	"net"
	"testing"

	"github.com/otfabric/go-iec60870-5/iec104/internal/station"
)

// The e2e stations listen on the loopback address they are dialled on and
// on no other interface: a process that listens on a specific address of
// the same port cannot take their connections.
func TestStationListensOnLoopback(t *testing.T) {
	for name, st := range map[string]*station.Station{
		"Start":      station.Start(t, station.Baseline(t)),
		"StartGroup": station.StartGroup(t, station.Baseline(t)),
	} {
		addr, ok := st.Server.Addr().(*net.TCPAddr)
		if !ok || !addr.IP.Equal(net.IPv4(127, 0, 0, 1)) {
			t.Errorf("%s: the station listens on %v, want 127.0.0.1", name, st.Server.Addr())
		}
		if st.Addr != st.Loopback() {
			t.Errorf("%s: Addr %s, Loopback %s", name, st.Addr, st.Loopback())
		}
	}
}

// A fixture's report cause must be a cause of transmission (1..63); 0 means
// the default, return information caused by a remote command.
func TestStationFixtureReportCause(t *testing.T) {
	for _, c := range []struct {
		cause int
		ok    bool
	}{{-1, false}, {0, true}, {1, true}, {11, true}, {63, true}, {64, false}, {256 + 11, false}} {
		_, err := station.Parse(fmt.Appendf(nil, `{"station": {"commonAddress": 1},
			"points": [{"ioa": 100, "type": "M_SP_NA_1", "value": true}],
			"commands": [{"ioa": 500, "type": "C_SC_NA_1", "target": 100, "reportCause": %d}]}`, c.cause))
		if (err == nil) != c.ok {
			t.Errorf("report cause %d: %v", c.cause, err)
		}
	}
}
