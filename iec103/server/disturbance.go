// SPDX-License-Identifier: MIT

package server

import (
	"math"

	"github.com/otfabric/go-iec60870-5/iec103"
)

// DisturbanceSource supplies the recorded disturbances for the transfer of
// clause 7.4.7. Calls come from the server's handler goroutine.
type DisturbanceSource interface {
	// List returns the recorded disturbances, at most
	// [iec103.MaxDisturbances], for ASDU 23.
	List() []iec103.RecordedFault
	// Record returns the record of fault number fan.
	Record(fan uint16) (*iec103.Disturbance, bool)
	// Transmitted is called with the final acknowledgement of a transfer
	// (ASDU 25, TOO 64 or 65). Clause 7.4.7: the record may be deleted
	// only after a positive acknowledgement.
	Transmitted(fan uint16, ok bool)
}

// distState is the transfer in progress.
type distState struct {
	rec  *iec103.Disturbance
	next int // index of the channel to offer next
}

// SendDisturbanceList queues the list of recorded disturbances (ASDU 23,
// cause 31) of the [DisturbanceSource]: how the equipment announces a new
// disturbance (clause 7.4.7).
func (s *Server) SendDisturbanceList() error { return s.sendList(noCycle) }

// sendList queues ASDU 23 with the function type of the equipment (clause
// 7.3.1.11).
func (s *Server) sendList(cy cycle) error {
	if s.cfg.Disturbances == nil {
		return nil
	}
	l := s.cfg.Disturbances.List()
	if len(l) > iec103.MaxDisturbances {
		l = l[len(l)-iec103.MaxDisturbances:]
	}
	return s.send(iec103.New(iec103.CauseDisturbanceData, s.ca, s.cfg.Fun, 0, iec103.DisturbanceList{Faults: l}), Class1, cy)
}

func (s *Server) distSend(fun iec103.FunctionType, b iec103.Body) {
	if err := s.Send(iec103.New(iec103.CauseDisturbanceData, s.ca, fun, 0, b), Class1); err != nil {
		s.log.Warn("disturbance data not queued", "err", err)
	}
}

func (s *Server) end(fun iec103.FunctionType, too iec103.TOO, tov uint8, fan uint16, acc uint8) {
	s.distSend(fun, iec103.EndOfTransmission{TOO: too, TOV: tov, FAN: fan, ACC: acc})
}

// current returns the transfer in progress for fan, or nil.
func (s *Server) current(fan uint16) *distState {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dist == nil || s.dist.rec.FAN != fan {
		return nil
	}
	return s.dist
}

func (s *Server) setDist(d *distState) {
	s.mu.Lock()
	s.dist = d
	s.mu.Unlock()
}

// abort ends the transfer in progress with the end of disturbance data
// aborted by the protection equipment (TOO 34), then sends the updated
// list (clause 7.4.7: after a completed or aborted transfer).
func (s *Server) abort(fun iec103.FunctionType, rec *iec103.Disturbance) {
	s.setDist(nil)
	s.end(fun, iec103.TOOEndDataAbortProt, rec.TOV, rec.FAN, iec103.ACCGlobal)
	_ = s.sendList(noCycle)
}

// disturbance runs one step of the transfer of clause 7.4.7 (figure 10).
// An order that does not fit the transfer in progress ("implausible
// orders", clause 7.4.7) is answered with the end of disturbance data
// aborted by the protection equipment (TOO 34), which ends the transfer;
// that choice of TOO is our reading of the clause. Without a transfer in
// progress the TOO 34 answers the order and no list follows.
func (s *Server) disturbance(a *iec103.ASDU, o iec103.DisturbanceOrder) {
	fun := a.Fun
	src := s.cfg.Disturbances
	if !o.Ack {
		switch o.TOO {
		case iec103.TOORequestList:
			_ = s.sendList(noCycle)
			return
		case iec103.TOOSelectFault:
			rec, ok := src.Record(o.FAN)
			noc, noe := 0, 0
			if ok && len(rec.Channels) > 0 {
				noc, noe = len(rec.Channels), len(rec.Channels[0].Values)
			}
			// NOC is one octet and NOE two (clauses 7.2.6.10, 7.2.6.11): a
			// record that does not fit them cannot be announced.
			if !ok || noc > math.MaxUint8 || noe > math.MaxUint16 {
				s.end(fun, iec103.TOOEndDataAbortProt, o.TOV, o.FAN, iec103.ACCGlobal)
				return
			}
			s.setDist(&distState{rec: rec})
			s.distSend(fun, iec103.DisturbanceReady{TOV: rec.TOV, FAN: rec.FAN, NOF: rec.NOF,
				NOC: uint8(noc), NOE: uint16(noe), INT: rec.Interval, Time: rec.Start})
			return
		default:
		}
	}
	d := s.current(o.FAN)
	if d == nil {
		s.end(fun, iec103.TOOEndDataAbortProt, o.TOV, o.FAN, iec103.ACCGlobal)
		return
	}
	rec := d.rec
	if o.Ack {
		switch o.TOO {
		case iec103.TOOTagsOK, iec103.TOOTagsFailed, iec103.TOOChannelOK, iec103.TOOChannelFailed:
			s.offerChannel(fun, d)
		case iec103.TOODataOK, iec103.TOODataFailed:
			s.setDist(nil)
			src.Transmitted(rec.FAN, o.TOO == iec103.TOODataOK)
			_ = s.sendList(noCycle)
		default:
			s.abort(fun, rec)
		}
		return
	}
	switch o.TOO {
	case iec103.TOORequestData:
		s.distSend(fun, iec103.TagsReady{FAN: rec.FAN})
	case iec103.TOORequestTags:
		s.sendTags(fun, rec)
		s.end(fun, iec103.TOOEndTags, rec.TOV, rec.FAN, iec103.ACCGlobal)
	case iec103.TOORequestChannel:
		s.sendChannel(fun, rec, o.ACC)
	case iec103.TOOAbortData:
		s.setDist(nil)
		s.end(fun, iec103.TOOEndDataAbortCtrl, rec.TOV, rec.FAN, iec103.ACCGlobal)
		_ = s.sendList(noCycle)
	case iec103.TOOAbortChannel:
		s.end(fun, iec103.TOOEndChannelAbortCtl, rec.TOV, rec.FAN, o.ACC)
	case iec103.TOOAbortTags:
		s.end(fun, iec103.TOOEndTagsAbortCtrl, rec.TOV, rec.FAN, iec103.ACCGlobal)
	default:
		s.abort(fun, rec)
	}
}

// offerChannel offers the next channel (ASDU 27), or ends the disturbance
// data (ASDU 31, TOO 32) after the last.
func (s *Server) offerChannel(fun iec103.FunctionType, d *distState) {
	s.mu.Lock()
	i := d.next
	d.next++
	s.mu.Unlock()
	rec := d.rec
	if i >= len(rec.Channels) {
		s.end(fun, iec103.TOOEndData, rec.TOV, rec.FAN, iec103.ACCGlobal)
		return
	}
	ch := rec.Channels[i]
	s.distSend(fun, iec103.ChannelReady{TOV: rec.TOV, FAN: rec.FAN, ACC: ch.ACC, RPV: ch.RPV, RSV: ch.RSV, RFA: ch.RFA})
}

// sendTags sends the tags in ASDUs 29: consecutive tags with the same
// position, at most [iec103.MaxTags] per ASDU (clause 7.3.1.15).
func (s *Server) sendTags(fun iec103.FunctionType, rec *iec103.Disturbance) {
	var cur *iec103.Tags
	flush := func() {
		if cur != nil {
			s.distSend(fun, *cur)
			cur = nil
		}
	}
	for _, t := range rec.Tags {
		if cur != nil && (cur.TAP != t.Pos || len(cur.Tags) == iec103.MaxTags) {
			flush()
		}
		if cur == nil {
			cur = &iec103.Tags{FAN: rec.FAN, TAP: t.Pos}
		}
		cur.Tags = append(cur.Tags, t.Tag)
	}
	flush()
}

// sendChannel sends the values of channel acc in ASDUs 30 of at most
// [iec103.MaxValues], then the end of the channel (clause 7.4.7).
func (s *Server) sendChannel(fun iec103.FunctionType, rec *iec103.Disturbance, acc uint8) {
	for _, ch := range rec.Channels {
		if ch.ACC != acc {
			continue
		}
		for i := 0; i < len(ch.Values); i += iec103.MaxValues {
			j := min(i+iec103.MaxValues, len(ch.Values))
			s.distSend(fun, iec103.DisturbanceValues{TOV: rec.TOV, FAN: rec.FAN, ACC: acc, NFE: uint16(i), Values: ch.Values[i:j]})
		}
		s.end(fun, iec103.TOOEndChannel, rec.TOV, rec.FAN, acc)
		return
	}
	s.end(fun, iec103.TOOEndChannelAbortPrt, rec.TOV, rec.FAN, acc)
}
