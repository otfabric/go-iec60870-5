//go:build interop

// SPDX-License-Identifier: MIT

package interop

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/otfabric/go-iec60870-5/cp"
	"github.com/otfabric/go-iec60870-5/iec103"
)

// genericEntry is a generic entry of the IEC 103 fixture (contract 2.3,
// iec60870-5-interop docs/FIXTURES.md "Generic data").
type genericEntry struct {
	GIN struct {
		Group uint8 `json:"group"`
		Entry uint8 `json:"entry"`
	} `json:"gin"`
	Description string          `json:"description"`
	Type        int             `json:"type"`
	Size        int             `json:"size"`
	Value       json.RawMessage `json:"value"`
	Writable    bool            `json:"writable"`
}

func (g genericEntry) gin() iec103.GIN { return iec103.GIN{Group: g.GIN.Group, Entry: g.GIN.Entry} }

// actual is the fixture's value as the GID of its data type and size.
func (g genericEntry) actual() iec103.Value {
	switch g.Type {
	case 1:
		var s string
		_ = json.Unmarshal(g.Value, &s)
		return iec103.ASCII(s)
	case 3:
		var n uint64
		_ = json.Unmarshal(g.Value, &n)
		return iec103.Uints(iec103.DataUint, g.Size, n)
	case 4:
		var n int64
		_ = json.Unmarshal(g.Value, &n)
		return iec103.Ints(g.Size, n)
	default:
		var f float64
		_ = json.Unmarshal(g.Value, &f)
		return iec103.Float32s(float32(f))
	}
}

// want is the value as the result documents write it: a string (type 1)
// or a number.
func (g genericEntry) want() any {
	var v any
	_ = json.Unmarshal(g.Value, &v)
	return v
}

// fixtureDisturbance is a recorded disturbance of the IEC 103 fixture.
type fixtureDisturbance struct {
	FAN int `json:"fan"`
	SOF struct {
		TP   bool `json:"tp"`
		TM   bool `json:"tm"`
		Test bool `json:"test"`
		OTEV bool `json:"otev"`
	} `json:"sof"`
	Time     string `json:"time"`
	NOF      int    `json:"nof"`
	Interval int    `json:"interval"`
	Start    string `json:"start"`
	Tags     []tag  `json:"tags"`
	Channels []struct {
		ACC    int     `json:"acc"`
		RPV    float64 `json:"rpv"`
		RSV    float64 `json:"rsv"`
		RFA    float64 `json:"rfa"`
		Values []int16 `json:"values"`
	} `json:"channels"`
}

type tag struct {
	Pos *int `json:"pos"`
	Fun int  `json:"fun"`
	Inf int  `json:"inf"`
	DPI int  `json:"dpi"`
}

func (d fixtureDisturbance) sof() iec103.SOF {
	var s iec103.SOF
	for bit, on := range map[iec103.SOF]bool{iec103.SOFTrip: d.SOF.TP, iec103.SOFTM: d.SOF.TM, iec103.SOFTest: d.SOF.Test, iec103.SOFOTEV: d.SOF.OTEV} {
		if on {
			s |= bit
		}
	}
	return s
}

func (d fixtureDisturbance) time() time.Time {
	t, _ := time.Parse("2006-01-02T15:04:05.000Z", d.Time)
	return t
}

// start is the time of the first value: the fixture's time of day on the
// date of the fault (ASDU 26 carries a CP32Time2a, no date).
func (d fixtureDisturbance) start() time.Time {
	day := d.time().Truncate(24 * time.Hour)
	tod, _ := time.Parse("15:04:05.000", d.Start)
	return day.Add(time.Duration(tod.Hour())*time.Hour + time.Duration(tod.Minute())*time.Minute +
		time.Duration(tod.Second())*time.Second + time.Duration(tod.Nanosecond()))
}

// The result documents of contract 2.3.
type (
	genericDoc struct {
		Cot  int      `json:"cot"`
		Sets []setDoc `json:"sets"`
	}
	setDoc struct {
		GIN    string `json:"gin"`
		KOD    int    `json:"kod"`
		Type   int    `json:"type"`
		Size   int    `json:"size"`
		Number int    `json:"number"`
		Values []any  `json:"values"`
		Data   string `json:"data,omitempty"`
	}
	faultDoc struct {
		FAN int `json:"fan"`
		SOF struct {
			TP   bool `json:"tp"`
			TM   bool `json:"tm"`
			Test bool `json:"test"`
			OTEV bool `json:"otev"`
		} `json:"sof"`
		Time string `json:"time"`
	}
	disturbanceDoc struct {
		FAN      int          `json:"fan"`
		TOV      *int         `json:"tov"`
		NOF      *int         `json:"nof"`
		NOC      int          `json:"noc"`
		NOE      int          `json:"noe"`
		Interval int          `json:"interval"`
		Start    string       `json:"start"`
		Tags     []tag        `json:"tags"`
		Channels []channelDoc `json:"channels"`
	}
	channelDoc struct {
		ACC    int     `json:"acc"`
		RPV    float64 `json:"rpv"`
		RSV    float64 `json:"rsv"`
		RFA    float64 `json:"rfa"`
		Values []int   `json:"values"`
	}
)

// genericRead answers a read of a single entry (ASDU 21, INF 244, clause
// 7.4.8.6.2) as the fixture's station does: KOD 1 the actual value, KOD
// 10 the description, with cause 42; anything else with cause 43 and the
// GIN with a generic reply code (1: no such GIN, 2: no such attribute).
// The station answers no other generic command.
func (st *station) genericRead(s interface {
	GenericAnswer(*iec103.ASDU, iec103.Cause, ...iec103.DataSet) error
}, a *iec103.ASDU, b iec103.GenericCommand) {
	if a.Inf != iec103.InfReadEntry || a.Cause != iec103.CauseGenericRead {
		return
	}
	cause := iec103.CauseGenericReadValid
	var sets []iec103.DataSet
	for _, it := range b.Items {
		var desc string
		v, ok := st.generic[it.GIN]
		for _, g := range st.f.Generic {
			if g.gin() == it.GIN {
				desc = g.Description
			}
		}
		switch {
		case !ok:
			cause = iec103.CauseGenericReadInvalid
			sets = append(sets, iec103.DataSet{GIN: it.GIN, KOD: it.KOD, Value: iec103.Reply(iec103.GRCInvalidGIN)})
		case it.KOD == iec103.KODActualValue:
			sets = append(sets, iec103.DataSet{GIN: it.GIN, KOD: it.KOD, Value: v})
		case it.KOD == iec103.KODDescription:
			sets = append(sets, iec103.DataSet{GIN: it.GIN, KOD: it.KOD, Value: iec103.ASCII(desc)})
		default:
			cause = iec103.CauseGenericReadInvalid
			sets = append(sets, iec103.DataSet{GIN: it.GIN, KOD: it.KOD, Value: iec103.Reply(iec103.GRCNotExist)})
		}
	}
	if cause == iec103.CauseGenericReadInvalid {
		// Clause 7.4.8.6.2: the answer is marked with cause 43 and may
		// carry the GIN with a GRC for each failed element.
		var failed []iec103.DataSet
		for _, d := range sets {
			if d.Value.Type == iec103.DataGRC {
				failed = append(failed, d)
			}
		}
		sets = failed
	}
	_ = s.GenericAnswer(a, cause, sets...)
}

// genericWrite answers a write with execution (ASDU 10, cause 40, INF 250,
// clause 7.4.8.6.3, figure 17): every data set of KOD 1 to a writable entry
// with the entry's data type and size is applied and the station answers
// cause 40 with no data sets; otherwise nothing is applied and it answers
// cause 41 with the GIN and a generic reply code of each failed set (1: no
// such GIN, 8: read only, 5: another data type, size or attribute).
func (st *station) genericWrite(s interface {
	GenericAnswer(*iec103.ASDU, iec103.Cause, ...iec103.DataSet) error
}, a *iec103.ASDU, b iec103.GenericData) {
	if a.Inf != iec103.InfWriteExecute || a.Cause != iec103.CauseGenericWrite {
		return
	}
	var failed []iec103.DataSet
	for _, d := range b.Sets {
		grc := iec103.GRCAck
		var entry *genericEntry
		for i := range st.f.Generic {
			if st.f.Generic[i].gin() == d.GIN {
				entry = &st.f.Generic[i]
			}
		}
		switch {
		case entry == nil:
			grc = iec103.GRCInvalidGIN
		case !entry.Writable:
			grc = iec103.GRCReadOnly
		case d.KOD != iec103.KODActualValue || int(d.Value.Type) != entry.Type || int(d.Value.Size) != entry.Size || d.Value.Number != 1:
			grc = iec103.GRCOutOfRange
		}
		if grc != iec103.GRCAck {
			failed = append(failed, iec103.DataSet{GIN: d.GIN, KOD: d.KOD, Value: iec103.Reply(grc)})
		}
	}
	if failed != nil {
		_ = s.GenericAnswer(a, iec103.CauseGenericWriteNack, failed...)
		return
	}
	for _, d := range b.Sets {
		st.generic[d.GIN] = d.Value
	}
	_ = s.GenericAnswer(a, iec103.CauseGenericWriteAck)
}

// List, Record and Transmitted serve the fixture's recorded disturbances
// (server.DisturbanceSource); the records stay after a transfer.
func (st *station) List() []iec103.RecordedFault {
	var l []iec103.RecordedFault
	for _, d := range st.f.Disturbances {
		l = append(l, iec103.RecordedFault{FAN: uint16(d.FAN), SOF: d.sof(), Time: cp.At(d.time())})
	}
	return l
}

func (st *station) Record(fan uint16) (*iec103.Disturbance, bool) {
	for _, d := range st.f.Disturbances {
		if d.FAN != int(fan) {
			continue
		}
		rec := &iec103.Disturbance{
			Fun: iec103.FunctionType(st.f.Station.Identification.Fun), TOV: iec103.TOVInstantaneous, FAN: fan,
			NOF: uint16(d.NOF), Interval: uint16(d.Interval), Start: cp.At(d.start()),
		}
		for _, t := range d.Tags {
			rec.Tags = append(rec.Tags, iec103.TagChange{Pos: uint16(*t.Pos),
				Tag: iec103.Tag{Fun: iec103.FunctionType(t.Fun), Inf: iec103.InfoNumber(t.Inf), DPI: iec103.DPI(t.DPI)}})
		}
		for _, c := range d.Channels {
			rec.Channels = append(rec.Channels, iec103.Channel{ACC: uint8(c.ACC), RPV: float32(c.RPV), RSV: float32(c.RSV), RFA: float32(c.RFA),
				Values: c.Values})
		}
		return rec, true
	}
	return nil, false
}

func (st *station) Transmitted(uint16, bool) {}

// wantDisturbance is what a transfer of d must return, in the form of the
// result document (contract 2.3): its description, tags and channels.
func wantDisturbance(d fixtureDisturbance) disturbanceDoc {
	w := disturbanceDoc{FAN: d.FAN, NOC: len(d.Channels), Interval: d.Interval, Start: d.Start, Tags: d.Tags}
	if len(d.Channels) > 0 {
		w.NOE = len(d.Channels[0].Values)
	}
	for _, c := range d.Channels {
		ch := channelDoc{ACC: c.ACC, RPV: c.RPV, RSV: c.RSV, RFA: c.RFA}
		for _, v := range c.Values {
			ch.Values = append(ch.Values, int(v))
		}
		w.Channels = append(w.Channels, ch)
	}
	return w
}

// valuesOf renders the GID of a data set the way the result documents do
// (contract 2.3): the text of type 1, the numbers of types 3, 4, 7 and 22.
func valuesOf(v iec103.Value) []any {
	var out []any
	switch v.Type {
	case iec103.DataASCII:
		return []any{strings.TrimRight(v.Text(), "\x00")}
	case iec103.DataUint, iec103.DataGRC:
		for i := range int(v.Number) {
			n, _ := v.Uint(i)
			out = append(out, float64(n))
		}
	case iec103.DataInt:
		for i := range int(v.Number) {
			n, _ := v.Int(i)
			out = append(out, float64(n))
		}
	case iec103.DataFloat32, iec103.DataFloat64:
		for i := range int(v.Number) {
			f, _ := v.Float(i)
			out = append(out, f)
		}
	default:
		return []any{fmt.Sprintf("%x", v.Data)}
	}
	return out
}
