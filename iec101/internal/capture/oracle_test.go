// SPDX-License-Identifier: MIT

package capture

import (
	"fmt"
	"math"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/otfabric/go-iec60870-5/asdu"
	"github.com/otfabric/go-iec60870-5/ft12"
	"github.com/otfabric/go-iec60870-5/internal/testutil/corpus"
)

// Wireshark as an independent decoder. make captures-oracle runs tshark
// over every capture of specs with the field sizes of the manifest and
// keeps, per frame, the fields below in <capture>.tshark.tsv.gz next to
// it; TestOracleTshark compares what go-iec60870-5 decodes from the same
// frames with them, field by field, without needing tshark. Wireshark
// shows nothing for the single character E5h.

var oracleFields = []string{
	"iec60870_101.linkaddr", "iec60870_101.ctrl_prm", "iec60870_101.ctrl_fcb", "iec60870_101.ctrl_fcv",
	"iec60870_101.ctrl_dfc", "iec60870_101.ctrl_func_pri_to_sec", "iec60870_101.ctrl_func_sec_to_pri",
	"iec60870_asdu.typeid", "iec60870_asdu.numix", "iec60870_asdu.sq", "iec60870_asdu.causetx",
	"iec60870_asdu.nega", "iec60870_asdu.test", "iec60870_asdu.oa", "iec60870_asdu.addr", "iec60870_asdu.ioa",
	"iec60870_asdu.siq.spi", "iec60870_asdu.diq.dpi", "iec60870_asdu.normval", "iec60870_asdu.scalval",
	"iec60870_asdu.float", "iec60870_asdu.bcr.count", "iec60870_asdu.qoi",
	"iec60870_asdu.cp56time.ms", "iec60870_asdu.cp56time.min", "iec60870_asdu.cp56time.hour",
	"iec60870_asdu.cp56time.day", "iec60870_asdu.cp56time.month", "iec60870_asdu.cp56time.year",
	"iec60870_asdu.cp56time.iv",
}

// oracleFile is next to the capture; the shared synthetic capture's oracle
// is in this corpus.
func oracleFile(s spec) string {
	if strings.HasPrefix(s.Path, "../") {
		return filepath.Join(dir, "wireshark", "synthetic_synthetic_iec101-coverage.tshark.tsv.gz")
	}
	return corpus.OracleFile(filepath.Join(dir, filepath.FromSlash(s.Path)))
}

func tsharkArgs(s spec) []string {
	args := append([]string(nil), s.tshark...)
	for _, o := range []struct {
		pref string
		v    int
	}{{"linkaddr_len", s.p.LinkAddr}, {"cot_len", s.p.Cause}, {"asdu_addr_len", s.p.CommonAddr}, {"asdu_ioa_len", s.p.IOA}} {
		args = append(args, "-o", fmt.Sprintf("iec60870_101.%s:%d", o.pref, o.v))
	}
	return append(args, "-Y", "iec60870_101")
}

// TestGenerateOracle writes the oracle files; it needs tshark and runs
// only with CAPTURE_ORACLE=1 (make captures-oracle).
func TestGenerateOracle(t *testing.T) {
	for _, s := range specs {
		corpus.GenerateOracle(t, filepath.Join(dir, filepath.FromSlash(s.Path)), oracleFile(s), tsharkArgs(s), oracleFields)
	}
}

// ourRow is what go-iec60870-5 decodes from the frames a capture frame
// completes, in the form of the oracle columns.
func ourRow(ds []Decoded, p Params) corpus.Row {
	row := corpus.Row{}
	add := func(f, v string) { row[f] = append(row[f], v) }
	bit := func(v bool) string {
		if v {
			return "1"
		}
		return "0"
	}
	b := func(v bool) string {
		if v {
			return "True"
		}
		return "False"
	}
	for _, d := range ds {
		if d.Kind == ft12.KindSingle {
			continue
		}
		c := d.Control()
		if p.LinkAddr > 0 {
			add("iec60870_101.linkaddr", strconv.Itoa(int(d.Addr)))
		}
		add("iec60870_101.ctrl_prm", bit(c.PRM()))
		if c.PRM() {
			add("iec60870_101.ctrl_fcb", bit(c.FCB()))
			add("iec60870_101.ctrl_fcv", bit(c.FCV()))
			add("iec60870_101.ctrl_func_pri_to_sec", strconv.Itoa(int(c.Func())))
		} else {
			add("iec60870_101.ctrl_dfc", bit(c.DFC()))
			add("iec60870_101.ctrl_func_sec_to_pri", strconv.Itoa(int(c.Func())))
		}
		x := d.ASDU
		if x == nil {
			continue
		}
		add("iec60870_asdu.typeid", strconv.Itoa(int(x.Type)))
		add("iec60870_asdu.numix", strconv.Itoa(x.Len()))
		add("iec60870_asdu.sq", b(x.Sequence))
		add("iec60870_asdu.causetx", strconv.Itoa(int(x.Cause)))
		add("iec60870_asdu.nega", b(x.Negative))
		add("iec60870_asdu.test", b(x.Test))
		if p.Cause == 2 {
			add("iec60870_asdu.oa", strconv.Itoa(int(x.Originator)))
		}
		add("iec60870_asdu.addr", strconv.Itoa(int(x.CommonAddr)))
		for _, o := range x.Objects {
			add("iec60870_asdu.ioa", strconv.Itoa(int(o.Address())))
			objectFields(x.Type, o, add, b)
		}
	}
	return row
}

func float(v float64) string { return strconv.FormatFloat(v, 'g', -1, 64) }

// objectFields adds the oracle columns of one information object.
func objectFields(typ asdu.TypeID, o asdu.InformationObject, add func(f, v string), b func(bool) string) {
	var ts *asdu.Timestamp
	switch o := o.(type) {
	case asdu.SinglePoint:
		add("iec60870_asdu.siq.spi", b(o.Value))
		ts = &o.Time
	case asdu.DoublePoint:
		add("iec60870_asdu.diq.dpi", strconv.Itoa(int(o.Value)))
		ts = &o.Time
	case asdu.StepPosition:
		ts = &o.Time
	case asdu.MeasuredNormalized:
		add("iec60870_asdu.normval", float(o.Value.Float64()))
		ts = &o.Time
	case asdu.MeasuredScaled:
		add("iec60870_asdu.scalval", strconv.Itoa(int(o.Value)))
		ts = &o.Time
	case asdu.MeasuredFloat:
		add("iec60870_asdu.float", strconv.FormatFloat(float64(o.Value), 'g', -1, 32))
		ts = &o.Time
	case asdu.IntegratedTotal:
		add("iec60870_asdu.bcr.count", strconv.Itoa(int(o.Value)))
		ts = &o.Time
	case asdu.SingleCommand:
		ts = &o.Time
	case asdu.DoubleCommand:
		ts = &o.Time
	case asdu.Interrogation:
		add("iec60870_asdu.qoi", strconv.Itoa(int(o.Qualifier)))
	case asdu.ClockSync:
		ts = &o.Time
	}
	// Wireshark shows the fields of CP56Time2a only; the types below 30
	// carry CP24Time2a.
	if ts == nil || typ != asdu.C_CS_NA_1 && (!typ.HasTimeTag() || typ < asdu.M_SP_TB_1) {
		return
	}
	raw, err := asdu.New(asdu.CauseSpontaneous, 1, asdu.ClockSync{Time: *ts}).Encode(asdu.IEC104)
	if err != nil {
		return
	}
	t := raw[len(raw)-7:]
	add("iec60870_asdu.cp56time.ms", strconv.Itoa(int(t[0])|int(t[1])<<8))
	add("iec60870_asdu.cp56time.min", strconv.Itoa(int(t[2]&0x3f)))
	add("iec60870_asdu.cp56time.hour", strconv.Itoa(int(t[3]&0x1f)))
	add("iec60870_asdu.cp56time.day", strconv.Itoa(int(t[4]&0x1f)))
	add("iec60870_asdu.cp56time.month", strconv.Itoa(int(t[5]&0x0f)))
	add("iec60870_asdu.cp56time.year", strconv.Itoa(int(t[6]&0x7f)))
	add("iec60870_asdu.cp56time.iv", b(t[2]&0x80 != 0))
}

func sameValue(field, want, got string) bool {
	if want == got {
		return true
	}
	if field == "iec60870_asdu.normval" || field == "iec60870_asdu.float" {
		w, err1 := strconv.ParseFloat(want, 64)
		g, err2 := strconv.ParseFloat(got, 64)
		return err1 == nil && err2 == nil && math.Abs(w-g) <= 1e-6*math.Max(1, math.Abs(w))
	}
	return false
}

// Classes of differences between Wireshark and go-iec60870-5.
const (
	classTestIOA = "Wireshark 4.6.9 reads the IOA of C_TS_NA_1 as three octets whatever asdu_ioa_len says (the first octet of the fixed test pattern AA55h becomes its high octet); go-iec60870-5 uses the IOA size of the manifest"
	classSplit   = "a frame whose four header octets are split over two TCP segments: Wireshark 4.6.9 does not reassemble it (the first segment is malformed, the second an empty row); go-iec60870-5 reassembles the stream and completes the frame in a later segment"
	classOther   = "unclassified"
)

// oracleExpected pins, per capture, the frames and values compared and
// the differences by class. Every difference is classified; a new one
// fails the test.
var oracleExpected = map[string]struct {
	frames, values int
	diffs          map[string]int
}{
	"wireshark-issues/unknown_sel-rtac_class1-class2-polls.pcap":                              {44, 3598, nil},
	"wireshark-issues/unknown_sel-rtac_double-command-sbo.pcap":                               {176, 1269, nil},
	"wireshark-issues/unknown_unknown_energisa-polls-m-sp-ta-1.pcapng":                        {1549, 15047, map[string]int{classTestIOA: 7, classSplit: 6}},
	"wireshark-issues/unknown_unknown_energisa-polls-m-sp-tb-1.pcapng":                        {23431, 229861, map[string]int{classTestIOA: 88, classSplit: 160}},
	"wireshark-issues/unknown_unknown_m-me-nb-1-link-address-2.pcapng":                        {1, 27, nil},
	"streampacketizer/synthetic_synthetic_class-polls.pcap":                                   {14, 63, nil},
	"streampacketizer/synthetic_synthetic_status-polls.pcap":                                  {65, 293, nil},
	"streampacketizer/synthetic_synthetic_interrogation-link-address-2.pcap":                  {216, 982, nil},
	"../../../iec104/testdata/captures/wireshark/synthetic_synthetic_iec104-coverage.pcap.gz": {20, 211, nil},
}

// diff is one difference in one frame.
type diff struct {
	frame        int
	field        string
	tshark, ours string
}

func classify(d diff, ds []Decoded) string {
	switch {
	case d.field == "iec60870_asdu.ioa" && len(ds) > 0 && ds[0].ASDU != nil && ds[0].ASDU.Type == asdu.C_TS_NA_1:
		return classTestIOA
	case d.field == "frame":
		return classSplit
	}
	return classOther
}

func TestOracleTshark(t *testing.T) {
	for _, s := range specs {
		t.Run(s.Path, func(t *testing.T) {
			version, rows := corpus.ReadOracle(t, oracleFile(s), oracleFields)
			byFrame := map[int][]Decoded{}
			for _, r := range read(t, s) {
				for _, d := range r.Decoded {
					byFrame[d.Last] = append(byFrame[d.Last], d)
				}
			}
			frames, values := 0, 0
			var diffs []diff
			for f, ds := range byFrame {
				want, got := rows[f], ourRow(ds, s.p)
				if len(want) == 0 && len(got) == 0 {
					continue
				}
				if len(want) == 0 {
					diffs = append(diffs, diff{f, "frame", "not dissected", fmt.Sprint(len(ds), " frames")})
					continue
				}
				frames++
				for _, field := range oracleFields {
					w, g := want[field], got[field]
					if len(w) != len(g) {
						diffs = append(diffs, diff{f, field, strings.Join(w, ","), strings.Join(g, ",")})
						continue
					}
					for i := range w {
						values++
						if !sameValue(field, w[i], g[i]) {
							diffs = append(diffs, diff{f, field, w[i], g[i]})
						}
					}
				}
			}
			for f, row := range rows {
				if byFrame[f] == nil && len(row) > 0 {
					diffs = append(diffs, diff{f, "fields", "dissected", "no frame completes"})
				}
			}
			got := map[string]int{}
			for _, d := range diffs {
				c := classify(d, byFrame[d.frame])
				got[c]++
				if c == classOther {
					t.Errorf("frame %d %s: tshark %q, go-iec60870-5 %q", d.frame, d.field, d.tshark, d.ours)
				}
			}
			want := oracleExpected[s.Path]
			if frames != want.frames || values != want.values {
				t.Errorf("%d frames, %d values compared; want %d and %d", frames, values, want.frames, want.values)
			}
			for c, n := range got {
				if c != classOther && want.diffs[c] != n {
					t.Errorf("%d differences, want %d: %s", n, want.diffs[c], c)
				}
			}
			for c, n := range want.diffs {
				if got[c] == 0 {
					t.Errorf("0 differences, want %d: %s", n, c)
				}
			}
			t.Logf("%s: %d frames, %d values, %d classified differences", version, frames, values, len(diffs))
		})
	}
}
