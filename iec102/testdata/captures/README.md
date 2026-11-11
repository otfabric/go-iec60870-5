# IEC 60870-5-102 frames for the coming IEC 102 work

There is no IEC 102 codec in this module yet (docs/ROADMAP.md). These
files are kept, byte-identical to their sources ([SHA256SUMS](SHA256SUMS),
`make captures-verify`), as the first evidence for it; their origin, terms
and counts are in [manifest.json](manifest.json) (`TestManifest`,
`make captures-manifest`).

`iec102/internal/capture` extracts every frame and `TestFrames` decodes it
as FT 1.2 with a 2-octet link address (start and stop characters, both
length octets, checksum) and encodes it back: all 759 frames pass. The
ASDUs (183, 123, 11 of the Spanish REE profile) are not decoded; we do not
hold IEC 60870-5-102 (docs/STANDARDS.md).

## Sources and terms

| Directory | Source | Revision | Terms |
|---|---|---|---|
| `innowatt/` | [innoWatt/icra-tarifes-optic](https://github.com/innoWatt/icra-tarifes-optic/tree/b76638cef85e345aba883776ab5b106554df5c5b) `usr/tests/icra_201605_raw.txt` | commit `b76638cef85e345aba883776ab5b106554df5c5b` | GPL-3.0 |
| `gisce/` | [gisce/iec870ree](https://github.com/gisce/iec870ree/tree/993242daa3e998c9d86948881d97835438d8bef3/tests) `tests/` | commit `993242daa3e998c9d86948881d97835438d8bef3` | AGPL-3.0 |

## Names and evidence

`<controlling-station>_<meter>_<operation>.<ext>`, original extension.

| File | Original name | Level | Basis |
|---|---|:-:|---|
| `innowatt/icra-tarifes-optic_actaris-sl762b_load-profile-201605.txt` | `usr/tests/icra_201605_raw.txt` | B | The tool's log of reading the Actaris SL762B at ICRA through its optical port (the README names the meter). A log, not a capture: 749 answers of the meter (link address 1), no requests, no time stamps |
| `gisce/synthetic_synthetic_link-frames.py` | `tests/test_protocol.py` | E | Frames in unit tests (link addresses 34572 and 53653) |
| `gisce/synthetic_synthetic_base-asdu.py` | `tests/test_base_asdu.py` | E | Frames in unit tests |
| `gisce/synthetic_synthetic_app-asdu.py` | `tests/test_app_asdu.py` | E | ASDU element octets in unit tests, no frames |

Not committed: the decoded readings of the innoWatt repository
(`usr/lectures/...`, derived from logs like this one).
