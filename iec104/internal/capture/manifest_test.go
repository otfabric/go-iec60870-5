// SPDX-License-Identifier: MIT

package capture

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/otfabric/go-iec60870-5/asdu"
	"github.com/otfabric/go-iec60870-5/internal/pcapio"
)

// testdata/captures/manifest.json describes every committed capture: where
// it comes from, what is known about the systems in it, its evidence level
// and what go-iec60870-5 derives from it. TestManifest checks the files and the
// counts against it; IEC104_CAPTURE_GENERATE=1 (make captures-manifest)
// writes it again from the sources below and the code.

// source is where a set of captures comes from.
type source struct {
	ID       string `json:"id"`
	URL      string `json:"url"`
	Revision string `json:"revision"`
	Terms    string `json:"terms"`
	Notes    string `json:"notes"`
}

// entry is one committed capture.
type entry struct {
	Path         string   `json:"path"`
	Source       string   `json:"source"`
	OriginalName string   `json:"original_name"`
	URL          string   `json:"url"`
	Size         int      `json:"size"`
	SHA256       string   `json:"sha256"`
	Format       string   `json:"format"`
	Recorded     string   `json:"recorded"`
	Evidence     string   `json:"evidence"`
	Basis        string   `json:"evidence_basis"`
	Systems      []string `json:"systems"`
	Operations   string   `json:"operations"`
	Privacy      string   `json:"privacy"`
	Missing      string   `json:"missing_for_verification"`
	Counts       counts   `json:"counts"`
}

// counts is what go-iec60870-5 derives from a capture.
type counts struct {
	Conversations int `json:"conversations"`
	FromZero      int `json:"conversations_from_start"`
	APDUs         int `json:"apdus"`
	I             int `json:"i_frames"`
	S             int `json:"s_frames"`
	U             int `json:"u_frames"`
	APCIErrors    int `json:"apci_errors"`
	ASDUErrors    int `json:"asdu_errors"`
	Exact         int `json:"reencoded_exactly"`
	Retransmitted int `json:"tcp_retransmitted"`
	OutOfOrder    int `json:"tcp_out_of_order"`
	Gaps          int `json:"tcp_gaps"`
	Split         int `json:"apdus_split_over_segments"`
	Shared        int `json:"segments_with_several_apdus"`
	Skipped       int `json:"octets_skipped"`
	SeqErrors     int `json:"sequence_errors"`
	StoppedI      int `json:"i_frames_while_stopped"`
	Types         int `json:"type_identifications"`
}

type manifest struct {
	Description string   `json:"description"`
	Levels      any      `json:"evidence_levels"`
	Sources     []source `json:"sources"`
	Files       []entry  `json:"files"`
}

const (
	srcCERTLV  = "cert-lv/spicy-iec104"
	srcUOWM    = "zenodo-7108614"
	srcWireshk = "wireshark"
	srcESET    = "eset/malware-research"
	srcITI     = "ITI/ICS-Security-Tools"
	srcNDPI    = "ntop/nDPI"
	srcWSWiki  = "wireshark-wiki"
	srcWSIssue = "wireshark-issues"
	srcEmre    = "EmreEkin/ICS-Pcaps"
)

var manifestSources = []source{
	{ID: srcCERTLV, URL: "https://github.com/cert-lv/spicy-iec104/tree/e758272584b063a4789899db19be7d56a4f665c3/testing/Traces",
		Revision: "e758272584b063a4789899db19be7d56a4f665c3",
		Terms:    "The repository is under the BSD 3-Clause licence (COPYING, Copyright 2023 Georgios Michail Makrakis, 2024 CERT.LV); the traces have no statement of their own.",
		Notes: "Test traces of CERT.LV's Spicy/Zeek IEC 104 analyzer. first/iec104.pcap is a copy of https://github.com/automayt/ICS-pcap/blob/master/IEC%2060870/iec104/iec104.pcap (testing/Traces/first/README); fifth/ and sixth/ were added in 2025 (commits 344fe86, b06d12f) without a statement of how they were made. " +
			"second/, third/ and fourth/ are byte-identical copies of files of the Zenodo record and are fetched by make captures-fetch, not committed. testing/Baseline holds Zeek's logs of the traces."},
	{ID: srcUOWM, URL: "https://zenodo.org/records/7108614",
		Revision: "record 7108614, revision 6 (published 2022-09-23, updated 2024-07-16)",
		Terms:    "CC-BY-4.0 (record metadata). Cite: P. Radoglou-Grammatikis et al., IEEE Transactions on Industrial Informatics 18(3), 2022, doi:10.1109/TII.2021.3093905.",
		Notes: "IEC 60870-5-104 Intrusion Detection Dataset, University of Western Macedonia. Each 7z archive holds, per entity, the whole traffic and an *_iec104_only.pcap filtered to the IEC 104 frames (no SYN, no ACK-only segments). " +
			"The ReadMe names five virtual RTUs (IEC TestServer on Windows 7), two real RTU devices, QTester104 v2.1 as HMI and three Kali Linux attackers (Metasploit iec104 module, OpenMUC j60870). " +
			"The ReadMe identifies the real RTUs neither by address nor by make or model. In the recordings all seven controlled stations (192.168.1.13, .19, .21, .22, .24, .25, .26) have MAC addresses of the VirtualBox range 08:00:27 and behave identically; none can be shown to be a physical device."},
	{ID: srcWireshk, URL: "https://gitlab.com/wireshark/wireshark/-/blob/f7e10bdd477b2f8f4995aa45c8155a0768f433ab/test/captures/iec104_coverage.pcap.gz",
		Revision: "f7e10bdd477b2f8f4995aa45c8155a0768f433ab",
		Terms:    "The Wireshark repository is GPL-2.0-or-later; the capture has no statement of its own.",
		Notes:    "A synthetic capture written for the dissector tests (commit message: generated, not taken from a network)."},
	{ID: srcESET, URL: "https://github.com/eset/malware-research/tree/8a3411529f80e85e48bef0f7c530c86df4c21cba/industroyer2",
		Revision: "8a3411529f80e85e48bef0f7c530c86df4c21cba",
		Terms:    "BSD-2-Clause (repository licence).",
		Notes:    "ESET Research's Industroyer2 playground: \"captured network traffic produced by Industroyer2 malware\"; IP addresses \"intentionally redacted\" (README.adoc)."},
	{ID: srcITI, URL: "https://github.com/ITI/ICS-Security-Tools/tree/bcb93ee1cdf395a457cc08f639cf29aab5975f01/pcaps/IEC60870-5-104",
		Revision: "bcb93ee1cdf395a457cc08f639cf29aab5975f01",
		Terms:    "CC-BY-4.0 (repository licence as GitHub reports it).",
		Notes:    "A collection of ICS captures; the directory also holds copies of the Wireshark issue 2840 and 3900 files (wireshark-issues/), committed once."},
	{ID: srcNDPI, URL: "https://github.com/ntop/nDPI/blob/0bcc7e82e65d27dcd02d997f2b45464613ea943e/tests/cfgs/default/pcap/IEC104.pcap",
		Revision: "0bcc7e82e65d27dcd02d997f2b45464613ea943e",
		Terms:    "LGPL-3.0 (repository licence).",
		Notes:    "Test capture of nDPI's IEC 104 detection; byte-identical to EmreEkin/ICS-Pcaps IEC60870-104/Logfile IEC104.pcap. nDPI's iec60780-5-104.pcap is the Wireshark issue 2840 file."},
	{ID: srcWSWiki, URL: "https://wiki.wireshark.org/SampleCaptures",
		Revision: "attachment as served on 2026-11-11 (uploads/__moin_import__/attachments/SampleCaptures/IEC104_SQ.pcapng)",
		Terms:    "Wireshark wiki sample capture; no licence is stated for the attachment.",
		Notes:    "Byte-identical to EmreEkin/ICS-Pcaps IEC60870-104/IEC1044.pcap."},
	{ID: srcWSIssue, URL: "https://gitlab.com/wireshark/wireshark/-/issues",
		Revision: "the attachments of issues 2840, 3900, 5889, 7035, 8849, 9596, 17209 and 18494 (migrated uploads, URL per file)",
		Terms:    "Attached to public Wireshark issues by their reporters; no licence is stated for the attachments.",
		Notes:    "Issue 17209 attached iec104S_test.7z (SHA256 ce1f3e6c0205f016632ce6a843526660bbb1981222811539d03a198873b8cad0) with two pcapng files, extracted unchanged, and two XML logs of a protocol analyzer, not committed. Issue 17130 (IEC 60870-5-7 over TLS 1.3, port 19998) is not used: the traffic is encrypted and the TLS keys are only in a pcapng comment, which go-iec60870-5's capture reader does not apply."},
	{ID: srcEmre, URL: "https://github.com/EmreEkin/ICS-Pcaps/tree/0ffbf023afb4e39373228b54851bb4a1f33a3ab8/IEC60870-104",
		Revision: "0ffbf023afb4e39373228b54851bb4a1f33a3ab8",
		Terms:    "The repository has no licence file and states no terms.",
		Notes:    "Captures of a client on 192.168.1.75 (ASUSTek) and a station on 192.168.1.200 (LCFC, a Lenovo laptop) or on one host's loopback, one per command type; the files are named after the type intended. 104pcap.pcap, IEC1044.pcap, Logfile IEC104.pcap and LocalPCAP/ are copies of other sources or of each other and are committed once."},
}

var manifestFiles = []entry{
	{Path: "certlv/unknown_unknown_interrogation-commands.pcap", Source: srcCERTLV, OriginalName: "testing/Traces/first/iec104.pcap",
		Evidence: "C", Basis: "Recorded on a network (Ethernet, 2013-07-04); the station's MAC prefix 00:16:D1 is registered to ZAT a.s., the client's 00:22:15 to ASUSTek; neither the software nor a device model is stated anywhere.",
		Systems:    []string{"controlling station 10.20.102.1 (unknown software)", "controlled station 10.20.100.108, common address 10 (unknown device)"},
		Operations: "TESTFR, STARTDT, two station interrogations (one answered by end of initialization first), single, double, regulating step, bitstring and set point commands (normalized, scaled, float) with confirmations, terminations and the spontaneous returns, with and without CP56Time2a",
		Privacy:    "private addresses only", Missing: "the controlled station's make, model and firmware"},
	{Path: "certlv/unknown_unknown_file-read.pcapng", Source: srcCERTLV, OriginalName: "testing/Traces/fifth/read.pcapng",
		Evidence: "D", Basis: "Recorded on the loopback interface (127.0.0.1 both ends, 2025-11-14): two programs on one host; which implementations is not stated.",
		Systems:    []string{"controlling station (unknown software)", "controlled station, common address 1 (unknown software)"},
		Operations: "STARTDT, clock synchronization and interrogations to the broadcast address, counter interrogation, three file downloads (11, 11 and 24568 octets, sections of 5000) with section and file acknowledgements, crossed TESTFR",
		Privacy:    "loopback only", Missing: "which implementations made it"},
	{Path: "certlv/unknown_unknown_file-write.pcapng", Source: srcCERTLV, OriginalName: "testing/Traces/fifth/write.pcapng",
		Evidence: "D", Basis: "Recorded on the loopback interface (2025-11-14): two programs on one host; which implementations is not stated.",
		Systems:    []string{"controlling station (unknown software)", "controlled station, common address 1 (unknown software)"},
		Operations: "STARTDT, end of initialization, clock synchronization, interrogations, three file uploads to the station (11, 11 and 24568 octets), crossed TESTFR",
		Privacy:    "loopback only", Missing: "which implementations made it"},
	{Path: "certlv/unknown_unknown_errors.pcapng", Source: srcCERTLV, OriginalName: "testing/Traces/sixth/errors.pcapng",
		Evidence: "D", Basis: "Recorded on the loopback interface (2025-12-25): two programs on one host; which implementations is not stated.",
		Systems:    []string{"controlling station (unknown software)", "controlled station, common address 1 (unknown software)"},
		Operations: "End of initialization, clock synchronization, an interrogation answered with parameters of measured values (P_ME_NA_1..P_ME_NC_1, cause 20), reads and parameter activations refused with \"unknown information object address\", one parameter confirmed",
		Privacy:    "loopback only", Missing: "which implementations made it"},
	{Path: "uowm/qtester104_iec-testserver_c-ci-na-1.pcap", Source: srcUOWM,
		OriginalName: "20200426_UOWM_IEC104_Dataset_c_ci_na_1.7z: 20200426_UOWM_IEC104_Dataset_c_ci_na_1/20200426_UOWM_IEC104_Dataset_c_ci_na_1_qtester/20200426_UOWM_IEC104_Dataset_c_ci_na_1_qtester_iec104_only.pcap",
		Evidence:     "D", Basis: "Software testbed (ReadMe): QTester104 against IEC TestServer on VirtualBox VMs (MAC prefix 08:00:27).",
		Systems:    []string{"QTester104 v2.1 on Windows 7, 192.168.1.20", "IEC TestServer on Windows 7, seven stations (VirtualBox)"},
		Operations: "seven conversations of about 3.5 hours: station interrogations, TESTFR both ways, spontaneous single points, integrated totals the station reports periodically",
		Privacy:    "private testbed addresses only", Missing: "-"},
	{Path: "uowm/qtester104-attacker_iec-testserver_c-ci-na-1.pcap", Source: srcUOWM,
		OriginalName: "20200426_UOWM_IEC104_Dataset_c_ci_na_1.7z: .../20200426_UOWM_IEC104_Dataset_c_ci_na_1_iecserver1/20200426_UOWM_IEC104_Dataset_c_ci_na_1_iecserver1_iec104_only.pcap",
		Evidence:     "D", Basis: "Software testbed (ReadMe); station 192.168.1.21 is a VirtualBox VM (08:00:27:e8:42:e6, NetBIOS name vm2_RTU-PC in the full recording).",
		Systems:    []string{"IEC TestServer, 192.168.1.21", "QTester104, 192.168.1.20", "attacker1, Kali Linux, 192.168.1.28 (Metasploit iec104 module)"},
		Operations: "the station's view: QTester104's interrogations and the attacker's unauthorized counter interrogations (C_CI_NA_1 with qualifier 20), each on a connection of its own",
		Privacy:    "private testbed addresses only", Missing: "-"},
	{Path: "uowm/qtester104-attacker_iec-testserver_c-se-na-1.pcap", Source: srcUOWM,
		OriginalName: "20200427_UOWM_IEC104_Dataset_c_se_na_1.7z: .../20200427_UOWM_IEC104_Dataset_c_se_na_1_iecserver1_iec104_only.pcap",
		Evidence:     "D", Basis: "Software testbed (ReadMe); VirtualBox VMs.",
		Systems:    []string{"IEC TestServer, 192.168.1.21", "QTester104, 192.168.1.20", "attacker1, 192.168.1.28"},
		Operations: "the attacker's set point commands (C_SE_NA_1, some of them two octets short of the type's length), the station's confirmations, QTester104's interrogations",
		Privacy:    "private testbed addresses only", Missing: "-"},
	{Path: "uowm/qtester104-attacker_iec-testserver_c-sc-na-1.pcap", Source: srcUOWM,
		OriginalName: "20200428_UOWM_IEC104_Dataset_c_sc_na_1.7z: .../20200428_UOWM_IEC104_Dataset_c_sc_na_1_iecserver1_iec104_only.pcap",
		Evidence:     "D", Basis: "Software testbed (ReadMe); VirtualBox VMs.",
		Systems:    []string{"IEC TestServer, 192.168.1.21", "QTester104, 192.168.1.20", "attacker1, 192.168.1.28"},
		Operations: "the attacker's single commands (C_SC_NA_1), confirmations and terminations, QTester104's interrogations",
		Privacy:    "private testbed addresses only", Missing: "-"},
	{Path: "uowm/qtester104-attacker_iec-testserver_c-rd-na-1.pcap", Source: srcUOWM,
		OriginalName: "20200605_UOWM_IEC104_Dataset_c_rd_na_1.7z: .../20200605_UOWM_IEC104_Dataset_c_rd_na_1_iecserver1_iec104_only.pcap",
		Evidence:     "D", Basis: "Software testbed (ReadMe); VirtualBox VMs.",
		Systems:    []string{"IEC TestServer, 192.168.1.21", "QTester104, 192.168.1.20", "attacker1, 192.168.1.28"},
		Operations: "the attacker's read commands (C_RD_NA_1 with cause activation and one octet too many), the station's negative confirmations, QTester104's interrogations",
		Privacy:    "private testbed addresses only", Missing: "-"},
	{Path: "uowm/qtester104-attacker_iec-testserver_c-rp-na-1.pcap", Source: srcUOWM,
		OriginalName: "20200606_UOWM_IEC104_Dataset_c_rp_na_1.7z: .../20200606_UOWM_IEC104_Dataset_c_rp_na_1_iecserver1_iec104_only.pcap",
		Evidence:     "D", Basis: "Software testbed (ReadMe); VirtualBox VMs.",
		Systems:    []string{"IEC TestServer, 192.168.1.21", "QTester104, 192.168.1.20", "attacker1, 192.168.1.28"},
		Operations: "the attacker's reset process commands (C_RP_NA_1), the station's negative confirmations without the qualifier octet, sent on every connection",
		Privacy:    "private testbed addresses only", Missing: "-"},
	{Path: "uowm/qtester104-attacker_iec-testserver_m-sp-na-1-dos.pcap", Source: srcUOWM,
		OriginalName: "20200425_UOWM_IEC104_Dataset_m_sp_na_1_DoS.7z: .../20200425_UOWM_IEC104_Dataset_m_sp_na_1_DoS_iecserver1_iec104_only.pcap",
		Evidence:     "D", Basis: "Software testbed (ReadMe); VirtualBox VMs.",
		Systems:    []string{"IEC TestServer, 192.168.1.21", "QTester104, 192.168.1.20"},
		Operations: "32,697 spontaneous single points from the station to QTester104 in three hours, interrogations",
		Privacy:    "private testbed addresses only", Missing: "-"},
	{Path: "wireshark/synthetic_synthetic_iec104-coverage.pcap.gz", Source: srcWireshk, OriginalName: "test/captures/iec104_coverage.pcap.gz",
		Evidence: "E", Basis: "Generated for Wireshark's dissector tests, not taken from a network (commit message).",
		Systems:    []string{"none: generated"},
		Operations: "every defined type identification with one, two and sequence-numbered objects, all causes, U/S/I frames, APDUs sharing a segment and split over two, noise before a start octet, IEC 60870-5-7 security ASDUs with placeholder data, IEC 101/103 frames on other ports",
		Privacy:    "placeholder data", Missing: "it is not traffic of any implementation"},
	{Path: "eset/industroyer2_unknown_commands-sample-1.pcap", Source: srcESET, OriginalName: "industroyer2/Industroyer2_Sample1.pcap", URL: "https://github.com/eset/malware-research/blob/8a3411529f80e85e48bef0f7c530c86df4c21cba/industroyer2/Industroyer2_Sample1.pcap",
		Evidence: "D", Basis: "Recorded by ESET Research between VMware virtual machines (MAC prefixes 00:0C:29, 00:50:56): Industroyer2 against stations that are not identified.",
		Systems:    []string{"Industroyer2 malware, 10.9.9.9 (redacted address)", "controlled stations 10.1.1.1 and 10.1.1.3 (not identified)"},
		Operations: "the malware's sequence: STARTDT, station interrogation, then single and double commands (C_SC_NA_1, C_DC_NA_1) to its configured IOAs with confirmations and terminations",
		Privacy:    "addresses redacted by ESET", Missing: "the stations' software"},
	{Path: "eset/industroyer2_unknown_commands-sample-2.pcap", Source: srcESET, OriginalName: "industroyer2/Industroyer2_Sample2.pcap", URL: "https://github.com/eset/malware-research/blob/8a3411529f80e85e48bef0f7c530c86df4c21cba/industroyer2/Industroyer2_Sample2.pcap",
		Evidence: "D", Basis: "Recorded by ESET Research between VMware virtual machines (MAC prefixes 00:0C:29, 00:50:56): Industroyer2 against stations that are not identified.",
		Systems:    []string{"Industroyer2 malware, 10.9.9.9 (redacted address)", "controlled stations 10.1.1.1 and 10.1.1.3 (not identified)"},
		Operations: "the malware's sequence: STARTDT, station interrogation, then single and double commands (C_SC_NA_1, C_DC_NA_1) to its configured IOAs with confirmations and terminations",
		Privacy:    "addresses redacted by ESET", Missing: "the stations' software"},
	{Path: "iti/unknown_unknown_javarmi-and-iec-misc.pcap", Source: srcITI, OriginalName: "pcaps/IEC60870-5-104/JavaRMI_and_IEC_Misc.pcap", URL: "https://github.com/ITI/ICS-Security-Tools/blob/bcb93ee1cdf395a457cc08f639cf29aab5975f01/pcaps/IEC60870-5-104/JavaRMI_and_IEC_Misc.pcap",
		Evidence: "C", Basis: "Recorded on a network (Dell, CommScope and Dimat MAC prefixes); the collection states nothing about the systems.",
		Systems:    []string{"controlling stations 192.168.1.44 and 192.168.1.113 (not identified)", "controlled station 10.209.13.145, common address 37133 (not identified)"},
		Operations: "repeated station interrogations with their answers (M_SP_NA_1, M_ST_NA_1, M_ME_NB_1 and others), mixed with Java RMI traffic on port 1099",
		Privacy:    "private addresses only", Missing: "the station's make, model and software"},
	{Path: "ndpi/unknown_unknown_m-me-tf-1.pcap", Source: srcNDPI, OriginalName: "tests/cfgs/default/pcap/IEC104.pcap", URL: "https://github.com/ntop/nDPI/blob/0bcc7e82e65d27dcd02d997f2b45464613ea943e/tests/cfgs/default/pcap/IEC104.pcap",
		Evidence: "C", Basis: "Recorded on a network (Cisco router and Dell MAC prefixes); not documented.",
		Systems:    []string{"controlling station 10.119.105.26 (not identified)", "controlled stations 10.175.211.1 and .3 (not identified)"},
		Operations: "spontaneous M_ME_TF_1, recorded without the connections' start",
		Privacy:    "private addresses only", Missing: "the station's make, model and software"},
	{Path: "wireshark-wiki/unknown_unknown_m-sp-na-1-sequence.pcapng", Source: srcWSWiki, OriginalName: "SampleCaptures/IEC104_SQ.pcapng", URL: "https://wiki.wireshark.org/uploads/__moin_import__/attachments/SampleCaptures/IEC104_SQ.pcapng",
		Evidence: "C", Basis: "One APDU recorded on a network (Cisco MAC prefix); not documented.",
		Systems:    []string{"controlled station 10.204.70.90 (not identified)"},
		Operations: "one APDU with four M_SP_NA_1 ASDUs answering an interrogation (sequences of objects)",
		Privacy:    "private addresses only", Missing: "the station's make, model and software"},
	{Path: "wireshark-issues/unknown_unknown_bad-timestamps.pcap", Source: srcWSIssue, OriginalName: "issue 5889: badtimestamps.pcap", URL: "https://gitlab.com/-/project/7898047/uploads/3b5d70cc864bdc546565827e4ab941fa/badtimestamps.pcap",
		Evidence: "C", Basis: "Recorded on a network: the station's MAC prefix is Portwell's (industrial computers), the controlling station's ABB's; the issue names no device.",
		Systems:    []string{"controlling station 192.168.0.10 (ABB MAC prefix)", "controlled station 192.168.0.11 (Portwell MAC prefix)"},
		Operations: "spontaneous M_ME_NC_1, M_DP_TB_1 and a clock synchronization, recorded without the connection's start",
		Privacy:    "private addresses only", Missing: "the station's make, model and software"},
	{Path: "wireshark-issues/unknown_unknown_diverse-signals-loopback.pcap", Source: srcWSIssue, OriginalName: "issue 3900: 090813_diverse.pcap", URL: "https://gitlab.com/-/project/7898047/uploads/c9f9841aa34bc15787cfac3ff6b0e51a/090813_diverse.pcap",
		Evidence: "D", Basis: "Both ends on 10.0.0.10 behind the Windows loopback adapter (MAC 02:00:4C:4F:4F:50): two programs on one host. Also in ITI/ICS-Security-Tools and EmreEkin/ICS-Pcaps (104pcap.pcap).",
		Systems:    []string{"controlling station and controlled station on one host (not identified)"},
		Operations: "monitor values of many types and commands with CP56Time2a whose year is written as years since 1900 (109)",
		Privacy:    "private addresses only", Missing: "the station's make, model and software"},
	{Path: "wireshark-issues/unknown_unknown_dissector-test-with-errors.pcap", Source: srcWSIssue, OriginalName: "issue 2840: TestDissectIec104.pcap", URL: "https://gitlab.com/-/project/7898047/uploads/ef80a24b453793290cb4812b6000ff91/TestDissectIec104.pcap",
		Evidence: "E", Basis: "The reporter: \"First 100 packets have intentionated errors. I fuzzy tested with this file.\" Edited for the dissector, so not a recording as it was. Also in ITI/ICS-Security-Tools and nDPI.",
		Systems:    []string{"controlling station 172.27.248.109", "controlled station 172.27.248.79"},
		Operations: "malformed APDUs and ASDUs (edited), then commands, clock synchronization and interrogation with their answers",
		Privacy:    "private addresses only", Missing: "the unedited recording"},
	{Path: "wireshark-issues/unknown_unknown_interrogation-sample.pcap", Source: srcWSIssue, OriginalName: "issue 8849: IEC104_Sample.pcap", URL: "https://gitlab.com/-/project/7898047/uploads/ec18bf6333942ace0b010d68b177588b/IEC104_Sample.pcap",
		Evidence: "C", Basis: "Recorded on Ethernet; the same station (10.20.100.108, MAC prefix of ZAT a.s.) and controlling station (ASUSTek) as certlv/unknown_unknown_interrogation-commands.pcap; no model or software stated.",
		Systems:    []string{"controlling station 10.20.102.1", "controlled station 10.20.100.108, common address 10"},
		Operations: "STARTDT, a station interrogation with end of initialization and answers in one APDU stream",
		Privacy:    "private addresses only", Missing: "the station's make, model and software"},
	{Path: "wireshark-issues/synthetic_synthetic_normalized-values.pcapng", Source: srcWSIssue, OriginalName: "issue 8849: NormVal.pcapng", URL: "https://gitlab.com/-/project/7898047/uploads/60b243fc0504c08fc4d51d77f0f42275/NormVal.pcapng",
		Evidence: "E", Basis: "Destination MAC 00:10:00:00:00:00-style placeholders (Sytek_00:00:00) and only the station's frames: written for the dissector, not recorded as is.",
		Systems:    []string{"controlled station 10.20.100.108 (placeholder link layer)"},
		Operations: "M_ME_NA_1 and M_ME_TD_1",
		Privacy:    "private addresses only", Missing: "how it was made"},
	{Path: "wireshark-issues/synthetic_synthetic_step-positions.pcapng", Source: srcWSIssue, OriginalName: "issue 8849: Position_All.pcapng", URL: "https://gitlab.com/-/project/7898047/uploads/949f5583c27c823713d496bb96a15ab2/Position_All.pcapng",
		Evidence: "E", Basis: "Placeholder link layer as NormVal.pcapng: written for the dissector.",
		Systems:    []string{"controlled station 10.20.100.108 (placeholder link layer)"},
		Operations: "127 M_ST_NA_1 with every step position",
		Privacy:    "private addresses only", Missing: "how it was made"},
	{Path: "wireshark-issues/unknown_unknown_step-position.pcap", Source: srcWSIssue, OriginalName: "issue 8849: StepPosition.pcap", URL: "https://gitlab.com/-/project/7898047/uploads/907e418c74dc739e78576ad8e18c4710/StepPosition.pcap",
		Evidence: "C", Basis: "One APDU of the station with the ZAT a.s. MAC prefix.",
		Systems:    []string{"controlled station 10.20.100.108"},
		Operations: "one M_ST_NA_1",
		Privacy:    "private addresses only", Missing: "the station's make, model and software"},
	{Path: "wireshark-issues/unknown_unknown_m-sp-tb-1-time-tag.pcap", Source: srcWSIssue, OriginalName: "issue 8849: TimeTag.pcap", URL: "https://gitlab.com/-/project/7898047/uploads/345518bc2397d59c98ab3f8d02133114/TimeTag.pcap",
		Evidence: "C", Basis: "One APDU recorded on a network (Cisco MAC prefix).",
		Systems:    []string{"controlled station 10.206.60.140"},
		Operations: "one M_SP_TB_1",
		Privacy:    "private addresses only", Missing: "the station's make, model and software"},
	{Path: "wireshark-issues/unknown_unknown_counters.pcapng", Source: srcWSIssue, OriginalName: "issue 9596: IEC104_Counters.pcapng", URL: "https://gitlab.com/-/project/7898047/uploads/312808f88b29c154b8c452c2fff1aa72/IEC104_Counters.pcapng",
		Evidence: "C", Basis: "Recorded on a network: one station with a Microchip MAC prefix (an embedded TCP/IP stack), the other end an Intel PC; not documented.",
		Systems:    []string{"controlling station 192.168.112.125", "controlled station 192.168.120.5"},
		Operations: "a counter interrogation and integrated totals M_IT_TB_1",
		Privacy:    "private addresses only", Missing: "the station's make, model and software"},
	{Path: "wireshark-issues/unknown_unknown_bitstring-32.pcapng", Source: srcWSIssue, OriginalName: "issue 9596: IEC870-104_32bitstring.pcapng", URL: "https://gitlab.com/-/project/7898047/uploads/61ebe3af9ea5f35bb8b1446908de93c1/IEC870-104_32bitstring.pcapng",
		Evidence: "C", Basis: "As IEC104_Counters.pcapng.",
		Systems:    []string{"controlling station 192.168.112.125", "controlled station 192.168.120.5"},
		Operations: "an interrogation answered with M_BO_NA_1 and M_BO_TB_1",
		Privacy:    "private addresses only", Missing: "the station's make, model and software"},
	{Path: "wireshark-issues/unknown_unknown_general-interrogation.pcapng", Source: srcWSIssue, OriginalName: "issue 9596: IEC870-104_GeneralInterrogation.pcapng", URL: "https://gitlab.com/-/project/7898047/uploads/ed222ce768c71efcad4699a0a9b1470f/IEC870-104_GeneralInterrogation.pcapng",
		Evidence: "C", Basis: "As IEC104_Counters.pcapng; here the Microchip-prefixed station is the controlling one.",
		Systems:    []string{"controlling station 192.168.120.5", "controlled station 192.168.112.124"},
		Operations: "an interrogation answered with several monitor types, integrated totals with cause 37",
		Privacy:    "private addresses only", Missing: "the station's make, model and software"},
	{Path: "wireshark-issues/unknown_unknown_m-sp-na-1-index-error.pcap", Source: srcWSIssue, OriginalName: "issue 7035: NumIxError.pcap", URL: "https://gitlab.com/-/project/7898047/uploads/f18d36f6850e6f00354a373c9680d98b/NumIxError.pcap",
		Evidence: "C", Basis: "One APDU recorded on a network (Cisco MAC prefix).",
		Systems:    []string{"controlled station 192.168.13.9"},
		Operations: "one M_SP_NA_1 whose number of objects does not match its length (the issue was closed as not a bug)",
		Privacy:    "private addresses only", Missing: "the station's make, model and software"},
	{Path: "wireshark-issues/synthetic_synthetic_cp56time-utc.pcap", Source: srcWSIssue, OriginalName: "issue 18494: iec104-wrong-ts.pcap", URL: "https://gitlab.com/-/project/7898047/uploads/cb540431a19594af5ca142fe9159ed57/iec104-wrong-ts.pcap",
		Evidence: "E", Basis: "One APDU with placeholder MAC addresses (00:00:00:00:00:02): written to show a display bug.",
		Systems:    []string{"controlled station 10.0.0.1 (placeholder)"},
		Operations: "one M_SP_TB_1",
		Privacy:    "placeholder addresses", Missing: "-"},
	{Path: "wireshark-issues/simulator_mio8_spontaneous-not-soe.pcapng", Source: srcWSIssue, OriginalName: "issue 17209: iec104S_test.7z: iec104S_test/MIO_notsoe.pcapng", URL: "https://gitlab.com/-/project/7898047/uploads/6e1419390a9a5e6d2b7dadf822e3df52/iec104S_test.7z",
		Evidence: "B", Basis: "The reporter describes an RTU polled by two test simulators (Triangle MicroWorks Test Harness and ASE2000) and names it MIO8 in the analyzer logs of the same archive; the station's MAC prefix is not a virtual machine's. Which simulator made this file is not stated.",
		Systems:    []string{"test simulator (TMW Test Harness or ASE2000), 192.168.1.253", "MIO8 RTU, 192.168.1.1"},
		Operations: "spontaneous M_SP_TB_1 (the issue's subject: the substituted bit)",
		Privacy:    "private addresses only", Missing: "the RTU's make, firmware and configuration"},
	{Path: "wireshark-issues/simulator_mio8_spontaneous-soe.pcapng", Source: srcWSIssue, OriginalName: "issue 17209: iec104S_test.7z: iec104S_test/MIO_soe.pcapng", URL: "https://gitlab.com/-/project/7898047/uploads/6e1419390a9a5e6d2b7dadf822e3df52/iec104S_test.7z",
		Evidence: "B", Basis: "The reporter describes an RTU polled by two test simulators (Triangle MicroWorks Test Harness and ASE2000) and names it MIO8 in the analyzer logs of the same archive; the station's MAC prefix is not a virtual machine's. Which simulator made this file is not stated.",
		Systems:    []string{"test simulator (TMW Test Harness or ASE2000), 192.168.1.253", "MIO8 RTU, 192.168.1.1"},
		Operations: "spontaneous M_SP_TB_1 with and without the substituted bit (the issue's subject), clock synchronization, interrogation and counter interrogation",
		Privacy:    "private addresses only", Missing: "the RTU's make, firmware and configuration"},
	{Path: "emreekin/unknown_unknown_p-me-nc-1-ip-value.pcap", Source: srcEmre, OriginalName: "IEC60870-104/104IPValue.pcap", URL: "https://github.com/EmreEkin/ICS-Pcaps/blob/0ffbf023afb4e39373228b54851bb4a1f33a3ab8/IEC60870-104/104IPValue.pcap",
		Evidence: "D", Basis: "A client on a laptop (ASUSTek MAC prefix) and a station on a laptop (LCFC, Lenovo): software stations, not named.",
		Systems:    []string{"controlling station 192.168.1.75", "controlled station 192.168.1.200 (software on a laptop)"},
		Operations: "parameter activation P_ME_NC_1 and C_SE_NC_1 with confirmations",
		Privacy:    "private addresses only", Missing: "the programs"},
	{Path: "emreekin/unknown_unknown_local-value-loopback.pcap", Source: srcEmre, OriginalName: "IEC60870-104/104LocalValue.pcap", URL: "https://github.com/EmreEkin/ICS-Pcaps/blob/0ffbf023afb4e39373228b54851bb4a1f33a3ab8/IEC60870-104/104LocalValue.pcap",
		Evidence: "D", Basis: "Recorded on one host's loopback interface: two programs, not named.",
		Systems:    []string{"controlling and controlled station on 127.0.0.1 (not named)"},
		Operations: "C_SE_NC_1 and monitor values on one host's loopback",
		Privacy:    "loopback only", Missing: "the programs"},
	{Path: "emreekin/unknown_unknown_deneme.pcap", Source: srcEmre, OriginalName: "IEC60870-104/104deneme.pcap", URL: "https://github.com/EmreEkin/ICS-Pcaps/blob/0ffbf023afb4e39373228b54851bb4a1f33a3ab8/IEC60870-104/104deneme.pcap",
		Evidence: "D", Basis: "A client on a laptop (ASUSTek MAC prefix) and a station on a laptop (LCFC, Lenovo): software stations, not named.",
		Systems:    []string{"controlling station 192.168.1.75", "controlled station 192.168.1.200 (software on a laptop)"},
		Operations: "commands with time tag C_SE_TC_1, parameters P_ME_NC_1, monitor values",
		Privacy:    "private addresses only", Missing: "the programs"},
	{Path: "emreekin/unknown_unknown_deneme-2.pcap", Source: srcEmre, OriginalName: "IEC60870-104/104deneme2.pcap", URL: "https://github.com/EmreEkin/ICS-Pcaps/blob/0ffbf023afb4e39373228b54851bb4a1f33a3ab8/IEC60870-104/104deneme2.pcap",
		Evidence: "D", Basis: "A client on a laptop (ASUSTek MAC prefix) and a station on a laptop (LCFC, Lenovo): software stations, not named.",
		Systems:    []string{"controlling station 192.168.1.75", "controlled station 192.168.1.200 (software on a laptop)"},
		Operations: "M_IT_TB_1, M_BO_NA_1, parameter activations",
		Privacy:    "private addresses only", Missing: "the programs"},
	{Path: "emreekin/unknown_unknown_m-bo-tb-1.pcap", Source: srcEmre, OriginalName: "IEC60870-104/33.pcap", URL: "https://github.com/EmreEkin/ICS-Pcaps/blob/0ffbf023afb4e39373228b54851bb4a1f33a3ab8/IEC60870-104/33.pcap",
		Evidence: "D", Basis: "A client on a laptop (ASUSTek MAC prefix) and a station on a laptop (LCFC, Lenovo): software stations, not named.",
		Systems:    []string{"controlling station 192.168.1.75", "controlled station 192.168.1.200 (software on a laptop)"},
		Operations: "M_BO_TB_1",
		Privacy:    "private addresses only", Missing: "the programs"},
	{Path: "emreekin/unknown_unknown_c-se-nc-1.pcap", Source: srcEmre, OriginalName: "IEC60870-104/50.pcap", URL: "https://github.com/EmreEkin/ICS-Pcaps/blob/0ffbf023afb4e39373228b54851bb4a1f33a3ab8/IEC60870-104/50.pcap",
		Evidence: "D", Basis: "A client on a laptop (ASUSTek MAC prefix) and a station on a laptop (LCFC, Lenovo): software stations, not named.",
		Systems:    []string{"controlling station 192.168.1.75", "controlled station 192.168.1.200 (software on a laptop)"},
		Operations: "C_SE_NC_1 with confirmations and terminations",
		Privacy:    "private addresses only", Missing: "the programs"},
	{Path: "emreekin/unknown_unknown_c-sc-ta-1-c-dc-ta-1.pcap", Source: srcEmre, OriginalName: "IEC60870-104/58_59.pcap", URL: "https://github.com/EmreEkin/ICS-Pcaps/blob/0ffbf023afb4e39373228b54851bb4a1f33a3ab8/IEC60870-104/58_59.pcap",
		Evidence: "D", Basis: "A client on a laptop (ASUSTek MAC prefix) and a station on a laptop (LCFC, Lenovo): software stations, not named.",
		Systems:    []string{"controlling station 192.168.1.75", "controlled station 192.168.1.200 (software on a laptop)"},
		Operations: "C_SC_TA_1 and C_DC_TA_1, select and execute",
		Privacy:    "private addresses only", Missing: "the programs"},
	{Path: "emreekin/unknown_unknown_c-dc-na-1.pcap", Source: srcEmre, OriginalName: "IEC60870-104/59.pcap", URL: "https://github.com/EmreEkin/ICS-Pcaps/blob/0ffbf023afb4e39373228b54851bb4a1f33a3ab8/IEC60870-104/59.pcap",
		Evidence: "D", Basis: "A client on a laptop (ASUSTek MAC prefix) and a station on a laptop (LCFC, Lenovo): software stations, not named.",
		Systems:    []string{"controlling station 192.168.1.75", "controlled station 192.168.1.200 (software on a laptop)"},
		Operations: "C_DC_NA_1 (the file name says 59, the content is type 46)",
		Privacy:    "private addresses only", Missing: "the programs"},
	{Path: "emreekin/unknown_unknown_c-rc-ta-1.pcap", Source: srcEmre, OriginalName: "IEC60870-104/60.pcap", URL: "https://github.com/EmreEkin/ICS-Pcaps/blob/0ffbf023afb4e39373228b54851bb4a1f33a3ab8/IEC60870-104/60.pcap",
		Evidence: "D", Basis: "A client on a laptop (ASUSTek MAC prefix) and a station on a laptop (LCFC, Lenovo): software stations, not named.",
		Systems:    []string{"controlling station 192.168.1.75", "controlled station 192.168.1.200 (software on a laptop)"},
		Operations: "C_RC_TA_1 with confirmations and terminations",
		Privacy:    "private addresses only", Missing: "the programs"},
	{Path: "emreekin/unknown_unknown_c-se-ta-1.pcap", Source: srcEmre, OriginalName: "IEC60870-104/61.pcap", URL: "https://github.com/EmreEkin/ICS-Pcaps/blob/0ffbf023afb4e39373228b54851bb4a1f33a3ab8/IEC60870-104/61.pcap",
		Evidence: "D", Basis: "A client on a laptop (ASUSTek MAC prefix) and a station on a laptop (LCFC, Lenovo): software stations, not named.",
		Systems:    []string{"controlling station 192.168.1.75", "controlled station 192.168.1.200 (software on a laptop)"},
		Operations: "C_SE_TA_1",
		Privacy:    "private addresses only", Missing: "the programs"},
	{Path: "emreekin/unknown_unknown_c-se-tb-1.pcap", Source: srcEmre, OriginalName: "IEC60870-104/62.pcap", URL: "https://github.com/EmreEkin/ICS-Pcaps/blob/0ffbf023afb4e39373228b54851bb4a1f33a3ab8/IEC60870-104/62.pcap",
		Evidence: "D", Basis: "A client on a laptop (ASUSTek MAC prefix) and a station on a laptop (LCFC, Lenovo): software stations, not named.",
		Systems:    []string{"controlling station 192.168.1.75", "controlled station 192.168.1.200 (software on a laptop)"},
		Operations: "C_SE_TB_1",
		Privacy:    "private addresses only", Missing: "the programs"},
	{Path: "emreekin/unknown_unknown_c-se-tc-1.pcap", Source: srcEmre, OriginalName: "IEC60870-104/63.pcap", URL: "https://github.com/EmreEkin/ICS-Pcaps/blob/0ffbf023afb4e39373228b54851bb4a1f33a3ab8/IEC60870-104/63.pcap",
		Evidence: "D", Basis: "A client on a laptop (ASUSTek MAC prefix) and a station on a laptop (LCFC, Lenovo): software stations, not named.",
		Systems:    []string{"controlling station 192.168.1.75", "controlled station 192.168.1.200 (software on a laptop)"},
		Operations: "C_SE_TC_1",
		Privacy:    "private addresses only", Missing: "the programs"},
	{Path: "emreekin/unknown_unknown_c-se-tc-1-2.pcap", Source: srcEmre, OriginalName: "IEC60870-104/63_1.pcap", URL: "https://github.com/EmreEkin/ICS-Pcaps/blob/0ffbf023afb4e39373228b54851bb4a1f33a3ab8/IEC60870-104/63_1.pcap",
		Evidence: "D", Basis: "A client on a laptop (ASUSTek MAC prefix) and a station on a laptop (LCFC, Lenovo): software stations, not named.",
		Systems:    []string{"controlling station 192.168.1.75", "controlled station 192.168.1.200 (software on a laptop)"},
		Operations: "C_SE_TC_1, a second run",
		Privacy:    "private addresses only", Missing: "the programs"},
	{Path: "emreekin/unknown_unknown_c-bo-ta-1.pcap", Source: srcEmre, OriginalName: "IEC60870-104/64.pcap", URL: "https://github.com/EmreEkin/ICS-Pcaps/blob/0ffbf023afb4e39373228b54851bb4a1f33a3ab8/IEC60870-104/64.pcap",
		Evidence: "D", Basis: "A client on a laptop (ASUSTek MAC prefix) and a station on a laptop (LCFC, Lenovo): software stations, not named.",
		Systems:    []string{"controlling station 192.168.1.75", "controlled station 192.168.1.200 (software on a laptop)"},
		Operations: "C_BO_TA_1",
		Privacy:    "private addresses only", Missing: "the programs"},
	{Path: "emreekin/unknown_unknown_vinci-loopback.pcap", Source: srcEmre, OriginalName: "LocalPCAP/vinci104.pcap", URL: "https://github.com/EmreEkin/ICS-Pcaps/blob/0ffbf023afb4e39373228b54851bb4a1f33a3ab8/LocalPCAP/vinci104.pcap",
		Evidence: "D", Basis: "Recorded on one host's loopback interface: two programs, not named.",
		Systems:    []string{"controlling and controlled station on 127.0.0.1 (not named)"},
		Operations: "commands with time tag of types 58 to 64 on one host's loopback",
		Privacy:    "loopback only", Missing: "the programs"},
	{Path: "emreekin/unknown_unknown_baselines.pcap", Source: srcEmre, OriginalName: "IEC60870-104/iec104_baselines.pcap", URL: "https://github.com/EmreEkin/ICS-Pcaps/blob/0ffbf023afb4e39373228b54851bb4a1f33a3ab8/IEC60870-104/iec104_baselines.pcap",
		Evidence: "C", Basis: "The station (10.20.100.108, ZAT a.s. MAC prefix) and controlling station of certlv/unknown_unknown_interrogation-commands.pcap; how it came to this collection is not stated.",
		Systems:    []string{"controlling station 10.20.102.1", "controlled station 10.20.100.108"},
		Operations: "STARTDT and commands of types 45 to 51 with cause activation",
		Privacy:    "private addresses only", Missing: "the station's make, model and software"},
}

const manifestPath = "../../testdata/captures/manifest.json"

// derive computes the counts of one capture.
func derive(t *testing.T, path string) counts {
	t.Helper()
	convs, err := ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var c counts
	types := map[asdu.TypeID]bool{}
	for _, cv := range convs {
		a := Analyze(cv, asdu.IEC104)
		c.Conversations++
		if a.FromZero {
			c.FromZero++
		}
		c.APDUs += len(cv.APDUs)
		c.I, c.S, c.U = c.I+a.I, c.S+a.S, c.U+a.U
		c.Retransmitted += cv.Retransmitted
		c.OutOfOrder += cv.OutOfOrder
		c.Gaps += cv.Gaps
		c.Split += cv.Split
		c.Shared += cv.Shared
		c.Skipped += cv.Skipped
		c.StoppedI += len(a.StoppedI)
		for _, s := range a.Seq {
			c.SeqErrors += len(s.SendErrors) + len(s.RecvErrors)
		}
		for _, d := range a.Decoded {
			switch {
			case d.APCIErr != nil:
				c.APCIErrors++
			case d.ASDUErr != nil:
				c.ASDUErrors++
			case d.Exact():
				c.Exact++
			}
			if d.ASDU != nil {
				types[d.ASDU.Type] = true
			}
		}
	}
	c.Types = len(types)
	return c
}

func buildManifest(t *testing.T) manifest {
	t.Helper()
	m := manifest{
		Description: "Recorded IEC 60870-5-104 traffic go-iec60870-5 is tested against (testdata/captures/README.md). Files are byte-identical to their sources.",
		Levels: map[string]string{
			"A": "verified physical-device traffic",
			"B": "credibly documented physical-device traffic",
			"C": "independent recorded traffic, hardware origin unknown",
			"D": "simulated or software testbed",
			"E": "standards-derived examples or generated fixtures",
		},
		Sources: manifestSources,
	}
	for _, e := range manifestFiles {
		p := filepath.Join("..", "..", "testdata", "captures", filepath.FromSlash(e.Path))
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(b)
		e.Size, e.SHA256 = len(b), hex.EncodeToString(sum[:])
		for _, s := range manifestSources {
			if s.ID == e.Source && e.URL == "" {
				e.URL = s.URL
			}
		}
		e.Format = format(t, e.Path, b)
		e.Recorded = recordedSpan(t, b)
		e.Counts = derive(t, p)
		m.Files = append(m.Files, e)
	}
	sort.Slice(m.Files, func(i, j int) bool { return m.Files[i].Path < m.Files[j].Path })
	return m
}

// recordedSpan returns the first and last time stamp of a capture.
func recordedSpan(t *testing.T, b []byte) string {
	t.Helper()
	ps, err := pcapio.Packets(b)
	if err != nil || len(ps) == 0 {
		t.Fatal(err)
	}
	first, last := pcapio.Span(ps)
	return first.Format("2006-01-02 15:04:05Z07:00") + " .. " + last.Format("2006-01-02 15:04:05Z07:00")
}

func TestManifest(t *testing.T) {
	m := buildManifest(t)
	got, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	if os.Getenv("IEC104_CAPTURE_GENERATE") != "" {
		if err := os.WriteFile(manifestPath, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Error("testdata/captures/manifest.json is not what the captures and the code give: run make captures-manifest and review the difference")
	}
	if len(committed(t)) != len(manifestFiles) {
		t.Errorf("%d captures committed, %d in the manifest", len(committed(t)), len(manifestFiles))
	}
}

// TestGenerateFuzzSeeds writes fuzz seeds for apci.FuzzParse and
// asdu.FuzzDecode from the recorded (not generated) captures: one APDU, and one ASDU, of
// every distinct shape (format, type identification, cause and P/N and T
// bits, decodes or not). It runs with IEC104_CAPTURE_GENERATE=1.
func TestGenerateFuzzSeeds(t *testing.T) {
	if os.Getenv("IEC104_CAPTURE_GENERATE") == "" {
		t.Skip("set IEC104_CAPTURE_GENERATE=1 to rewrite the fuzz seeds taken from the captures")
	}
	write := func(dir string, b []byte) {
		sum := sha256.Sum256(b)
		name := filepath.Join(dir, "capture-"+hex.EncodeToString(sum[:8]))
		body := "go test fuzz v1\n[]byte(" + strconvQuote(b) + ")\n"
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	apciDir := filepath.Join("..", "..", "apci", "testdata", "fuzz", "FuzzParse")
	asduDir := filepath.Join("..", "..", "asdu", "testdata", "fuzz", "FuzzDecode")
	seen := map[string]bool{}
	for _, f := range committed(t) {
		if strings.Contains(f, "synthetic") {
			continue // generated: the codec tests cover its shapes
		}
		convs, err := ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range convs {
			for _, d := range Analyze(c, asdu.IEC104).Decoded {
				key := []byte{byte(d.Frame.Format)}
				if d.APCIErr != nil {
					key = append(key, 0xff)
				}
				if len(d.Frame.ASDU) >= 3 {
					key = append(key, d.Frame.ASDU[0], d.Frame.ASDU[2])
				}
				if d.ASDUErr != nil {
					key = append(key, 0xee)
				}
				if seen[string(key)] {
					continue
				}
				seen[string(key)] = true
				write(apciDir, d.Data)
				if d.APCIErr == nil && d.Frame.Format == 0 {
					write(asduDir, d.Frame.ASDU)
				}
			}
		}
	}
}

// strconvQuote quotes b the way the fuzz corpus files do.
func strconvQuote(b []byte) string { return strconv.Quote(string(b)) }

// format describes the container and link layer of a capture.
func format(t *testing.T, path string, b []byte) string {
	t.Helper()
	ps, err := pcapio.Packets(b)
	if err != nil || len(ps) == 0 {
		t.Fatalf("%s: %v", path, err)
	}
	c := "pcap"
	if strings.Contains(path, ".pcapng") {
		c = "pcapng"
	}
	if strings.HasSuffix(path, ".gz") {
		c += " (gzip)"
	}
	l := map[uint32]string{pcapio.LinkNull: "BSD loopback (link type 0)", pcapio.LinkEthernet: "Ethernet"}[ps[0].Link]
	if l == "" {
		l = fmt.Sprintf("link type %d", ps[0].Link)
	}
	return c + ", " + l + ", IPv4, TCP 2404"
}
