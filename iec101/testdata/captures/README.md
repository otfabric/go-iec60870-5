# Recorded IEC 60870-5-101 traffic

Captures of IEC 60870-5-101 lines recorded by others, committed
byte-identical to their sources (checked by [SHA256SUMS](SHA256SUMS),
`make captures-verify`, also a CI job). Each file's origin, terms, field
sizes and what the tests derive from it are in
[manifest.json](manifest.json), which `TestManifest` regenerates and checks
(`make captures-manifest`). This is evidence from recorded traffic, not a
test against a live system and not a conformance claim.

## What the tests do

`iec101/internal/capture` (with `internal/pcapio` and `internal/serialcap`)
reads the SEL RTAC serial-line captures and the TCP streams of serial port
servers, and for every frame of every capture:

- splits the octets into FT 1.2 frames and checks start, length, checksum
  and stop characters: **no octet of any committed capture is undecodable**;
- decodes the control field and checks the link procedures per direction
  and link address: FCV per function, FCB toggled for every new message and
  kept for a repetition, FCB 1 after a reset, every request answered by a
  matching response before the next (`serialcap.Analyze`);
- decodes the ASDU with the field sizes of the manifest and encodes frame
  and ASDU again: **every frame of the recordings encodes back byte for
  byte**;
- compares the decoded fields with Wireshark's (`TestOracleTshark`, the
  `*.tshark.tsv.gz` files written by `make captures-oracle`);
- replays every recorded controlled station against `iec101/client`
  (`client/capture_test.go`: recorded requests made through `Interrogate`,
  `Command` and `Send` must equal the recorded octets; every station ASDU
  must reach the handler unchanged) and the controlling stations that send
  requests against `iec101/server` (`server/capture_test.go`: the
  confirmations and terminations the server builds must equal the recorded
  ones);
- seeds `ft12.FuzzDecode` and `asdu.FuzzDecode` with one frame and one ASDU of
  every shape of the recordings.

## Sources and terms

| Directory | Source | Revision | Terms |
|---|---|---|---|
| `wireshark-issues/` | Wireshark issues [15688](https://gitlab.com/wireshark/wireshark/-/issues/15688), [16428](https://gitlab.com/wireshark/wireshark/-/issues/16428), [17775](https://gitlab.com/wireshark/wireshark/-/issues/17775) | the attachments' upload URLs (manifest) | Attached to public issues; no licence stated for the attachments |
| `streampacketizer/` | [unddef/StreamPacketizer](https://github.com/unddef/StreamPacketizer/tree/01a394334a1962c9b3f5cb5d08226b1c70c231d3/VSC_proj) `VSC_proj/` | commit `01a394334a1962c9b3f5cb5d08226b1c70c231d3` | No licence file, no terms stated |
| (shared) | `iec104/testdata/captures/wireshark/synthetic_synthetic_iec104-coverage.pcap.gz`, TCP port 2405 | Wireshark commit `f7e10bdd477b2f8f4995aa45c8155a0768f433ab` | GPL-2.0-or-later repository; committed once, in the IEC 104 corpus |

Not committed: `iec101.tar.xz` of issue 16428 (the same two pcapng files and
two screenshots) and StreamPacketizer's `test_output.pcap` (no frame).
The Wireshark issue 19921 attachments (`101mistakes.pcapng`, `1.pcapng` to
`4.pcapng`) need a login and were not obtained.

## Names

`<controlling-station>_<controlled-station>_<operation>.<ext>`, lower case,
words joined by hyphens, original extension.

| Part | Values |
|---|---|
| controlling station | `unknown` (not stated by the source), `synthetic` (generated) |
| controlled station | `sel-rtac` (an SEL RTAC recorded its own serial port), `unknown`, `synthetic` |
| operation | what the capture holds |

## Evidence

Levels: A verified physical-device traffic, B credibly documented
physical-device traffic, C independent recorded traffic with unknown
hardware origin, D simulated or software testbed, E generated.

| File | Original name | Level | Basis |
|---|---|:-:|---|
| `wireshark-issues/unknown_sel-rtac_class1-class2-polls.pcap` | `IEC_101_Sample_Class_1_and_Class_2.pcap` | B | SEL RTAC serial-line capture with the UART control lines of a physical port; the RTAC is the controlled station. Model, firmware and the controlling station are not stated |
| `wireshark-issues/unknown_sel-rtac_double-command-sbo.pcap` | `IEC_101_control_capture.pcap` | B | As above; a shared line: the RTAC also hears the polls of link addresses 78, 20 and 57 and the answers of the stations at 78 and 20 |
| `wireshark-issues/unknown_unknown_energisa-polls-m-sp-ta-1.pcapng` | `monitor_energisa_1_2020_03_05.pcapng` | B | Utility network (Energisa); three serial lines reached through Digi (MAC 00:40:9D) and Siemens Canada/RuggedCom (50:00:84) serial port servers. The stations behind them are not identified |
| `wireshark-issues/unknown_unknown_energisa-polls-m-sp-tb-1.pcapng` | `monitor_energisa_2.pcapng` | B | The same network, 18 minutes |
| `wireshark-issues/unknown_unknown_m-me-nb-1-link-address-2.pcapng` | `issue-17775.pcapng` | E | One frame a Wireshark developer rebuilt from the reporter's screenshot |
| `streampacketizer/synthetic_synthetic_class-polls.pcap` | `VSC_proj/test.pcap` | E | Written by the tool: whole-second time stamps, made-up addresses and sequence numbers |
| `streampacketizer/synthetic_synthetic_status-polls.pcap` | `VSC_proj/testing.pcap` | E | As above |
| `streampacketizer/synthetic_synthetic_interrogation-link-address-2.pcap` | `VSC_proj/today.pcap` | E | As above; 2-octet link address, cause, common address and IOA |
| (shared) IEC 104 corpus `wireshark/synthetic_synthetic_iec104-coverage.pcap.gz`, port 2405 | `test/captures/iec104_coverage.pcap.gz` | E | Generated for Wireshark's dissector tests; the IEC 104 corpus records the file as E |

Levels B rest on the capture format and the network hardware, not on a
named device: none of the sources names the make, model or firmware of an
IEC 101 station.

## What the recordings show

Counts per file are in [manifest.json](manifest.json). The four B
recordings hold 25,535 frames (FT 1.2 fixed and variable frames and 81 E5h)
of the RTAC, two other stations on its line and the three Energisa
stations; all decode, all encode back byte for byte, and the link analysis
finds no departure from the rules above: FCB toggles in every one of
12,754 checked messages, no repetition was needed, and every request is
answered except one request of link status to link address 57, where no
station answers on the RTAC's shared line. Recorded behaviour that the tests pin:

- **SEL RTAC (controlled station)**: answers a class 2 poll with "no data"
  (function 9) rather than E5h; confirms and terminates C_IC_NA_1 and the
  select and execute of C_DC_NA_1 with the request's objects; the station
  at link address 78 on its line answers polls with E5h.
- **Energisa stations** (link addresses 7, 12, 15): CP24Time2a (M_SP_TA_1,
  M_DP_TA_1) next to CP56Time2a in the same stations; C_TS_NA_1 sent by the
  controlled stations with cause activation (6), and C_CS_NA_1 with cause
  spontaneous (3), with no request from the controlling station, which
  sends class 1 and class 2 polls only. We read IEC 60870-5-101 7.2.3 as
  allowing C_TS_NA_1 only in the control direction (and its confirmation in
  the monitor direction); this is recorded as the stations' behaviour, not
  checked against the table, and the client passes these ASDUs to the
  handler unchanged.
- **Wireshark 4.6.9** (oracle): reads the IOA of C_TS_NA_1 as three octets
  whatever its IOA size preference says, and does not reassemble a frame
  whose four header octets are split over two TCP segments; both are
  classified in `oracle_test.go`, everything else agrees.

The generated captures (E) break the link procedures on purpose or by
construction (responses without requests, FCB not toggled); their findings
are listed in the manifest and do not count as evidence.

## Not in the recordings

Balanced transmission, 2-octet link addresses in recorded traffic, the
originator address, file transfer, counter interrogation, reads,
parameters, set points, end of initialization, link resets and repeated
messages after a time-out, and any named IEC 101 device.
