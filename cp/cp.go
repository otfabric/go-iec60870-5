// SPDX-License-Identifier: MIT

// Package cp encodes and decodes the binary time tags of IEC 60870-5-4
// (subclause 6.8): CP16Time2a (elapsed milliseconds), CP24Time2a (minute,
// second, millisecond), CP32Time2a (adds the hour) and CP56Time2a (a full
// date and time).
//
// They are shared by the companion standards that use them: IEC 60870-5-101
// and -104 use CP16, CP24 and CP56; IEC 60870-5-103 uses CP32 for event time
// tags and CP56 for clock synchronization. The ASDU codecs of those
// standards build on this package; applications normally use their
// re-exported types (for example asdu.Timestamp).
package cp

import (
	"errors"
	"fmt"
	"time"
)

// Encoded sizes in octets.
const (
	Size16 = 2
	Size24 = 3
	Size32 = 4
	Size56 = 7
)

// ErrShort reports a buffer shorter than the time tag it should hold.
var ErrShort = errors.New("cp: time tag truncated")

// Timestamp is a time tag. Which fields are meaningful depends on the format
// it travels in:
//
//   - CP24Time2a decodes onto 0001-01-01 00:mm:ss.mmm: only the minute,
//     second and nanosecond of Time are meaningful, and the receiver has to
//     supply the hour and date. On encode only those fields are used.
//   - CP32Time2a adds the hour (and SU) and decodes onto 0001-01-01
//     hh:mm:ss.mmm; the receiver supplies the date.
//   - CP56Time2a carries a full date and time. Its year is two digits:
//     70..99 decode as 1970..1999 and 0..69 as 2000..2069.
//
// The wire formats carry no time zone; times are converted to and from the
// location given to the codec functions.
//
// A zero Time encoded as CP56Time2a is written as 2000-01-01 with the
// invalid flag set. A received CP56Time2a with a field out of range, or a
// date that does not exist, decodes to the zero Time with Invalid set.
type Timestamp struct {
	time.Time

	// Invalid is the IV flag: the time is not reliable.
	Invalid bool
	// Substituted is the GEN flag (bit 7 of the minute octet's RES1
	// position in IEC 60870-5-4, used as "substituted time" by
	// IEC 60870-5-101/-104 implementations): the time was not taken at the
	// source of the information but added by intermediate equipment.
	Substituted bool
	// SummerTime is the SU flag (CP32Time2a and CP56Time2a): the time is
	// daylight saving time.
	SummerTime bool
	// NoWeekday reports a CP56Time2a whose day-of-week field is 0. The
	// field ranges 1..7 (IEC 60870-5-4, CP56Time2a); stations that do not
	// use it send 0. When it is set the encoder writes 0 again instead of
	// the day of the week of Time, so a time tag that is passed on or
	// mirrored keeps the octets it came with.
	NoWeekday bool
}

// At returns a valid time tag for t.
func At(t time.Time) Timestamp { return Timestamp{Time: t} }

// Now returns a valid time tag for the current time.
func Now() Timestamp { return Timestamp{Time: time.Now()} }

func zone(loc *time.Location) *time.Location {
	if loc == nil {
		return time.UTC
	}
	return loc
}

func flags(t Timestamp) byte {
	var f byte
	if t.Invalid {
		f |= 0x80
	}
	if t.Substituted {
		f |= 0x40
	}
	return f
}

// AppendCP16 appends d as CP16Time2a: elapsed milliseconds 0..59999. A
// duration outside that range is clamped.
func AppendCP16(b []byte, d time.Duration) []byte {
	ms := d.Milliseconds()
	ms = min(max(ms, 0), 59999)
	return append(b, byte(ms&0xFF), byte(ms>>8&0xFF))
}

// ParseCP16 decodes a CP16Time2a. Values above 59999 are returned as they
// are; ok is false for them.
func ParseCP16(b []byte) (d time.Duration, ok bool, err error) {
	if len(b) < Size16 {
		return 0, false, fmt.Errorf("%w: CP16Time2a needs %d octets, have %d", ErrShort, Size16, len(b))
	}
	ms := int(b[0]) | int(b[1])<<8
	return time.Duration(ms) * time.Millisecond, ms <= 59999, nil
}

// AppendCP24 appends t as CP24Time2a in loc (nil means UTC).
func AppendCP24(b []byte, t Timestamp, loc *time.Location) []byte {
	tt := t.In(zone(loc))
	ms := tt.Second()*1000 + tt.Nanosecond()/int(time.Millisecond)
	return append(b, byte(ms&0xFF), byte(ms>>8&0xFF), byte(tt.Minute()&0x3F)|flags(t))
}

// ParseCP24 decodes a CP24Time2a in loc (nil means UTC). Out-of-range
// fields are normalized by time.Date and flagged invalid.
func ParseCP24(b []byte, loc *time.Location) (Timestamp, error) {
	if len(b) < Size24 {
		return Timestamp{}, fmt.Errorf("%w: CP24Time2a needs %d octets, have %d", ErrShort, Size24, len(b))
	}
	return parse24(b, zone(loc)), nil
}

func parse24(b []byte, loc *time.Location) Timestamp {
	ms := int(b[0]) | int(b[1])<<8
	minute := int(b[2] & 0x3F)
	return Timestamp{
		Time:        time.Date(1, 1, 1, 0, minute, ms/1000, ms%1000*int(time.Millisecond), loc),
		Invalid:     b[2]&0x80 != 0 || ms > 59999 || minute > 59,
		Substituted: b[2]&0x40 != 0,
	}
}

// AppendCP32 appends t as CP32Time2a in loc (nil means UTC): CP24Time2a
// followed by the hour octet with the SU flag.
func AppendCP32(b []byte, t Timestamp, loc *time.Location) []byte {
	b = AppendCP24(b, t, loc)
	return append(b, hourOctet(t, loc))
}

func hourOctet(t Timestamp, loc *time.Location) byte {
	h := byte(t.In(zone(loc)).Hour() & 0x1F)
	if t.SummerTime {
		h |= 0x80
	}
	return h
}

// ParseCP32 decodes a CP32Time2a in loc (nil means UTC). An hour above 23
// is flagged invalid.
func ParseCP32(b []byte, loc *time.Location) (Timestamp, error) {
	if len(b) < Size32 {
		return Timestamp{}, fmt.Errorf("%w: CP32Time2a needs %d octets, have %d", ErrShort, Size32, len(b))
	}
	loc = zone(loc)
	t := parse24(b, loc)
	hour := int(b[3] & 0x1F)
	t.Time = t.Add(time.Duration(hour) * time.Hour)
	t.SummerTime = b[3]&0x80 != 0
	if hour > 23 {
		t.Invalid = true
	}
	return t, nil
}

// AppendCP56 appends t as CP56Time2a in loc (nil means UTC). A time outside
// 1970..2069 cannot round-trip through the two-digit year and goes out
// flagged invalid.
func AppendCP56(b []byte, t Timestamp, loc *time.Location) []byte {
	loc = zone(loc)
	if t.IsZero() {
		t = Timestamp{Time: time.Date(2000, 1, 1, 0, 0, 0, 0, loc), Invalid: true}
	}
	tt := t.In(loc)
	year := tt.Year()
	if year < 1970 || year > 2069 {
		t.Invalid = true
	}
	b = AppendCP24(b, t, loc)
	dow := byte(tt.Weekday() & 0x07)
	switch {
	case t.NoWeekday:
		dow = 0
	case dow == 0:
		dow = 7
	}
	return append(b, hourOctet(t, loc), byte(tt.Day()&0x1F)|dow<<5, byte(tt.Month()&0x0F), byte((year%100+100)%100&0x7F))
}

// ParseCP56 decodes a CP56Time2a in loc (nil means UTC).
func ParseCP56(b []byte, loc *time.Location) (Timestamp, error) {
	if len(b) < Size56 {
		return Timestamp{}, fmt.Errorf("%w: CP56Time2a needs %d octets, have %d", ErrShort, Size56, len(b))
	}
	loc = zone(loc)
	ms := int(b[0]) | int(b[1])<<8
	minute, hour := int(b[2]&0x3F), int(b[3]&0x1F)
	day, month, year := int(b[4]&0x1F), time.Month(b[5]&0x0F), int(b[6]&0x7F)
	validYear := year <= 99
	if year >= 70 {
		year += 1900
	} else {
		year += 2000
	}
	t := Timestamp{
		Time:        time.Date(year, month, day, hour, minute, ms/1000, ms%1000*int(time.Millisecond), loc),
		Invalid:     b[2]&0x80 != 0,
		Substituted: b[2]&0x40 != 0,
		SummerTime:  b[3]&0x80 != 0,
		NoWeekday:   b[4]&0xE0 == 0,
	}
	if !validYear || ms > 59999 || minute > 59 || hour > 23 || t.Day() != day || t.Month() != month {
		t.Time = time.Time{}
		t.Invalid = true
	}
	return t, nil
}
