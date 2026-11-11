// SPDX-License-Identifier: MIT

package capture

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/otfabric/go-iec60870-5/cp"
	"github.com/otfabric/go-iec60870-5/ft12"
	"github.com/otfabric/go-iec60870-5/iec103"
	"github.com/otfabric/go-iec60870-5/internal/testutil/corpus"
)

// Wireshark's IEC 103 dissector as an independent decoder of the captures
// (make captures-oracle writes the *.tshark.tsv.gz files, the test needs
// no tshark). It dissects ASDUs 1, 5, 6, 7, 8, 9, 20 and 205 and the link
// layer; the hex vectors are not captures and have no oracle.

var oracleFields = []string{
	"iec60870_5_103.linkaddr", "iec60870_5_103.ctrl_prm", "iec60870_5_103.ctrl_fcb", "iec60870_5_103.ctrl_fcv",
	"iec60870_5_103.ctrl_dfc", "iec60870_5_103.ctrl_func_pri_to_sec", "iec60870_5_103.ctrl_func_sec_to_pri",
	"iec60870_5_103.asdu_typeid_mon", "iec60870_5_103.asdu_typeid_ctrl", "iec60870_5_103.sq",
	"iec60870_5_103.cot_mon", "iec60870_5_103.cot_ctrl", "iec60870_5_103.asdu_address",
	"iec60870_5_103.func_type", "iec60870_5_103.info_num", "iec60870_5_103.dpi", "iec60870_5_103.dco",
	"iec60870_5_103.rii", "iec60870_5_103.scn", "iec60870_5_103.col",
	"iec60870_5_103.cp32time2a_ms", "iec60870_5_103.cp32time2a_min", "iec60870_5_103.cp32time2a_hr", "iec60870_5_103.cp32time2a_iv",
}

func hasOracle(s spec) bool { return !strings.HasSuffix(s.Path, ".hex") }

func oracleFile(s spec) string {
	if strings.HasPrefix(s.Path, "../") {
		return filepath.Join(dir, "wireshark", "synthetic_synthetic_iec103-coverage.tshark.tsv.gz")
	}
	return corpus.OracleFile(filepath.Join(dir, filepath.FromSlash(s.Path)))
}

func TestGenerateOracle(t *testing.T) {
	for _, s := range specs {
		if hasOracle(s) {
			corpus.GenerateOracle(t, filepath.Join(dir, filepath.FromSlash(s.Path)), oracleFile(s), append(append([]string(nil), s.tshark...), "-Y", "iec60870_5_103"), oracleFields)
		}
	}
}

func hex8(v uint8) string { return fmt.Sprintf("0x%02x", v) }

func ourRow(ds []Decoded) corpus.Row {
	row := corpus.Row{}
	add := func(f, v string) { row[f] = append(row[f], v) }
	bit := func(v bool) string {
		if v {
			return "1"
		}
		return "0"
	}
	for _, d := range ds {
		if d.Kind == ft12.KindSingle {
			continue
		}
		c := d.Control()
		add("iec60870_5_103.linkaddr", strconv.Itoa(int(d.Addr)))
		add("iec60870_5_103.ctrl_prm", bit(c.PRM()))
		if c.PRM() {
			add("iec60870_5_103.ctrl_fcb", bit(c.FCB()))
			add("iec60870_5_103.ctrl_fcv", bit(c.FCV()))
			add("iec60870_5_103.ctrl_func_pri_to_sec", strconv.Itoa(int(c.Func())))
		} else {
			add("iec60870_5_103.ctrl_dfc", bit(c.DFC()))
			add("iec60870_5_103.ctrl_func_sec_to_pri", strconv.Itoa(int(c.Func())))
		}
		x := d.ASDU
		if x == nil {
			continue
		}
		dir := "mon"
		if c.PRM() {
			dir = "ctrl"
		}
		add("iec60870_5_103.asdu_typeid_"+dir, hex8(uint8(x.Type)))
		add("iec60870_5_103.sq", hex8(uint8(x.VSQ)))
		add("iec60870_5_103.cot_"+dir, hex8(uint8(x.Cause)))
		add("iec60870_5_103.asdu_address", strconv.Itoa(int(x.CommonAddr)))
		add("iec60870_5_103.func_type", strconv.Itoa(int(x.Fun)))
		add("iec60870_5_103.info_num", strconv.Itoa(int(x.Inf)))
		var ts *cp.Timestamp
		switch b := x.Body.(type) {
		case iec103.TimeTagged:
			add("iec60870_5_103.dpi", strconv.Itoa(int(b.DPI)))
			ts = &b.Time
		case iec103.GeneralCommand:
			add("iec60870_5_103.dco", strconv.Itoa(int(b.DCO)))
			add("iec60870_5_103.rii", strconv.Itoa(int(b.RII)))
		case iec103.GIInit:
			add("iec60870_5_103.scn", strconv.Itoa(int(b.SCN)))
		case iec103.GITermination:
			add("iec60870_5_103.scn", strconv.Itoa(int(b.SCN)))
		case iec103.Identification:
			add("iec60870_5_103.col", strconv.Itoa(int(b.COL)))
		}
		if ts != nil {
			t := cp.AppendCP32(nil, *ts, nil)
			add("iec60870_5_103.cp32time2a_ms", strconv.Itoa(int(t[0])|int(t[1])<<8))
			add("iec60870_5_103.cp32time2a_min", strconv.Itoa(int(t[2]&0x3f)))
			add("iec60870_5_103.cp32time2a_iv", bit(t[2]&0x80 != 0))
			add("iec60870_5_103.cp32time2a_hr", strconv.Itoa(int(t[3]&0x1f)))
		}
	}
	return row
}

// Classes of differences between Wireshark and go-iec60870-5.
const (
	classRefused = "an ASDU the codec refuses (listed in TestCorpus): Wireshark shows its header and objects, go-iec60870-5 the frame only"
	classOther   = "unclassified"
)

var oracleExpected = map[string]struct {
	frames, values int
	diffs          map[string]int
}{
	"wireshark-mr2917/sel-rtac_sel-ied_asdu1-5-20-45-46.pcap":                                 {21, 204, nil},
	"wireshark-mr2917/sel-rtac_unknown_asdu1-6-7-8-9.pcap":                                    {40, 337, map[string]int{classRefused: 18}},
	"wireshark-mr2917/sel-rtac_unknown_private-asdu205.pcap":                                  {88, 644, map[string]int{classRefused: 6}},
	"../../../iec104/testdata/captures/wireshark/synthetic_synthetic_iec104-coverage.pcap.gz": {23, 191, map[string]int{classRefused: 11}},
}

func TestOracleTshark(t *testing.T) {
	for _, s := range specs {
		if !hasOracle(s) {
			continue
		}
		t.Run(s.Path, func(t *testing.T) {
			version, rows := corpus.ReadOracle(t, oracleFile(s), oracleFields)
			byFrame := map[int][]Decoded{}
			for _, r := range read(t, s) {
				for _, d := range r.Decoded {
					byFrame[d.Last] = append(byFrame[d.Last], d)
				}
			}
			frames, values := 0, 0
			got := map[string]int{}
			diff := func(f int, field, w, g string) {
				c := classOther
				for _, d := range byFrame[f] {
					if d.ASDUErr != nil {
						c = classRefused
					}
				}
				got[c]++
				if c == classOther {
					t.Errorf("frame %d %s: tshark %q, go-iec60870-5 %q", f, field, w, g)
				}
			}
			for f, ds := range byFrame {
				want, ours := rows[f], ourRow(ds)
				if len(want) == 0 && len(ours) == 0 {
					continue
				}
				if len(want) == 0 {
					diff(f, "frame", "not dissected", fmt.Sprint(len(ds), " frames"))
					continue
				}
				frames++
				for _, field := range oracleFields {
					w, g := want[field], ours[field]
					if len(w) != len(g) {
						diff(f, field, strings.Join(w, ","), strings.Join(g, ","))
						continue
					}
					for i := range w {
						values++
						if w[i] != g[i] {
							diff(f, field, w[i], g[i])
						}
					}
				}
			}
			for f, row := range rows {
				if byFrame[f] == nil && len(row) > 0 {
					diff(f, "fields", "dissected", "no frame completes")
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
			t.Logf("%s: %d frames, %d values, %v", version, frames, values, got)
		})
	}
}
