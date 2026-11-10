// SPDX-License-Identifier: MIT

package cp

import (
	"bytes"
	"errors"
	"testing"
	"time"
)

func TestCP56RoundTrip(t *testing.T) {
	cet := time.FixedZone("CET", 3600)
	for _, tc := range []struct {
		ts  Timestamp
		loc *time.Location
	}{
		{At(time.Date(2026, 11, 10, 23, 59, 59, 999e6, time.UTC)), nil},
		{Timestamp{Time: time.Date(1970, 1, 1, 0, 0, 0, 0, cet), SummerTime: true, Substituted: true}, cet},
		{Timestamp{Time: time.Date(2069, 12, 31, 12, 0, 0, 0, time.UTC), NoWeekday: true}, time.UTC},
	} {
		b := AppendCP56(nil, tc.ts, tc.loc)
		if len(b) != Size56 {
			t.Fatalf("len %d", len(b))
		}
		got, err := ParseCP56(b, tc.loc)
		if err != nil || !got.Equal(tc.ts.Time) || got.SummerTime != tc.ts.SummerTime ||
			got.Substituted != tc.ts.Substituted || got.NoWeekday != tc.ts.NoWeekday || got.Invalid {
			t.Errorf("%v: got %+v, %v", tc.ts, got, err)
		}
		if again := AppendCP56(nil, got, tc.loc); !bytes.Equal(again, b) {
			t.Errorf("re-encode % x, want % x", again, b)
		}
	}
}

func TestCP56Edges(t *testing.T) {
	// Zero time: 2000-01-01 flagged invalid.
	b := AppendCP56(nil, Timestamp{}, nil)
	if got, _ := ParseCP56(b, nil); !got.Invalid || got.Year() != 2000 {
		t.Errorf("zero: %+v", got)
	}
	// Out of the two-digit window: flagged invalid.
	if b := AppendCP56(nil, At(time.Date(2070, 1, 1, 0, 0, 0, 0, time.UTC)), nil); b[2]&0x80 == 0 {
		t.Error("2070 not flagged invalid")
	}
	// Sunday encodes as 7.
	if b := AppendCP56(nil, At(time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC)), nil); b[4]>>5 != 7 {
		t.Errorf("sunday dow %d", b[4]>>5)
	}
	// Impossible date and out-of-range fields decode to zero, invalid.
	for _, bad := range [][]byte{
		{0, 0, 0, 0, 31, 2, 26},      // 31 February
		{0x60, 0xEA, 0, 0, 1, 1, 26}, // 60000 ms
		{0, 0, 60, 0, 1, 1, 26},      // minute 60
		{0, 0, 0, 24, 1, 1, 26},      // hour 24
		{0, 0, 0, 0, 1, 1, 100},      // year 100
	} {
		if got, err := ParseCP56(bad, nil); err != nil || !got.IsZero() || !got.Invalid {
			t.Errorf("% x: %+v %v", bad, got, err)
		}
	}
}

func TestCP24CP32(t *testing.T) {
	ts := Timestamp{Time: time.Date(1, 1, 1, 13, 42, 7, 250e6, time.UTC), SummerTime: true, Invalid: true}
	b24 := AppendCP24(nil, ts, nil)
	got24, err := ParseCP24(b24, nil)
	if err != nil || got24.Minute() != 42 || got24.Second() != 7 || got24.Nanosecond() != 250e6 || !got24.Invalid || got24.Hour() != 0 {
		t.Errorf("CP24 %+v %v", got24, err)
	}
	b32 := AppendCP32(nil, ts, nil)
	if len(b32) != Size32 || !bytes.Equal(b32[:3], b24) {
		t.Fatalf("CP32 % x", b32)
	}
	got32, err := ParseCP32(b32, nil)
	if err != nil || !got32.Equal(ts.Time) || !got32.SummerTime || !got32.Invalid {
		t.Errorf("CP32 %+v %v", got32, err)
	}
	if got, _ := ParseCP32([]byte{0, 0, 0, 24}, nil); !got.Invalid {
		t.Error("hour 24 not invalid")
	}
	if got, _ := ParseCP24([]byte{0x60, 0xEA, 0}, nil); !got.Invalid {
		t.Error("60000 ms not invalid")
	}
}

func TestCP16(t *testing.T) {
	for d, want := range map[time.Duration][]byte{
		1500 * time.Millisecond: {0xDC, 0x05},
		-time.Second:            {0, 0},
		time.Minute:             {0x5F, 0xEA},
	} {
		if b := AppendCP16(nil, d); !bytes.Equal(b, want) {
			t.Errorf("%v: % x, want % x", d, b, want)
		}
	}
	if d, ok, err := ParseCP16([]byte{0xDC, 0x05}); d != 1500*time.Millisecond || !ok || err != nil {
		t.Errorf("parse: %v %v %v", d, ok, err)
	}
	if _, ok, _ := ParseCP16([]byte{0x60, 0xEA}); ok {
		t.Error("60000 ok")
	}
}

func TestShort(t *testing.T) {
	if _, _, err := ParseCP16([]byte{1}); !errors.Is(err, ErrShort) {
		t.Error(err)
	}
	for _, f := range []func([]byte, *time.Location) (Timestamp, error){ParseCP24, ParseCP32, ParseCP56} {
		if _, err := f([]byte{1, 2}, nil); !errors.Is(err, ErrShort) {
			t.Error(err)
		}
	}
	if !Now().After(At(time.Time{}).Time) {
		t.Error("Now")
	}
}

func FuzzParse(f *testing.F) {
	f.Add([]byte{0, 0, 0, 0, 1, 1, 26})
	f.Add([]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff})
	f.Fuzz(func(t *testing.T, b []byte) {
		_, _, _ = ParseCP16(b)
		_, _ = ParseCP24(b, nil)
		_, _ = ParseCP32(b, nil)
		if ts, err := ParseCP56(b, nil); err == nil && !ts.Invalid {
			back, _ := ParseCP56(AppendCP56(nil, ts, nil), nil)
			if !back.Equal(ts.Time) {
				t.Fatalf("round trip %v -> %v", ts, back)
			}
		}
	})
}
