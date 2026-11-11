// SPDX-License-Identifier: MIT

package server

import (
	"slices"
	"sync"

	"github.com/otfabric/go-iec60870-5/iec102"
)

// Store holds the records a server serves: integration periods of
// integrated totals and single-point information, per record address. It
// is safe for concurrent use; the application adds to it while the server
// runs.
type Store struct {
	mu     sync.Mutex
	totals map[uint8][]iec102.IntegratedTotals
	points map[uint8][]iec102.SinglePoint
}

// NewStore returns an empty store.
func NewStore() *Store {
	return &Store{totals: map[uint8][]iec102.IntegratedTotals{}, points: map[uint8][]iec102.SinglePoint{}}
}

// AddPeriod adds the integrated totals of one integration period to record
// address record. p.T (M_IT_TA_2 .. M_IT_TM_2) is the type it is served
// as, p.Time the end of the period. Periods are kept in the order added,
// which should be oldest first: "the oldest integration period" of
// C_CI_NA_2 and its siblings is the first one added of its family.
func (s *Store) AddPeriod(record uint8, p iec102.IntegratedTotals) {
	p.Totals = slices.Clone(p.Totals)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.totals[record] = append(s.totals[record], p)
}

// AddSinglePoints adds single-point information to record address record
// (normally a list address 50..55).
func (s *Store) AddSinglePoints(record uint8, pts ...iec102.SinglePoint) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.points[record] = append(s.points[record], pts...)
}

// minuteKey orders TimeA values by their date and time fields, ignoring
// the flags.
func minuteKey(t iec102.TimeA) uint32 {
	return ((((uint32(t.Year)*16+uint32(t.Month))*32+uint32(t.Day))*32+uint32(t.Hour))*64 + uint32(t.Minute))
}

// selectTotals returns the periods a read command selects, or the cause
// of the refusal.
func (s *Store) selectTotals(record uint8, q iec102.Read) ([]iec102.IntegratedTotals, iec102.Cause) {
	s.mu.Lock()
	all, known := s.totals[record]
	var fam []iec102.IntegratedTotals
	for _, p := range all {
		if p.T.Family() == q.T.Family() {
			fam = append(fam, p)
		}
	}
	s.mu.Unlock()
	switch {
	case !known:
		return nil, iec102.CauseUnknownRecordAddr
	case len(fam) == 0:
		return nil, iec102.CauseUnknownType
	}
	sel := q.T.Selection()
	var periods []iec102.IntegratedTotals
	switch sel {
	case iec102.SelectOldest, iec102.SelectOldestRange:
		periods = fam[:1]
	case iec102.SelectPeriod, iec102.SelectPeriodRange:
		for _, p := range fam {
			if minuteKey(p.Time) == minuteKey(q.From) {
				periods = append(periods, p)
			}
		}
	case iec102.SelectTimeRange:
		for _, p := range fam {
			if k := minuteKey(p.Time); k >= minuteKey(q.From) && k <= minuteKey(q.To) {
				periods = append(periods, p)
			}
		}
	default:
		return nil, iec102.CauseUnknownType
	}
	if len(periods) == 0 {
		return nil, iec102.CauseNoPeriod
	}
	if sel == iec102.SelectOldest || sel == iec102.SelectPeriod {
		return periods, 0
	}
	var out []iec102.IntegratedTotals
	for _, p := range periods {
		f := p
		f.Totals = nil
		for _, t := range p.Totals {
			if t.Addr >= q.First && t.Addr <= q.Last {
				f.Totals = append(f.Totals, t)
			}
		}
		if len(f.Totals) > 0 {
			out = append(out, f)
		}
	}
	if len(out) == 0 {
		return nil, iec102.CauseNoObject
	}
	return out, 0
}

// selectPoints returns the single points of a record, within q's time
// range for C_SP_NB_2; ok is false for an unknown record.
func (s *Store) selectPoints(record uint8, q iec102.Read) ([]iec102.SinglePoint, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	all, ok := s.points[record]
	if !ok || q.T == iec102.C_SP_NA_2 {
		return slices.Clone(all), ok
	}
	var out []iec102.SinglePoint
	for _, p := range all {
		if k := minuteKey(p.Time.TimeA); k >= minuteKey(q.From) && k <= minuteKey(q.To) {
			out = append(out, p)
		}
	}
	return out, true
}
