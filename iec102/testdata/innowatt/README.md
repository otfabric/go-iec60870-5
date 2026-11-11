# Recorded IEC 60870-5-102 traffic (innoWatt)

Decoder logs of real meter readings, committed byte-identical to their
source and checked by [SHA256SUMS](SHA256SUMS). `iec102` tests decode every
frame the logs contain (FT 1.2 with a two-octet link address, then the
ASDU) and encode it again to the same octets.

| File | Source | Revision | Terms |
|---|---|---|---|
| `icra_201605_raw.txt` | [innoWatt/icra-tarifes-optic](https://github.com/innoWatt/icra-tarifes-optic) `usr/tests/icra_201605_raw.txt` | commit `b76638cef85e345aba883776ab5b106554df5c5b` (file last changed in `ec25a5a0f8a16383a9391dda0ae91e63d9510622`) | repository GPL-3.0; no statement for the log |
| `proves.txt` | same repository, `usr/lectures/girona/proves.txt` | commit `b76638cef85e345aba883776ab5b106554df5c5b` | as above |

OT Fabric records the terms of captures and logs; they do not restrict
their use as test input. No code of the repository is used.

## What they contain

`icra_201605_raw.txt` is the output of the repository's own decoder
(Catalan labels, ANSI colours) for one reading of an Actaris SL762B meter
in May 2016: only the frames of the meter (the "PREGUNTA" sections, the
controlling station's requests, are empty), 749 frames in all:

| Frames | Content |
|---:|---|
| 2 | fixed frame, ACK (function 0), link address 1 |
| 1 | C_AC_NA_2 (183), cause 7: confirmation of the session start, access key 1 |
| 1 | C_CI_NU_2 (123), cause 7: confirmation of a read of record 11, addresses 1..1, 2016-05-01 00:00 .. 2016-06-01 00:00 |
| 744 | M_IT_TK_2 (11), cause 5: one hourly increment of total 1 (active import) each, with time a |
| 1 | C_CI_NU_2 (123), cause 10: termination of the read |

Link address and common address (measuring point address) are two octets,
both 1; the record address is one octet. Each M_IT_TK_2 object is six
octets (address, CR4, quality descriptor), no signature.

`proves.txt` (a meter in Girona) holds one frame of the controlling
station: a send/confirm (control 73h) with ASDU 162 (P_IN_VA_2, the REE
extension "read instantaneous values"), cause 5, followed by the decoder's
error message that the type is not implemented.
