// SPDX-License-Identifier: MIT

package e2e

import (
	"errors"
	"sync"
	"testing"

	"github.com/otfabric/go-iec60870-5/iec103"
	"github.com/otfabric/go-iec60870-5/iec103/server"
)

// offering is an equipment application that announces NOC channels of NOE
// values (ASDU 26) and then offers channels channels of values values each,
// in ASDUs of at most iec103.MaxValues.
type offering struct {
	noc      uint8
	noe      uint16
	channels int
	values   int

	mu   sync.Mutex
	next int // channels offered
	acks []iec103.TOO
}

func (q *offering) HandleASDU(s *server.Server, a *iec103.ASDU) {
	o, ok := a.Body.(iec103.DisturbanceOrder)
	if !ok {
		return
	}
	send := func(b iec103.Body) {
		_ = s.Send(iec103.New(iec103.CauseDisturbanceData, s.CommonAddr(), fun, 0, b), server.Class1)
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if o.Ack {
		q.acks = append(q.acks, o.TOO)
		switch o.TOO {
		case iec103.TOOTagsOK, iec103.TOOTagsFailed, iec103.TOOChannelOK, iec103.TOOChannelFailed:
			if q.next < q.channels {
				q.next++
				send(iec103.ChannelReady{FAN: 7, ACC: uint8(q.next)})
				return
			}
			send(iec103.EndOfTransmission{TOO: iec103.TOOEndData, FAN: 7})
		default:
		}
		return
	}
	switch o.TOO {
	case iec103.TOORequestList:
		send(iec103.DisturbanceList{Faults: []iec103.RecordedFault{{FAN: 7}}})
	case iec103.TOOSelectFault:
		send(iec103.DisturbanceReady{FAN: 7, NOF: 1, NOC: q.noc, NOE: q.noe, INT: 1000})
	case iec103.TOORequestData:
		send(iec103.TagsReady{FAN: 7})
	case iec103.TOORequestTags:
		send(iec103.EndOfTransmission{TOO: iec103.TOOEndTags, FAN: 7})
	case iec103.TOORequestChannel:
		for nfe := 0; nfe < q.values; nfe += iec103.MaxValues {
			n := min(iec103.MaxValues, q.values-nfe)
			send(iec103.DisturbanceValues{FAN: 7, ACC: o.ACC, NFE: uint16(nfe), Values: make([]int16, n)})
		}
		send(iec103.EndOfTransmission{TOO: iec103.TOOEndChannel, FAN: 7, ACC: o.ACC})
	default:
	}
}

// TestDisturbanceWithinAnnounced: the record holds at most what the ASDU 26
// announced, NOC channels of NOE values (clauses 7.2.6.10, 7.2.6.11). Fewer
// end with a negative acknowledgement; a value beyond NOE, or a channel
// beyond NOC, ends the transfer at once with ErrInvalid and no further
// order.
func TestDisturbanceWithinAnnounced(t *testing.T) {
	const noe = 60 // three ASDUs 30: 25, 25 and 10 values
	cases := []struct {
		name             string
		channels, values int
		noc              uint8
		err              error
		kept             int // channels in the record
		acks             []iec103.TOO
	}{
		{"NOE-1 values", 2, noe - 1, 2, iec103.ErrNegative, 2,
			[]iec103.TOO{iec103.TOOTagsOK, iec103.TOOChannelFailed, iec103.TOOChannelFailed, iec103.TOODataFailed}},
		{"NOE values", 2, noe, 2, nil, 2,
			[]iec103.TOO{iec103.TOOTagsOK, iec103.TOOChannelOK, iec103.TOOChannelOK, iec103.TOODataOK}},
		{"NOE+1 values", 2, noe + 1, 2, iec103.ErrInvalid, 1,
			[]iec103.TOO{iec103.TOOTagsOK}},
		{"NOC-1 channels", 1, noe, 2, iec103.ErrNegative, 1,
			[]iec103.TOO{iec103.TOOTagsOK, iec103.TOOChannelOK, iec103.TOODataFailed}},
		{"NOC+1 channels", 3, noe, 2, iec103.ErrInvalid, 2,
			[]iec103.TOO{iec103.TOOTagsOK, iec103.TOOChannelOK, iec103.TOOChannelOK}},
		{"a channel when NOC is 0", 1, noe, 0, iec103.ErrInvalid, 0,
			[]iec103.TOO{iec103.TOOTagsOK}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			q := &offering{noc: c.noc, noe: noe, channels: c.channels, values: c.values}
			r := start(t, options{custom: q, noInit: true, noDist: true})
			rec, err := r.c.Disturbance(ctx(t), fun, 7)
			if c.err == nil && err != nil || c.err != nil && !errors.Is(err, c.err) {
				t.Fatalf("%v, want %v", err, c.err)
			}
			if rec == nil || len(rec.Channels) != c.kept {
				t.Fatalf("record %+v, want %d channels", rec, c.kept)
			}
			for _, ch := range rec.Channels {
				if len(ch.Values) > noe {
					t.Fatalf("channel ACC %d with %d values, NOE %d", ch.ACC, len(ch.Values), noe)
				}
			}
			// The equipment handles orders in the order they arrive: once the
			// list it is asked for next has come back, it has seen every
			// order the transfer sent.
			if _, err := r.c.DisturbanceList(ctx(t), fun); err != nil {
				t.Fatal(err)
			}
			q.mu.Lock()
			defer q.mu.Unlock()
			if len(q.acks) != len(c.acks) {
				t.Fatalf("acknowledgements %v, want %v", q.acks, c.acks)
			}
			for i := range c.acks {
				if q.acks[i] != c.acks[i] {
					t.Fatalf("acknowledgements %v, want %v", q.acks, c.acks)
				}
			}
		})
	}
}
