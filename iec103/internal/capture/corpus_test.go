// SPDX-License-Identifier: MIT

package capture

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/otfabric/go-iec60870-5/ft12"
	"github.com/otfabric/go-iec60870-5/iec103"
	"github.com/otfabric/go-iec60870-5/internal/pcapio"
	"github.com/otfabric/go-iec60870-5/internal/serialcap"
	"github.com/otfabric/go-iec60870-5/internal/testutil/corpus"
)

// The IEC 103 corpus (testdata/captures/README.md): every frame split out
// of the recording, checksum and control field checked, the link
// procedures of both directions checked, the ASDU decoded with the iec103
// codec (private types as Raw) and frame and ASDU encoded again;
// tshark's decode compared in oracle_test.go.

const dir = "../../testdata/captures"

type spec struct {
	corpus.File
	ports  []uint16 // TCP ports; none for RTAC or hex vectors
	tshark []string
	// findings is the number of link findings of a generated capture.
	findings int
	// inexact are the capture frames that do not encode back byte for
	// byte, asduErrors those whose ASDU the codec refuses, findingsAt
	// those with a link finding; each with the reason.
	inexact, asduErrors, findingsAt map[int]string
}

const (
	srcMR    = "wireshark-mr-2917"
	srcAR    = "masarray/ARIEC103"
	srcWS    = "wireshark"
	upload   = "https://gitlab.com/-/project/7898047/uploads/"
	arVector = "https://github.com/masarray/ARIEC103/blob/ec08dd73991be646864aef55e6a5538b78129db8/samples/test-vectors/"
)

var sources = []corpus.Source{
	{ID: srcMR, URL: "https://gitlab.com/wireshark/wireshark/-/merge_requests/2917", Revision: "uploads of the merge request's first comment (2021-05-04); merged 2021-05-09",
		Terms: "Attached to a public Wireshark merge request by its author; no licence is stated for the attachments. Wireshark itself is GPL-2.0-or-later.",
		Notes: "Merge request \"packet-iec104.c - Add IEC 60870-5-103 Protocol Dissection\" by Chris Bontje: \"sample PCAP files providing examples of the supported ASDU types\", SEL RTAC serial-line captures (decode the RTAC serial line as IEC 103)."},
	{ID: srcAR, URL: "https://github.com/masarray/ARIEC103/tree/ec08dd73991be646864aef55e6a5538b78129db8/samples/test-vectors", Revision: "ec08dd73991be646864aef55e6a5538b78129db8",
		Terms: "Apache-2.0 (repository licence).",
		Notes: "\"Sanitized FT1.2 frame examples\" of the ARIEC103 .NET protocol library (samples/test-vectors/README.md), each with the decode it expects in a comment."},
	{ID: srcWS, URL: "https://gitlab.com/wireshark/wireshark/-/blob/f7e10bdd477b2f8f4995aa45c8155a0768f433ab/test/captures/iec104_coverage.pcap.gz", Revision: "f7e10bdd477b2f8f4995aa45c8155a0768f433ab",
		Terms: "The Wireshark repository is GPL-2.0-or-later; the capture has no statement of its own.",
		Notes: "The synthetic capture of the IEC 104 corpus (iec104/testdata/captures/wireshark), not committed twice: its TCP port 2406 carries generated IEC 103 frames."},
}

const (
	excerpt      = "the excerpt leaves out frames"
	measurands16 = "ASDU 9 with 16 measurands; clause 7.3.1.8 allows one to nine: the equipment's deviation, the codec refuses the ASDU"
	vsq01        = "VSQ 01h where clause 7.2.2 has 81h for a single information object: the encoder writes 81h"
)

// with sets the expected inexact frames and ASDU errors of s.
func with(s spec, inexact, asduErrors map[int]string) spec {
	s.inexact, s.asduErrors = inexact, asduErrors
	return s
}

func rtacBasis(extra string) string {
	return "Written by an SEL RTAC's serial-line capture with the UART control lines of a physical port; the RTAC is the controlling system. " + extra
}

func hexSpec(name, original, ops string) spec {
	return spec{File: corpus.File{Path: "ariec103/synthetic_synthetic_" + name + ".hex", Source: srcAR, OriginalName: "samples/test-vectors/" + original,
		URL: arVector + original, Format: "text, one FT 1.2 frame per line in hexadecimal", Evidence: "E",
		Basis:   "Hand-made test vector of another implementation (\"sanitized\", \"project-neutral\"), not a recording.",
		Systems: []string{"generated protection equipment, link address 1"}, Operations: ops,
		Privacy: "none", Missing: "-"}}
}

var specs = []spec{
	{File: corpus.File{Path: "wireshark-mr2917/sel-rtac_sel-ied_asdu1-5-20-45-46.pcap", Source: srcMR,
		OriginalName: "iec103_asdu1_5_20_45_46.pcap", URL: upload + "496922fdf33fce9a50ebb548c03fb00b/iec103_asdu1_5_20_45_46.pcap",
		Format: "pcap, SEL RTAC serial line (link type 250)", Evidence: "B",
		Basis:      rtacBasis("The protection equipment identifies itself as \"SEL\" in ASDU 5; its model and firmware are not stated."),
		Systems:    []string{"SEL RTAC as controlling system", "protection equipment of manufacturer \"SEL\" (ASDU 5), link address 1"},
		Operations: "identification (ASDU 5), general commands (ASDU 20) with their time-tagged answers (ASDU 1), class 1 and 2 polls, private ASDUs 45 and 46 in the control direction",
		Privacy:    "serial line only", Missing: "model and firmware of both"},
		tshark: []string{"-d", "rtacser.data,iec60870_5_103"},
		findingsAt: map[int]string{
			8:  excerpt + " (2.2 s without a frame before it on a line the RTAC polls continuously): FCB looks not toggled",
			20: excerpt + " (41 s without a frame before it): FCB looks not toggled",
		}},
	{File: corpus.File{Path: "wireshark-mr2917/sel-rtac_unknown_asdu1-6-7-8-9.pcap", Source: srcMR,
		OriginalName: "iec103_asdu1_6_7_8_9.pcap", URL: upload + "6c6eb65be80cdb92028ae8552b93092e/iec103_asdu1_6_7_8_9.pcap",
		Format: "pcap, SEL RTAC serial line (link type 250)", Evidence: "B",
		Basis:      rtacBasis("The protection equipment is not identified."),
		Systems:    []string{"SEL RTAC as controlling system", "protection equipment (not identified), link address 1"},
		Operations: "time synchronization (ASDU 6), general interrogation (ASDU 7) answered with ASDU 1 and its termination (ASDU 8), cyclic measurands (ASDU 9)",
		Privacy:    "serial line only", Missing: "the protection equipment, the RTAC model and firmware"},
		tshark:     []string{"-d", "rtacser.data,iec60870_5_103"},
		asduErrors: map[int]string{3: measurands16, 5: measurands16, 8: measurands16},
		findingsAt: map[int]string{
			8: excerpt + " (149 s between frames 7 and 8): the answer to a poll that is not in the file",
			9: excerpt + ": FCB as after the left-out polls",
		}},
	{File: corpus.File{Path: "wireshark-mr2917/sel-rtac_unknown_private-asdu205.pcap", Source: srcMR,
		OriginalName: "iec103_asdu205.pcap", URL: upload + "b7c9eccb7539031e36c337556aa71c1f/iec103_asdu205.pcap",
		Format: "pcap, SEL RTAC serial line (link type 250)", Evidence: "B",
		Basis:      rtacBasis("A shared line with protection equipment at link addresses 1, 2 and 3; addresses 4 and 5 do not answer. None is identified."),
		Systems:    []string{"SEL RTAC as controlling system", "protection equipment at link addresses 1, 2 and 3 (not identified)"},
		Operations: "reset of the frame count bit to five link addresses, class 2 polls answered with measurands (ASDU 9) and the private ASDU 205 (function types 133 and 134)",
		Privacy:    "serial line only", Missing: "the protection equipment, the meaning of ASDU 205"},
		tshark:     []string{"-d", "rtacser.data,iec60870_5_103"},
		asduErrors: map[int]string{8: "ASDU 9 with 14 measurands; clause 7.3.1.8 allows one to nine: the equipment's deviation, the codec refuses the ASDU"}},
	with(hexSpec("class1-event-type1", "class1-event-type1.hex", "ASDU 1 with cause spontaneous, FUN 192, INF 36, DPI 2, 14:34:12.345"),
		map[int]string{3: vsq01}, nil),
	hexSpec("class2-measurand-type9", "class2-measurand-type9.hex", "ASDU 9, first value 1234"),
	hexSpec("fixed-no-data", "fixed-no-data.hex", "fixed frame: secondary function 9 (no data)"),
	with(hexSpec("gi-end-type8", "gi-end-type8.hex", "ASDU 8, end of general interrogation"), map[int]string{3: vsq01}, nil),
	with(hexSpec("identification-type5", "identification-type5.hex", "ASDU 5 with the text \"ArIEC103 Relay Simulator\""), nil,
		map[int]string{3: "ASDU 5 without the compatibility level and with 24 characters where clause 7.3.1.5 has the level, eight characters and four octets of software identification: the vector's error"}),
	hexSpec("unknown-private-type205", "unknown-private-type205.hex", "private ASDU 205 with two octets of data"),
	{File: corpus.File{Path: "../../../iec104/testdata/captures/wireshark/synthetic_synthetic_iec104-coverage.pcap.gz", Source: srcWS,
		OriginalName: "test/captures/iec104_coverage.pcap.gz", URL: "https://gitlab.com/wireshark/wireshark/-/blob/f7e10bdd477b2f8f4995aa45c8155a0768f433ab/test/captures/iec104_coverage.pcap.gz",
		Format: "pcap (gzip), Ethernet, IPv4, TCP 2406 (IEC 103 part)", Evidence: "E",
		Basis:      "Generated for Wireshark's dissector tests; the frames follow no exchange.",
		Systems:    []string{"generated controlling system", "generated protection equipment"},
		Operations: "the ASDUs the Wireshark dissector knows, in both directions",
		Privacy:    "documentation addresses (192.0.2.0/24)", Missing: "-"},
		ports: []uint16{2406}, tshark: []string{"-d", "tcp.port==2406,iec60870_5_103"}, findings: 19,
		inexact: map[int]string{601: vsq01, 605: vsq01, 607: vsq01 + "; a day of week that does not match the date", 609: vsq01,
			611: "a measurand with its reserved bit set: the encoder writes it as zero", 617: vsq01 + "; a day of week that does not match the date",
			619: vsq01, 621: vsq01},
		asduErrors: map[int]string{603: "ASDU 1 with two information objects (VSQ 02h); clause 7.2.2 has one (81h): generated"}},
}

type recording struct {
	serialcap.Recording
	Decoded []Decoded
	Link    *serialcap.Link
}

func read(t testing.TB, s spec) []recording {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(s.Path))
	var recs []serialcap.Recording
	if strings.HasSuffix(s.Path, ".hex") {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		runs, err := Hex(b)
		if err != nil {
			t.Fatalf("%s: %v", s.Path, err)
		}
		recs = []serialcap.Recording{{Name: "vectors", Line: serialcap.Split([2][]pcapio.Run{nil, runs}, FT12)}}
	} else {
		var err error
		if recs, err = serialcap.ReadFile(path, FT12, s.ports...); err != nil {
			t.Fatalf("%s: %v", s.Path, err)
		}
	}
	out := make([]recording, 0, len(recs))
	for _, r := range recs {
		out = append(out, recording{Recording: r, Decoded: Decode(r.Line), Link: serialcap.Analyze(r.Line)})
	}
	return out
}

type counts struct {
	Lines        int            `json:"lines"`
	Frames       int            `json:"frames"`
	Single       int            `json:"single_characters"`
	Fixed        int            `json:"fixed_frames"`
	Variable     int            `json:"variable_frames"`
	ASDUErrors   int            `json:"asdu_errors"`
	Exact        int            `json:"reencoded_exactly"`
	Raw          int            `json:"raw_bodies"`
	Exchanges    int            `json:"exchanges"`
	Leading      int            `json:"responses_before_first_request"`
	Overheard    int            `json:"responses_overheard"`
	Repeated     int            `json:"repetitions"`
	Abandoned    []int          `json:"requests_abandoned,omitempty"`
	FCBToggled   int            `json:"fcb_toggles_checked"`
	PrimaryFn    []int          `json:"primary_functions"`
	SecondaryFn  []int          `json:"secondary_functions"`
	ASDUs        map[string]int `json:"asdus"`
	LinkFindings []string       `json:"link_findings,omitempty"`
}

func derive(t testing.TB, s spec) (counts, []string) {
	t.Helper()
	c := counts{ASDUs: map[string]int{}}
	var bad []string
	pf, sf := map[byte]int{}, map[byte]int{}
	for _, r := range read(t, s) {
		c.Lines++
		for _, b := range r.Bad {
			bad = append(bad, fmt.Sprintf("%s: %s", r.Name, b))
		}
		for _, d := range r.Decoded {
			c.Frames++
			switch d.Kind {
			case ft12.KindSingle:
				c.Single++
			case ft12.KindFixed:
				c.Fixed++
			default:
				c.Variable++
			}
			if d.ASDUErr != nil {
				c.ASDUErrors++
			}
			if d.Exact() {
				c.Exact++
			}
			if d.ASDU != nil {
				who := "protection equipment"
				if d.Control().PRM() {
					who = "controlling system"
				}
				if _, ok := d.ASDU.Body.(iec103.Raw); ok {
					c.Raw++
				}
				c.ASDUs[fmt.Sprintf("%s ASDU %d cause %d FUN %d", who, d.ASDU.Type, d.ASDU.Cause, d.ASDU.Fun)]++
			}
		}
		l := r.Link
		c.Exchanges += l.Exchanges
		c.Leading += l.Leading
		c.Overheard += l.Overheard
		c.Repeated += l.Repeated
		c.Abandoned = append(c.Abandoned, l.Abandoned...)
		c.FCBToggled += l.FCBToggled
		for k, v := range l.PrimaryFunc {
			pf[k] += v
		}
		for k, v := range l.SecondaryFunc {
			sf[k] += v
		}
		for _, v := range l.Violations {
			c.LinkFindings = append(c.LinkFindings, fmt.Sprintf("%s: %s", r.Name, v))
		}
	}
	for _, k := range serialcap.Funcs(pf) {
		c.PrimaryFn = append(c.PrimaryFn, int(k))
	}
	for _, k := range serialcap.Funcs(sf) {
		c.SecondaryFn = append(c.SecondaryFn, int(k))
	}
	return c, bad
}

func TestCorpus(t *testing.T) {
	keys := func(m map[int]string) []int {
		var out []int
		for k := range m {
			out = append(out, k)
		}
		slices.Sort(out)
		return out
	}
	for _, s := range specs {
		t.Run(s.Path, func(t *testing.T) {
			inexact, asduErrors, findings := map[int]string{}, map[int]string{}, map[int]string{}
			for _, r := range read(t, s) {
				for _, b := range r.Bad {
					t.Errorf("%s: %s", r.Name, b)
				}
				for _, d := range r.Decoded {
					switch {
					case d.ASDUErr != nil:
						asduErrors[d.Last] = d.ASDUErr.Error()
					case !d.Exact():
						inexact[d.Last] = fmt.Sprintf("recorded % x, re-encoded % x", d.Raw, d.Reencoded)
					}
				}
				for _, v := range r.Link.Violations {
					findings[v.Frame] = v.What
				}
				// IEC 60870-5-103 6.2.3: unbalanced, the controlling system
				// is always primary.
				l := r.Link
				if l.Primary[0]+l.Primary[1] > 0 && s.findings == 0 && (l.Balanced || l.Primary[1-l.Controlling()] != 0) {
					t.Errorf("%s: primary messages in both directions %v", r.Name, l.Primary)
				}
			}
			for _, c := range []struct {
				what      string
				got, want map[int]string
			}{{"not re-encoded exactly", inexact, s.inexact}, {"ASDU refused", asduErrors, s.asduErrors}, {"link finding", findings, s.findingsAt}} {
				if c.what == "link finding" && s.findings > 0 {
					if len(c.got) != s.findings {
						t.Errorf("%d link findings, want %d: %v", len(c.got), s.findings, c.got)
					}
					continue
				}
				if !slices.Equal(keys(c.got), keys(c.want)) {
					for _, k := range keys(c.got) {
						t.Logf("frame %d %s: %s", k, c.what, c.got[k])
					}
					t.Errorf("%s in frames %v, listed %v", c.what, keys(c.got), keys(c.want))
				}
			}
		})
	}
}

// TestVectors checks the decode the ARIEC103 vectors state in their
// comments.
func TestVectors(t *testing.T) {
	first := func(name string) Decoded {
		for _, s := range specs {
			if strings.Contains(s.Path, name) {
				return read(t, s)[0].Decoded[0]
			}
		}
		t.Fatalf("no vector %s", name)
		return Decoded{}
	}
	if d := first("class1-event"); d.ASDU.Type != iec103.TypeTimeTagged || d.ASDU.Cause != iec103.CauseSpontaneous || d.ASDU.Fun != 192 || d.ASDU.Inf != 36 {
		t.Errorf("type 1: %s", d.ASDU)
	} else if b := d.ASDU.Body.(iec103.TimeTagged); b.DPI != 2 || b.Time.Hour() != 14 || b.Time.Minute() != 34 || b.Time.Second() != 12 || b.Time.Nanosecond() != 345e6 {
		t.Errorf("type 1 body: %+v", b)
	}
	// "first signed 16-bit value 1234": the octets D2h 04h are one MEA
	// (clause 7.2.6.8): MVAL 154 with ER set; the vector reads them as a
	// plain integer.
	if d := first("measurand-type9"); d.ASDU.Type != iec103.TypeMeasurands2 || d.ASDU.Body.(iec103.Measurands).Values[0].Raw != 1234>>3 || !d.ASDU.Body.(iec103.Measurands).Values[0].Error {
		t.Errorf("type 9: %s", d.ASDU)
	}
	if d := first("fixed-no-data"); d.Kind != ft12.KindFixed || d.Control().Func() != 9 {
		t.Errorf("no data: %+v", d)
	}
	if d := first("gi-end"); d.ASDU.Type != iec103.TypeGITermination || d.ASDU.Cause != iec103.CauseGITermination {
		t.Errorf("type 8: %s", d.ASDU)
	}
	// The vector carries 24 characters and no compatibility level: refused.
	if d := first("identification"); d.ASDUErr == nil {
		t.Errorf("type 5 accepted: %s", d.ASDU)
	}
	if d := first("private-type205"); d.ASDU.Type != 205 || len(d.ASDU.Body.(iec103.Raw).Data) != 2 {
		t.Errorf("type 205: %s %#v", d.ASDU, d.ASDU.Body)
	}
}

func TestManifest(t *testing.T) {
	m := corpus.Manifest{
		Description: "Recorded IEC 60870-5-103 traffic and test vectors go-iec60870-5 is tested against (testdata/captures/README.md). Files are byte-identical to their sources.",
		Levels:      corpus.Levels, Sources: sources,
	}
	var listed []string
	for _, s := range specs {
		f := s.File
		corpus.Fill(t, dir, &f)
		c, bad := derive(t, s)
		f.Counts, f.Undecodable = c, bad
		m.Files = append(m.Files, f)
		if !strings.HasPrefix(s.Path, "../") {
			listed = append(listed, s.Path)
		}
	}
	corpus.CheckManifest(t, dir, m)
	slices.Sort(listed)
	if got := corpus.Committed(t, dir, ".pcap", ".pcapng", ".hex"); !slices.Equal(got, listed) {
		t.Errorf("committed %v, in specs %v", got, listed)
	}
}

// TestGenerateFuzzSeeds writes seeds for iec103.FuzzDecode and
// ft12.FuzzDecode from the recordings (evidence A to D); it runs with
// CAPTURE_GENERATE=1 (make captures-manifest).
func TestGenerateFuzzSeeds(t *testing.T) {
	seen := map[string]bool{}
	for _, s := range specs {
		if s.Evidence == "E" {
			continue
		}
		for _, r := range read(t, s) {
			for _, d := range r.Decoded {
				key := fmt.Sprint(d.Kind, d.Frame.Frame.Control&0x4f)
				if d.ASDU != nil {
					key += fmt.Sprint(d.ASDU.Type, d.ASDU.Cause, d.ASDU.Fun)
				}
				if seen[key] {
					continue
				}
				seen[key] = true
				corpus.FuzzSeed(t, "../../../ft12/testdata/fuzz/FuzzDecode", d.Raw, 1)
				if d.ASDU != nil {
					corpus.FuzzSeed(t, "../../testdata/fuzz/FuzzDecode", d.Frame.Frame.Data)
				}
			}
		}
	}
	if len(seen) == 0 {
		t.Fatal("no shapes")
	}
}
