// SPDX-License-Identifier: MIT

package e2e

import (
	"testing"

	"github.com/otfabric/go-iec60870-5/iec102"
	"github.com/otfabric/go-iec60870-5/iec102/client"
	"github.com/otfabric/go-iec60870-5/iec102/server"
)

// With PaceAnswers the server's answer to a read longer than its class 2
// queue (48 periods and a queue of 4) reaches the client whole, as the
// client's polls take the queue.
func TestPacedAnswers(t *testing.T) {
	r := newRig(t, func(_ *client.Config, s *server.Config) {
		s.QueueSize = 4
		s.PaceAnswers = true
	})
	c := ctx(t)
	got, err := r.c.ReadTotals(c, 11, iec102.Read{T: iec102.C_CI_NU_2, First: 1, Last: 8, From: hour(1, 0), To: hour(3, 0)})
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, p := range got {
		n += len(p.Totals)
	}
	if len(got) != 48 || n != 288 || got[47].Totals[5].Value != 470+6 {
		t.Fatalf("%d periods, %d totals", len(got), n)
	}
	pts, err := r.c.ReadSinglePoints(c, 52, nil, nil)
	if err != nil || len(pts) != 40 {
		t.Fatalf("%d single points, %v", len(pts), err)
	}
}
