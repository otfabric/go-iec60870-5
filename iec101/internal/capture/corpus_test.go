// SPDX-License-Identifier: MIT

package capture

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/otfabric/go-iec60870-5/ft12"
	"github.com/otfabric/go-iec60870-5/internal/serialcap"
	"github.com/otfabric/go-iec60870-5/internal/testutil/corpus"
)

// The IEC 101 corpus (testdata/captures/README.md). Every frame of every
// committed capture is split out of the recording, its checksum checked,
// its control field decoded, its ASDU decoded under the field sizes the
// manifest records, and frame and ASDU encoded again; the link procedures
// of both directions are checked (package serialcap); tshark's decode is
// compared field by field (oracle_test.go).

const dir = "../../testdata/captures"

// spec is one committed capture and what the tests expect of it.
type spec struct {
	corpus.File
	ports  []uint16 // TCP ports of the serial port servers; none for RTAC
	p      Params
	tshark []string // decode-as rules for the oracle
	// inexact are the frames (capture frame numbers) that do not encode
	// back byte for byte, with the reason.
	inexact map[int]string
	// findings is the number of link findings (serialcap.Link.Violations)
	// of a generated capture; recordings must have none unless listed in
	// the manifest's counts and explained in the README.
	findings int
}

const (
	srcWS15688 = "wireshark-issue-15688"
	srcWS16428 = "wireshark-issue-16428"
	srcWS17775 = "wireshark-issue-17775"
	srcSP      = "unddef/StreamPacketizer"
	srcWS      = "wireshark"
	upload     = "https://gitlab.com/-/project/7898047/uploads/"
	spTree     = "https://github.com/unddef/StreamPacketizer/blob/01a394334a1962c9b3f5cb5d08226b1c70c231d3/VSC_proj/"
)

var sources = []corpus.Source{
	{ID: srcWS15688, URL: "https://gitlab.com/wireshark/wireshark/-/issues/15688", Revision: "attachments 17041 and 17042 of bug 15688 (2019-04-09), migrated uploads",
		Terms: "Attached to a public Wireshark issue by the reporter; no licence is stated for the attachments. Wireshark itself is GPL-2.0-or-later.",
		Notes: "Issue \"Add support for IEC 60870-5-101\", reported by Chris Bontje, who wrote the IEC 101 dissector and the RTAC serial-line dissector. Both files are SEL RTAC serial-line captures (link type 250)."},
	{ID: srcWS16428, URL: "https://gitlab.com/wireshark/wireshark/-/issues/16428", Revision: "attachments of bug 16428 (2020-03-06), migrated uploads",
		Terms: "Attached to a public Wireshark issue by the reporter; no licence is stated for the attachments.",
		Notes: "Issue \"Dissector IEC 60870-5-101 can not decode some typeid information\", reported by João Paulo Rezende with the captures monitor_energisa_1_2020_03_05.pcapng (\"typeid 2\") and monitor_energisa_2.pcapng (\"typeid 30\"); iec101.tar.xz holds the same two files and two screenshots and is not committed."},
	{ID: srcWS17775, URL: "https://gitlab.com/wireshark/wireshark/-/issues/17775", Revision: "upload 64b2250b29a7ea42841126f6d5399b82 (2021-12-09)",
		Terms: "Attached to a public Wireshark issue by a Wireshark developer; no licence is stated for the attachment.",
		Notes: "Issue \"IEC 60870-5-101 link address field is 1 byte, but should have configurable length\": the developer \"added the frame of the screenshot\" of the reporter, one frame rebuilt from a screenshot."},
	{ID: srcSP, URL: "https://github.com/unddef/StreamPacketizer/tree/01a394334a1962c9b3f5cb5d08226b1c70c231d3/VSC_proj", Revision: "01a394334a1962c9b3f5cb5d08226b1c70c231d3",
		Terms: "The repository has no licence file and states no terms.",
		Notes: "A tool that cuts an IEC 101 byte stream into packets and writes them as TCP between 192.168.0.1:2424 and 192.168.0.2:1212 with whole-second time stamps and sequence numbers starting at 0 and 1000. test_output.pcap holds no frame and is not committed."},
	{ID: srcWS, URL: "https://gitlab.com/wireshark/wireshark/-/blob/f7e10bdd477b2f8f4995aa45c8155a0768f433ab/test/captures/iec104_coverage.pcap.gz", Revision: "f7e10bdd477b2f8f4995aa45c8155a0768f433ab",
		Terms: "The Wireshark repository is GPL-2.0-or-later; the capture has no statement of its own.",
		Notes: "The synthetic capture of the IEC 104 corpus (iec104/testdata/captures/wireshark), not committed twice: its TCP port 2405 carries generated IEC 101 frames."},
}

func rtac() []string { return []string{"-d", "rtacser.data,iec60870_101"} }

func tcp(ports ...uint16) []string {
	var out []string
	for _, p := range ports {
		out = append(out, "-d", fmt.Sprintf("tcp.port==%d,iec60870_101", p))
	}
	return out
}

var specs = []spec{
	{File: corpus.File{Path: "wireshark-issues/unknown_sel-rtac_class1-class2-polls.pcap", Source: srcWS15688,
		OriginalName: "IEC_101_Sample_Class_1_and_Class_2.pcap", URL: upload + "3ffc535b6782d21c872a012abb940424/IEC_101_Sample_Class_1_and_Class_2.pcap",
		Format: "pcap, SEL RTAC serial line (link type 250)", Evidence: "B",
		Basis:      "Written by an SEL RTAC's serial-line capture: the frames carry the UART control lines (RTS, CTS, DSR, DTR) of a physical serial port. The RTAC is the controlled station; neither its model and firmware nor the controlling station are stated.",
		Systems:    []string{"controlling station (not stated) on the RTAC's serial port", "SEL RTAC as controlled station, link address 64, common address 64"},
		Operations: "station interrogation (C_IC_NA_1) with confirmation and termination, class 1 and class 2 polls answered with M_SP_TB_1, M_DP_TB_1, M_ST_TB_1, M_ME_TD_1 (\"Class 1/2 Scans with Various Info Object Responses\")",
		Privacy:    "serial line only, no addresses", Missing: "the RTAC model and firmware, the controlling station"},
		p: Params{1, 1, 1, 2}, tshark: rtac()},
	{File: corpus.File{Path: "wireshark-issues/unknown_sel-rtac_double-command-sbo.pcap", Source: srcWS15688,
		OriginalName: "IEC_101_control_capture.pcap", URL: upload + "d5360b16b3b2bca36527fe9c2c8f313e/IEC_101_control_capture.pcap",
		Format: "pcap, SEL RTAC serial line (link type 250)", Evidence: "B",
		Basis:      "Written by an SEL RTAC's serial-line capture with the UART control lines of a physical port. The line is shared: the RTAC (link address 64) also records the polls of link addresses 78, 20 and 57 and the answers of the stations at 78 and 20.",
		Systems:    []string{"controlling station (not stated)", "SEL RTAC as controlled station, link address 64, common address 64", "controlled stations at link addresses 78 and 20 (heard only)", "a request of link status to link address 57 that no station answers"},
		Operations: "class 2 polls and requests of link status of several link addresses, a double command (C_DC_NA_1, IOA 22003) as select and execute with confirmations and termination (\"IEC 101 Select-Before-Operate sample\")",
		Privacy:    "serial line only, no addresses", Missing: "the RTAC model and firmware, the controlling station, the stations at link addresses 78 and 20"},
		p: Params{1, 1, 1, 2}, tshark: rtac()},
	{File: corpus.File{Path: "wireshark-issues/unknown_unknown_energisa-polls-m-sp-ta-1.pcapng", Source: srcWS16428,
		OriginalName: "monitor_energisa_1_2020_03_05.pcapng", URL: upload + "566eb4ef867fc002ecbf96cdcb6b077b/monitor_energisa_1_2020_03_05.pcapng",
		Format: "pcapng, Ethernet, IPv4, TCP to serial port servers (2101, 2102, 50001)", Evidence: "B",
		Basis:      "Recorded on the network of the distribution utility Energisa (file name, reporter): a controlling station on a Supermicro server (MAC 00:25:90) polls three serial lines through serial port servers of Digi International (00:40:9D) and Siemens Canada / RuggedCom (50:00:84). Physical serial lines; the controlled stations behind them are not identified.",
		Systems:    []string{"controlling station 192.150.52.11 (software not stated)", "controlled stations at link addresses 7 and 12 behind Digi port server 192.150.52.4 (TCP 2102, 2101)", "controlled station at link address 15 behind RuggedCom device 192.150.52.101 (TCP 50001)"},
		Operations: "class 1 and class 2 polls only from the controlling station; the stations answer with M_SP_TA_1 (CP24Time2a), M_ME_NC_1 periodic and spontaneous, C_CS_NA_1 with cause spontaneous and C_TS_NA_1 with cause activation; about 70 s",
		Privacy:    "private-range addresses of a utility network (192.150.52.0/24)", Missing: "make, model and firmware of the controlled stations and of the controlling station"},
		ports: []uint16{2101, 2102, 50001}, p: Params{1, 1, 1, 2}, tshark: tcp(2101, 2102, 50001)},
	{File: corpus.File{Path: "wireshark-issues/unknown_unknown_energisa-polls-m-sp-tb-1.pcapng", Source: srcWS16428,
		OriginalName: "monitor_energisa_2.pcapng", URL: upload + "0ad5557336accface09acfa939989815/monitor_energisa_2.pcapng",
		Format: "pcapng, Ethernet, IPv4, TCP to serial port servers (2101, 2102, 50001)", Evidence: "B",
		Basis:      "The same network and stations as monitor_energisa_1, about 18 minutes.",
		Systems:    []string{"controlling station 192.150.52.11", "controlled stations at link addresses 7 and 12 behind Digi port server 192.150.52.4", "controlled station at link address 15 behind RuggedCom device 192.150.52.101"},
		Operations: "class 1 and class 2 polls answered with M_DP_TA_1 (CP24Time2a), M_DP_TB_1, M_SP_TB_1, M_ME_NC_1 and C_TS_NA_1 with cause activation; about 18 minutes",
		Privacy:    "private-range addresses of a utility network", Missing: "make, model and firmware of the stations"},
		ports: []uint16{2101, 2102, 50001}, p: Params{1, 1, 1, 2}, tshark: tcp(2101, 2102, 50001)},
	{File: corpus.File{Path: "wireshark-issues/unknown_unknown_m-me-nb-1-link-address-2.pcapng", Source: srcWS17775,
		OriginalName: "issue-17775.pcapng", URL: upload + "64b2250b29a7ea42841126f6d5399b82/issue-17775.pcapng",
		Format: "pcapng, Ethernet, IPv4, TCP 950", Evidence: "E",
		Basis:      "One frame a Wireshark developer rebuilt from the reporter's screenshot, not a recording.",
		Systems:    []string{"controlled station at link address 3 (from the screenshot)"},
		Operations: "M_ME_NB_1 with eight objects, 2-octet link address, 2-octet common address, 3-octet IOA",
		Privacy:    "private addresses only", Missing: "the recording itself"},
		ports: []uint16{950}, p: Params{2, 1, 2, 3}, tshark: tcp(950)},
	{File: corpus.File{Path: "streampacketizer/synthetic_synthetic_class-polls.pcap", Source: srcSP,
		OriginalName: "VSC_proj/test.pcap", URL: spTree + "test.pcap",
		Format: "pcap, Ethernet, IPv4, TCP 2424", Evidence: "E",
		Basis:      "Written by the StreamPacketizer tool (whole-second time stamps, made-up addresses and sequence numbers); the origin of the byte stream is not stated.",
		Systems:    []string{"controlling station (generated)", "controlled station at link address 1 (generated)"},
		Operations: "class 1 and class 2 polls answered with \"no data\"",
		Privacy:    "made-up addresses", Missing: "the origin of the byte stream"},
		ports: []uint16{2424}, p: Params{1, 1, 1, 2}, tshark: tcp(2424)},
	{File: corpus.File{Path: "streampacketizer/synthetic_synthetic_status-polls.pcap", Source: srcSP,
		OriginalName: "VSC_proj/testing.pcap", URL: spTree + "testing.pcap",
		Format: "pcap, Ethernet, IPv4, TCP 2424", Evidence: "E",
		Basis:      "Written by the StreamPacketizer tool; the origin of the byte stream is not stated.",
		Systems:    []string{"controlling station (generated)", "controlled station at link address 1 (generated)"},
		Operations: "requests of link status, answered several times in a row, and resets of the link",
		Privacy:    "made-up addresses", Missing: "the origin of the byte stream"},
		ports: []uint16{2424}, p: Params{1, 1, 1, 2}, tshark: tcp(2424), findings: 3},
	{File: corpus.File{Path: "streampacketizer/synthetic_synthetic_interrogation-link-address-2.pcap", Source: srcSP,
		OriginalName: "VSC_proj/today.pcap", URL: spTree + "today.pcap",
		Format: "pcap, Ethernet, IPv4, TCP 2424", Evidence: "E",
		Basis:      "Written by the StreamPacketizer tool; the origin of the byte stream is not stated. The one ASDU fixes the field sizes: 2-octet cause, common address 1000 in 2 octets, IOA 0 in 2 octets.",
		Systems:    []string{"controlling station (generated)", "controlled station at link address 1, 2-octet link address (generated)"},
		Operations: "class 1 and class 2 polls answered with \"no data\", one station interrogation",
		Privacy:    "made-up addresses", Missing: "the origin of the byte stream"},
		ports: []uint16{2424}, p: Params{2, 2, 2, 2}, tshark: tcp(2424), findings: 1},
	{File: corpus.File{Path: "../../../iec104/testdata/captures/wireshark/synthetic_synthetic_iec104-coverage.pcap.gz", Source: srcWS,
		OriginalName: "test/captures/iec104_coverage.pcap.gz", URL: "https://gitlab.com/wireshark/wireshark/-/blob/f7e10bdd477b2f8f4995aa45c8155a0768f433ab/test/captures/iec104_coverage.pcap.gz",
		Format: "pcap (gzip), Ethernet, IPv4, TCP 2405 (IEC 101 part)", Evidence: "E",
		Basis:      "Generated for Wireshark's dissector tests (commit message); the IEC 104 corpus records it as E too. The frames follow no exchange: responses without requests, FCB not toggled.",
		Systems:    []string{"generated controlling station", "generated controlled station at link address 5, common address 9"},
		Operations: "every link function, E5h, monitor and control ASDUs of the common types",
		Privacy:    "documentation addresses (192.0.2.0/24)", Missing: "-"},
		ports: []uint16{2405}, p: Params{1, 1, 1, 2}, tshark: tcp(2405), findings: 16,
		inexact: map[int]string{573: "a sequence of single point information with reserved SIQ bits set (0xFF, 0x55): the encoder writes them as zero"}},
}

// counts is what go-iec60870-5 derives from a capture.
type counts struct {
	Lines       int   `json:"lines"`
	Frames      int   `json:"frames"`
	Single      int   `json:"single_characters"`
	Fixed       int   `json:"fixed_frames"`
	Variable    int   `json:"variable_frames"`
	Undecodable int   `json:"undecodable_stretches"`
	ASDUErrors  int   `json:"asdu_errors"`
	Exact       int   `json:"reencoded_exactly"`
	Exchanges   int   `json:"exchanges"`
	Leading     int   `json:"responses_before_first_request"`
	Overheard   int   `json:"responses_overheard"`
	Repeated    int   `json:"repetitions"`
	Abandoned   []int `json:"requests_abandoned,omitempty"`
	FCBToggled  int   `json:"fcb_toggles_checked"`
	Balanced    bool  `json:"balanced"`
	PrimaryFn   []int `json:"primary_functions"`
	SecondaryFn []int `json:"secondary_functions"`
	Types       []int `json:"type_identifications"`
	Causes      []int `json:"causes"`
	// ASDUs counts the ASDUs by sender, type and cause, e.g.
	// "controlled C_TS_NA_1 cause 6".
	ASDUs        map[string]int `json:"asdus"`
	LinkFindings []string       `json:"link_findings,omitempty"`
}

// recording is a capture read and decoded.
type recording struct {
	serialcap.Recording
	Decoded []Decoded
	Link    *serialcap.Link
}

func read(t testing.TB, s spec) []recording {
	t.Helper()
	recs, err := serialcap.ReadFile(filepath.Join(dir, filepath.FromSlash(s.Path)), s.p.FT12(), s.ports...)
	if err != nil {
		t.Fatalf("%s: %v", s.Path, err)
	}
	out := make([]recording, 0, len(recs))
	for _, r := range recs {
		out = append(out, recording{Recording: r, Decoded: Decode(r.Line, s.p), Link: serialcap.Analyze(r.Line)})
	}
	return out
}

func derive(t testing.TB, s spec) (counts, []string) {
	t.Helper()
	c := counts{ASDUs: map[string]int{}}
	var bad []string
	types, causes := map[int]bool{}, map[int]bool{}
	pf, sf := map[byte]int{}, map[byte]int{}
	for _, r := range read(t, s) {
		c.Lines++
		for _, b := range r.Bad {
			bad = append(bad, fmt.Sprintf("%s: %s", r.Name, b))
		}
		c.Undecodable += len(r.Bad)
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
				types[int(d.ASDU.Type)] = true
				causes[int(d.ASDU.Cause)] = true
				who := "controlled"
				if d.Control().PRM() {
					who = "controlling"
				}
				c.ASDUs[fmt.Sprintf("%s %s cause %d", who, d.ASDU.Type, d.ASDU.Cause)]++
			}
		}
		l := r.Link
		c.Exchanges += l.Exchanges
		c.Leading += l.Leading
		c.Overheard += l.Overheard
		c.Repeated += l.Repeated
		c.Abandoned = append(c.Abandoned, l.Abandoned...)
		c.FCBToggled += l.FCBToggled
		c.Balanced = c.Balanced || l.Balanced
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
	for k := range types {
		c.Types = append(c.Types, k)
	}
	for k := range causes {
		c.Causes = append(c.Causes, k)
	}
	slices.Sort(c.Types)
	slices.Sort(c.Causes)
	return c, bad
}

// TestCorpus checks every frame of every committed capture.
func TestCorpus(t *testing.T) {
	for _, s := range specs {
		t.Run(s.Path, func(t *testing.T) {
			for _, r := range read(t, s) {
				// Every octet is a frame: the recordings hold no noise and
				// no frame cut by the capture.
				for _, b := range r.Bad {
					t.Errorf("%s: %s", r.Name, b)
				}
				for _, d := range r.Decoded {
					if d.ASDUErr != nil {
						t.Errorf("%s frame %d: %v (% x)", r.Name, d.Last, d.ASDUErr, d.Raw)
						continue
					}
					why, listed := s.inexact[d.Last]
					if d.Exact() == listed {
						t.Errorf("%s frame %d: exact %v, listed %q:\n  recorded   % x\n  re-encoded % x", r.Name, d.Last, d.Exact(), why, d.Raw, d.Reencoded)
					}
					// The control field decodes to what it was.
					c := d.Control()
					if d.Kind != ft12.KindSingle && byte(c) != d.Frame.Frame.Control {
						t.Errorf("frame %d: control %v", d.Last, c)
					}
				}
				l := r.Link
				if s.findings == 0 {
					for _, v := range l.Violations {
						t.Errorf("%s: %s", r.Name, v)
					}
				}
				// Unbalanced: one side sends the primary messages, the
				// other the responses; on a shared line the recording
				// station also hears other stations' responses.
				ctl := l.Controlling()
				if !l.Balanced && s.findings == 0 && l.Primary[0]+l.Primary[1] > 0 && (l.Primary[1-ctl] != 0 || l.Secondary[ctl]+l.Single[ctl] != l.Overheard) {
					t.Errorf("%s: primary %v, secondary %v, E5h %v, overheard %d", r.Name, l.Primary, l.Secondary, l.Single, l.Overheard)
				}
			}
			if s.Evidence != "E" {
				i := slices.IndexFunc(Replays, func(r Replay) bool { return r.Path == s.Path })
				if i < 0 || !slices.Equal(Replays[i].Ports, s.ports) || Replays[i].Params != s.p {
					t.Error("not in Replays with the ports and parameters of the manifest")
				}
			}
			c, _ := derive(t, s)
			if s.findings != len(c.LinkFindings) {
				t.Errorf("%d link findings, want %d: %v", len(c.LinkFindings), s.findings, c.LinkFindings)
			}
			if c.Frames == 0 {
				t.Error("no frames")
			}
		})
	}
}

func TestManifest(t *testing.T) {
	m := corpus.Manifest{
		Description: "Recorded IEC 60870-5-101 traffic go-iec60870-5 is tested against (testdata/captures/README.md). Files are byte-identical to their sources.",
		Levels:      corpus.Levels, Sources: sources,
	}
	for _, s := range specs {
		f := s.File
		corpus.Fill(t, dir, &f)
		f.Params = s.p
		c, bad := derive(t, s)
		f.Counts, f.Undecodable = c, bad
		m.Files = append(m.Files, f)
	}
	corpus.CheckManifest(t, dir, m)
	var listed []string
	for _, s := range specs {
		if !strings.HasPrefix(s.Path, "../") {
			listed = append(listed, s.Path)
		}
	}
	slices.Sort(listed)
	if got := corpus.Committed(t, dir, ".pcap", ".pcapng", ".pcap.gz"); !slices.Equal(got, listed) {
		t.Errorf("committed %v, in specs %v", got, listed)
	}
}

// TestGenerateFuzzSeeds writes seeds for ft12.FuzzDecode and asdu.FuzzDecode:
// one frame and one ASDU of every distinct shape of the recordings
// (evidence A to D). It runs with CAPTURE_GENERATE=1 (make captures-manifest).
func TestGenerateFuzzSeeds(t *testing.T) {
	seen := map[string]bool{}
	for _, s := range specs {
		if s.Evidence == "E" {
			continue
		}
		for _, r := range read(t, s) {
			for _, d := range r.Decoded {
				key := fmt.Sprint(s.p, d.Kind, d.Frame.Frame.Control&0x4f)
				if d.ASDU != nil {
					key += fmt.Sprint(d.ASDU.Type, d.ASDU.Cause, d.ASDU.Sequence)
				}
				if seen[key] {
					continue
				}
				seen[key] = true
				corpus.FuzzSeed(t, "../../../ft12/testdata/fuzz/FuzzDecode", d.Raw, s.p.LinkAddr)
				if d.ASDU != nil {
					corpus.FuzzSeed(t, "../../../asdu/testdata/fuzz/FuzzDecode", d.Frame.Frame.Data)
				}
			}
		}
	}
	if len(seen) == 0 {
		t.Fatal("no shapes")
	}
}
