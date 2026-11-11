# Recorded IEC 60870-5-103 traffic and test vectors

Captures of IEC 60870-5-103 lines recorded by others and the test vectors
of another implementation, committed byte-identical to their sources
([SHA256SUMS](SHA256SUMS), `make captures-verify`). Origin, terms and what
the tests derive from each file are in [manifest.json](manifest.json)
(`TestManifest`, `make captures-manifest`). Evidence from recorded traffic,
not a test against a live system and not a conformance claim.

## What the tests do

`iec103/internal/capture` (with `internal/pcapio` and `internal/serialcap`)
reads every frame and:

- checks FT 1.2 framing and checksum (no octet of any file is
  undecodable), the control field, FCV and FCB per link address, and the
  pairing of every request with its response (`serialcap.Analyze`);
- decodes the ASDU with `iec103.Decode` (private types such as 205 as
  `iec103.Raw`) and encodes frame and ASDU again: what decodes encodes back
  byte for byte, except the frames listed in `corpus_test.go` with their
  reason;
- compares the link fields and the ASDU fields Wireshark dissects (ASDUs 1,
  5, 6, 7, 8, 9, 20, 205; `TestOracleTshark`, `*.tshark.tsv.gz`);
- checks the decode the ARIEC103 vectors state in their comments
  (`TestVectors`);
- seeds `iec103.FuzzDecode` and `ft12.FuzzDecode` from the recordings.

There is no replay against `iec103/client` or `iec103/server` yet: the
recordings are short excerpts (see below) on shared lines, which one
client per item of equipment cannot serve.

## Sources and terms

| Directory | Source | Revision | Terms |
|---|---|---|---|
| `wireshark-mr2917/` | Wireshark [merge request 2917](https://gitlab.com/wireshark/wireshark/-/merge_requests/2917) (IEC 103 dissector), sample captures in its first comment | upload URLs in the manifest (2021-05-04) | Attached to a public merge request; no licence stated for the attachments |
| `ariec103/` | [masarray/ARIEC103](https://github.com/masarray/ARIEC103/tree/ec08dd73991be646864aef55e6a5538b78129db8/samples/test-vectors) `samples/test-vectors/` | commit `ec08dd73991be646864aef55e6a5538b78129db8` | Apache-2.0 |
| (shared) | `iec104/testdata/captures/wireshark/synthetic_synthetic_iec104-coverage.pcap.gz`, TCP port 2406 | Wireshark commit `f7e10bdd477b2f8f4995aa45c8155a0768f433ab` | GPL-2.0-or-later repository; committed once, in the IEC 104 corpus |

Not committed: the vectors' README, `sample_iec103_trace.log` (a log, not
frames) and the mapping profile of ARIEC103.

## Names

`<controlling-system>_<protection-equipment>_<operation>.<ext>`, lower
case, words joined by hyphens, original extension.

| Part | Values |
|---|---|
| controlling system | `sel-rtac` (an SEL RTAC recorded its own serial port), `synthetic` |
| protection equipment | `sel-ied` (identifies itself as "SEL" in ASDU 5), `unknown`, `synthetic` |
| operation | the ASDUs or the procedure the file holds |

## Evidence

Levels: A verified physical-device traffic, B credibly documented
physical-device traffic, C independent recorded traffic with unknown
hardware origin, D simulated or software testbed, E generated.

| File | Original name | Level | Basis |
|---|---|:-:|---|
| `wireshark-mr2917/sel-rtac_sel-ied_asdu1-5-20-45-46.pcap` | `iec103_asdu1_5_20_45_46.pcap` | B | SEL RTAC serial-line capture (UART control lines of a physical port), the RTAC is the controlling system; the equipment answers ASDU 5 with manufacturer "SEL". No model or firmware stated |
| `wireshark-mr2917/sel-rtac_unknown_asdu1-6-7-8-9.pcap` | `iec103_asdu1_6_7_8_9.pcap` | B | As above; the equipment is not identified |
| `wireshark-mr2917/sel-rtac_unknown_private-asdu205.pcap` | `iec103_asdu205.pcap` | B | As above; a shared line with equipment at link addresses 1 to 3, addresses 4 and 5 do not answer |
| `ariec103/synthetic_synthetic_class1-event-type1.hex` | `class1-event-type1.hex` | E | Hand-made test vector |
| `ariec103/synthetic_synthetic_class2-measurand-type9.hex` | `class2-measurand-type9.hex` | E | Hand-made test vector |
| `ariec103/synthetic_synthetic_fixed-no-data.hex` | `fixed-no-data.hex` | E | Hand-made test vector |
| `ariec103/synthetic_synthetic_gi-end-type8.hex` | `gi-end-type8.hex` | E | Hand-made test vector |
| `ariec103/synthetic_synthetic_identification-type5.hex` | `identification-type5.hex` | E | Hand-made test vector |
| `ariec103/synthetic_synthetic_unknown-private-type205.hex` | `unknown-private-type205.hex` | E | Hand-made test vector |
| (shared) IEC 104 corpus `wireshark/synthetic_synthetic_iec104-coverage.pcap.gz`, port 2406 | `test/captures/iec104_coverage.pcap.gz` | E | Generated for Wireshark's dissector tests |

## What the recordings show

The three B captures hold 149 frames. All are FT 1.2 frames with valid
checksums and all 149 encode back byte for byte, including the 20 private
ASDUs 205 (FUN 133) and the private control ASDUs 45 and 46 carried as
`iec103.Raw`, and the ASDUs 9 below. Wireshark agrees on every field it
dissects.

- **ASDU 9 with more than nine measurands.** The equipment of
  `asdu1-6-7-8-9` sends ASDU 9 with 16 measurands (VSQ 10h), one station
  of `private-asdu205` with 14 (VSQ 0Eh). IEC 60870-5-103 clause 7.3.1.8
  allows one to nine; `iec103.Decode` carries them up to
  `iec103.MaxMeasurandsWire` (127) and records them as the equipment's
  deviation. Wireshark dissects them.
- **The files are excerpts.** The RTAC polls continuously, yet
  `asdu1-5-20-45-46` has 2.2 s and 41 s without a frame and
  `asdu1-6-7-8-9` 149 s: the link findings at those points (FCB looking
  not toggled, an answer to a poll that is not in the file) are listed in
  `corpus_test.go` as artefacts of the excerpt, not as RTAC deviations.
- **SEL RTAC as controlling system**: in `private-asdu205` it resets the
  frame count bit (function 7, clause 6.3) of each link address before
  polling it; it sends general commands (ASDU 20), answered with ASDU 1 of
  causes 20 and 12, and the private ASDUs 45 and 46 with cause 20.

The ARIEC103 vectors encode the VSQ of ASDUs 1 and 8 as 01h instead of
81h: the codec decodes them and writes 81h, so they do not encode back.
The identification vector has 24 characters and no compatibility level,
which the codec refuses. Both are the vectors' errors, listed in
`corpus_test.go`. The ASDU 9 vector's "first signed 16-bit value 1234"
is the MEA 154 with ER set (clause 7.2.6.8).

## Not in the recordings

Disturbance data transfer, generic services (ASDUs 10, 11, 21), test
mode, the command transmission of clause 7.4.5 beyond general commands,
link resets of the communication unit (function 0) and any named
equipment model.
