// SPDX-License-Identifier: MIT

package iec102_test

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/otfabric/go-iec60870-5/ft12"
	"github.com/otfabric/go-iec60870-5/iec102"
	"github.com/otfabric/go-iec60870-5/iec102/ree"
)

// frameLine matches the frame lines of the innoWatt decoder log:
// "  26 bytes: 68 14 14 68 8 1 0 b ...".
var frameLine = regexp.MustCompile(`^\s+(\d+) bytes: ((?:[0-9a-f]{1,2} )+)\s*$`)

// logFrames returns the frames of a decoder log: the first "N bytes:" line
// of each <missatge>.
func logFrames(t *testing.T, path string) [][]byte {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	var out [][]byte
	inMsg, taken := false, false
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.Contains(line, "<missatge>"):
			inMsg, taken = true, false
		case strings.Contains(line, "</missatge>"):
			inMsg = false
		}
		m := frameLine.FindStringSubmatch(line)
		if m == nil || !inMsg || taken {
			continue
		}
		taken = true
		var b []byte
		for _, x := range strings.Fields(m[2]) {
			if len(x) == 1 {
				x = "0" + x
			}
			v, err := hex.DecodeString(x)
			if err != nil {
				t.Fatal(err)
			}
			b = append(b, v...)
		}
		if n := len(b); m[1] != itoa(n) {
			t.Fatalf("frame %x: log says %s octets, have %d", b, m[1], n)
		}
		out = append(out, b)
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func itoa(n int) string {
	var b []byte
	for {
		b = append([]byte{byte('0' + n%10)}, b...)
		if n /= 10; n == 0 {
			return string(b)
		}
	}
}

var fp2 = ft12.Params{AddrSize: 2}

// TestCaptureInnowatt decodes every frame of the Actaris SL762B log and
// encodes it back to the same octets, with and without the REE profile.
func TestCaptureInnowatt(t *testing.T) {
	frames := logFrames(t, "testdata/innowatt/icra_201605_raw.txt")
	if len(frames) != 749 {
		t.Fatalf("%d frames, want 749", len(frames))
	}
	count := map[iec102.TypeID]int{}
	fixed := 0
	var last iec102.TimeA
	for _, profile := range []iec102.Profile{nil, ree.Profile} {
		p := iec102.Params{Profile: profile}
		for i, raw := range frames {
			f, n, err := ft12.Decode(raw, fp2)
			if err != nil || n != len(raw) {
				t.Fatalf("frame %d % x: %v (%d of %d octets)", i, raw, err, n, len(raw))
			}
			again, err := f.Encode(fp2)
			if err != nil || !bytes.Equal(again, raw) {
				t.Fatalf("frame %d: re-encoded % x, want % x (%v)", i, again, raw, err)
			}
			if f.Addr != 1 {
				t.Fatalf("frame %d: link address %d", i, f.Addr)
			}
			if f.Kind != ft12.KindVariable {
				fixed++
				continue
			}
			a, err := iec102.Decode(f.Data, p)
			if err != nil {
				t.Fatalf("frame %d % x: %v", i, f.Data, err)
			}
			enc, err := a.Encode(p)
			if err != nil || !bytes.Equal(enc, f.Data) {
				t.Fatalf("frame %d: ASDU re-encoded % x, want % x (%v)", i, enc, f.Data, err)
			}
			if a.CommonAddr != 1 {
				t.Fatalf("frame %d: common address %d", i, a.CommonAddr)
			}
			if profile != nil {
				continue
			}
			count[a.Type]++
			if b, ok := a.Body.(iec102.IntegratedTotals); ok {
				if a.Cause != iec102.CauseRequest || a.Record != 11 || len(b.Totals) != 1 || b.Totals[0].Addr != 1 || b.Signed {
					t.Fatalf("frame %d: %v %+v", i, a, b)
				}
				tt, ok := b.Time.Time(nil)
				if !ok || b.Time.Invalid {
					t.Fatalf("frame %d: time %v", i, b.Time)
				}
				if prev, _ := last.Time(nil); last.Month != 0 && tt.Sub(prev) != time.Hour {
					t.Fatalf("frame %d: %v follows %v", i, b.Time, last)
				}
				last = b.Time
			}
		}
	}
	if fixed != 2*2 || count[iec102.M_IT_TK_2] != 744 || count[iec102.C_CI_NU_2] != 2 || count[ree.C_AC_NA_2] != 1 {
		t.Fatalf("fixed %d, types %v", fixed, count)
	}
	if last.String() != "2016-06-01 00:00 SU" {
		t.Fatalf("last period %v", last)
	}
}

// TestCaptureInnowattFields checks the fields of the first frames against
// the decoder log's own reading of them.
func TestCaptureInnowattFields(t *testing.T) {
	frames := logFrames(t, "testdata/innowatt/icra_201605_raw.txt")
	asdu := func(i int, p iec102.Params) *iec102.ASDU {
		f, _, err := ft12.Decode(frames[i], fp2)
		if err != nil {
			t.Fatal(err)
		}
		a, err := iec102.Decode(f.Data, p)
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	// Session start, confirmed: "Clau d'accés: 1".
	login := asdu(1, iec102.Params{Profile: ree.Profile})
	if k, ok := login.Body.(ree.AccessKey); !ok || k.Key != 1 || login.Cause != iec102.CauseActivationCon || login.VSQ != 1 {
		t.Fatalf("login %v %#v", login, login.Body)
	}
	if raw := asdu(1, iec102.Params{}); raw.Type != ree.C_AC_NA_2 {
		t.Fatalf("without profile: %v", raw)
	} else if _, ok := raw.Body.(iec102.Raw); !ok {
		t.Fatalf("without profile: %#v", raw.Body)
	}
	// "Direcció inici: 1 ... final: 1", "01/05/2016 00:00" .. "01/06/2016 00:00".
	read := asdu(3, iec102.Params{})
	q, ok := read.Body.(iec102.Read)
	if !ok || q.T != iec102.C_CI_NU_2 || q.First != 1 || q.Last != 1 || read.Record != 11 ||
		q.From.String() != "2016-05-01 00:00" || q.To.String() != "2016-06-01 00:00" {
		t.Fatalf("read %v %+v", read, q)
	}
	// "Energia (kWh): 79", "Data: 01/05/2016 01:00", SU, Sunday.
	first := asdu(4, iec102.Params{}).Body.(iec102.IntegratedTotals)
	if first.Totals[0].Value != 79 || first.Totals[0].Quality != 0 || first.Time.String() != "2016-05-01 01:00 SU" || first.Time.Weekday != 7 {
		t.Fatalf("first period %+v", first)
	}
	madrid, err := time.LoadLocation("Europe/Madrid")
	if err == nil {
		tt, ok := first.Time.Time(madrid)
		if !ok || !tt.Equal(time.Date(2016, 4, 30, 23, 0, 0, 0, time.UTC)) {
			t.Fatalf("first period at %v", tt)
		}
	}
	end := asdu(748, iec102.Params{})
	if end.Cause != iec102.CauseActivationTerm || !end.Mirrors(read) {
		t.Fatalf("termination %v", end)
	}
}

// TestCaptureProves decodes the controlling station's ASDU 162 (REE
// extension, not interpreted) and encodes it back.
func TestCaptureProves(t *testing.T) {
	frames := logFrames(t, "testdata/innowatt/proves.txt")
	if len(frames) != 1 {
		t.Fatalf("%d frames", len(frames))
	}
	f, _, err := ft12.Decode(frames[0], fp2)
	if err != nil || f.Control != 0x73 {
		t.Fatalf("%v %v", f, err)
	}
	for _, p := range []iec102.Params{{}, {Profile: ree.Profile}} {
		a, err := iec102.Decode(f.Data, p)
		if err != nil {
			t.Fatal(err)
		}
		raw, ok := a.Body.(iec102.Raw)
		if !ok || a.Type != ree.P_IN_VA_2 || a.Cause != iec102.CauseRequest || !bytes.Equal(raw.Data, []byte{0xC0}) {
			t.Fatalf("%v %#v", a, a.Body)
		}
		enc, _ := a.Encode(p)
		if !bytes.Equal(enc, f.Data) {
			t.Fatalf("% x, want % x", enc, f.Data)
		}
	}
}
