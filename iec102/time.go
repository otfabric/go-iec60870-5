// SPDX-License-Identifier: MIT

package iec102

import (
	"fmt"
	"time"
)

// Encoded sizes of the time formats in octets.
const (
	SizeTimeA = 5
	SizeTimeB = 7
)

// TimeA is the five-octet binary time "time a" of IEC 60870-5-102,
// CP40Time2a: minute, hour, day of month with day of week, month and
// two-digit year, with the flags IV (invalid), TIS (tariff information
// switch), SU (summer time), ETI (energy tariff information) and PTI
// (power tariff information).
//
// Source: Beckhoff TS650x manual, section 9.1.12 (T_CP40Time2a); the same
// bit positions are used by gisce/iec870ree (TimeA) and
// daiiniel/IEC60870-5-102 (CP40Time2a), and the recorded traffic of an
// Actaris SL762B meter (testdata/innowatt) decodes consistently with them.
// It is not a time tag of IEC 60870-5-4: TIS, ETI and PTI exist only here.
//
// The fields hold the octets as received, so that a value that does not
// form a valid date (all zero, for example) survives decoding and encoding
// unchanged; [TimeA.Time] converts to a time.Time.
type TimeA struct {
	Minute uint8 // 0..59 (6 bits)
	Hour   uint8 // 0..23 (5 bits)
	Day    uint8 // day of month 1..31 (5 bits)
	// Weekday is the day of week, 1 (Monday) .. 7 (Sunday), 0 not used
	// (3 bits). The recorded traffic sends 7 for a Sunday and 3 for a
	// Wednesday.
	Weekday uint8
	Month   uint8 // 1..12 (4 bits)
	// Year is the year of the century, 0..99 (7 bits). [TimeA.Time] reads
	// it as 2000..2099, as gisce/iec870ree and daiiniel/IEC60870-5-102 do;
	// the manual names no century.
	Year uint8

	Invalid    bool  // IV: the time is not valid
	TIS        bool  // tariff information switch (minute octet, bit 6)
	SummerTime bool  // SU (hour octet, bit 7)
	ETI        uint8 // energy tariff information 0..3 (month octet, bits 4-5)
	PTI        uint8 // power tariff information 0..3 (month octet, bits 6-7)

	// Res1 holds the two spare bits 5 and 6 of the hour octet and Res2 the
	// spare bit 7 of the year octet, kept so that encoding reproduces the
	// received octets.
	Res1 uint8
	Res2 bool
}

// TimeAOf returns the TimeA of t in loc (nil means UTC): the wall clock
// fields, the day of week and SU set when loc observes daylight saving
// time at t. A time outside 2000..2099 is flagged invalid.
func TimeAOf(t time.Time, loc *time.Location) TimeA {
	t = t.In(zone(loc))
	wd := uint8(t.Weekday())
	if wd == 0 {
		wd = 7
	}
	return TimeA{
		Minute: uint8(t.Minute()), Hour: uint8(t.Hour()), Day: uint8(t.Day()), Weekday: wd,
		Month: uint8(t.Month()), Year: uint8(((t.Year() % 100) + 100) % 100),
		Invalid: t.Year() < 2000 || t.Year() > 2099, SummerTime: t.IsDST(),
	}
}

func zone(loc *time.Location) *time.Location {
	if loc == nil {
		return time.UTC
	}
	return loc
}

// Time returns the time in loc (nil means UTC). ok is false when the
// fields do not form a valid date and time; IV is reported separately in
// Invalid. When the wall clock is ambiguous in loc (the hour repeated at
// the end of summer time), SU selects the summer or the standard time.
func (t TimeA) Time(loc *time.Location) (time.Time, bool) {
	return civil(int(t.Year), int(t.Month), int(t.Day), int(t.Hour), int(t.Minute), 0, 0, t.SummerTime, zone(loc))
}

func civil(year, month, day, hour, minute, sec, ms int, su bool, loc *time.Location) (time.Time, bool) {
	tt := time.Date(2000+year, time.Month(month), day, hour, minute, sec, ms*int(time.Millisecond), loc)
	ok := year <= 99 && month >= 1 && month <= 12 && day >= 1 && hour <= 23 && minute <= 59 && sec <= 59 && ms <= 999 &&
		tt.Day() == day && int(tt.Month()) == month && tt.Hour() == hour
	if ok && tt.IsDST() != su {
		for _, d := range []time.Duration{time.Hour, -time.Hour} {
			alt := tt.Add(d)
			if alt.IsDST() == su && alt.Hour() == hour && alt.Minute() == minute {
				return alt, true
			}
		}
	}
	return tt, ok
}

// String formats the fields, for example "2016-05-01 01:00 SU".
func (t TimeA) String() string {
	s := fmt.Sprintf("20%02d-%02d-%02d %02d:%02d", t.Year, t.Month, t.Day, t.Hour, t.Minute)
	return s + t.flags()
}

func (t TimeA) flags() string {
	s := ""
	if t.SummerTime {
		s += " SU"
	}
	if t.Invalid {
		s += " IV"
	}
	if t.TIS {
		s += " TIS"
	}
	return s
}

// AppendTo appends the five octets of t.
func (t TimeA) AppendTo(b []byte) []byte {
	m := t.Minute & 0x3F
	if t.TIS {
		m |= 0x40
	}
	if t.Invalid {
		m |= 0x80
	}
	h := t.Hour&0x1F | (t.Res1&0x03)<<5
	if t.SummerTime {
		h |= 0x80
	}
	y := t.Year & 0x7F
	if t.Res2 {
		y |= 0x80
	}
	return append(b, m, h, t.Day&0x1F|(t.Weekday&0x07)<<5, t.Month&0x0F|(t.ETI&0x03)<<4|(t.PTI&0x03)<<6, y)
}

// ParseTimeA decodes the five octets of a TimeA.
func ParseTimeA(b []byte) (TimeA, error) {
	if len(b) < SizeTimeA {
		return TimeA{}, fmt.Errorf("%w: time a needs %d octets, have %d", ErrShort, SizeTimeA, len(b))
	}
	return parseTimeA(b), nil
}

func parseTimeA(b []byte) TimeA {
	return TimeA{
		Minute: b[0] & 0x3F, TIS: b[0]&0x40 != 0, Invalid: b[0]&0x80 != 0,
		Hour: b[1] & 0x1F, Res1: b[1] >> 5 & 0x03, SummerTime: b[1]&0x80 != 0,
		Day: b[2] & 0x1F, Weekday: b[2] >> 5,
		Month: b[3] & 0x0F, ETI: b[3] >> 4 & 0x03, PTI: b[3] >> 6,
		Year: b[4] & 0x7F, Res2: b[4]&0x80 != 0,
	}
}

// TimeB is the seven-octet binary time "time b" of IEC 60870-5-102,
// CP56Time2b: milliseconds and seconds followed by the five octets of a
// [TimeA].
//
// Source: Beckhoff TS650x manual, section 9.1.13 (T_CP56Time2b): the first
// two octets are a little-endian word with the milliseconds 0..999 in bits
// 0-9 and the seconds 0..59 above them (the manual prints "bits 11..15",
// which cannot hold 59; the seconds take bits 10-15). gisce/iec870ree
// (TimeB) and its test vectors agree. daiiniel/IEC60870-5-102 instead
// writes seconds*1000+milliseconds as in CP56Time2a of IEC 60870-5-4; that
// disagreement is recorded in docs/iec102/INTEROPERABILITY.md.
type TimeB struct {
	Millisecond uint16 // 0..999 (10 bits)
	Second      uint8  // 0..59 (6 bits)
	TimeA
}

// TimeBOf returns the TimeB of t in loc (nil means UTC).
func TimeBOf(t time.Time, loc *time.Location) TimeB {
	t = t.In(zone(loc))
	return TimeB{Millisecond: uint16(t.Nanosecond() / int(time.Millisecond)), Second: uint8(t.Second()), TimeA: TimeAOf(t, loc)}
}

// Time returns the time in loc (nil means UTC); ok as for [TimeA.Time].
func (t TimeB) Time(loc *time.Location) (time.Time, bool) {
	return civil(int(t.Year), int(t.Month), int(t.Day), int(t.Hour), int(t.Minute), int(t.Second), int(t.Millisecond), t.SummerTime, zone(loc))
}

// String formats the fields, for example "2018-08-18 04:42:25.000".
func (t TimeB) String() string {
	return fmt.Sprintf("20%02d-%02d-%02d %02d:%02d:%02d.%03d", t.Year, t.Month, t.Day, t.Hour, t.Minute, t.Second, t.Millisecond) + t.flags()
}

// AppendTo appends the seven octets of t.
func (t TimeB) AppendTo(b []byte) []byte {
	w := t.Millisecond&0x3FF | uint16(t.Second&0x3F)<<10
	return t.TimeA.AppendTo(append(b, byte(w), byte(w>>8)))
}

// ParseTimeB decodes the seven octets of a TimeB.
func ParseTimeB(b []byte) (TimeB, error) {
	if len(b) < SizeTimeB {
		return TimeB{}, fmt.Errorf("%w: time b needs %d octets, have %d", ErrShort, SizeTimeB, len(b))
	}
	return parseTimeB(b), nil
}

func parseTimeB(b []byte) TimeB {
	w := uint16(b[0]) | uint16(b[1])<<8
	return TimeB{Millisecond: w & 0x3FF, Second: uint8(w >> 10), TimeA: parseTimeA(b[2:])}
}
