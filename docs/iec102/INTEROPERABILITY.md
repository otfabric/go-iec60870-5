# IEC 60870-5-102: sources, classification and verification

What `iec102`, `iec102/ree`, `iec102/client` and `iec102/server`
implement, where each behaviour comes from and how it is verified.

**We do not hold the text of IEC 60870-5-102:1996.** No free national
adoption was found (DSTU EN 60870-5-102:2022 is an identical adoption but
not available to us). Nothing below is checked against the standard;
everything is traced to the sources listed next, and anything resting on
them alone is an interpretation.

## Sources

| Id | Source | Revision | Terms | How it is used |
|---|---|---|---|---|
| **BK** | Beckhoff, "Manual TwinCAT PLC IEC 60870-5-101, -102, -103, -104" (TS650x), 511 pages | version 1.3, 2023 | vendor documentation | the IEC 102 telegram structure (section 3.2), type identifications (4.2, 9.1.10), read command layouts (4.22.6 to 4.22.29), integrated totals and single-point layouts (4.25.1 to 4.25.13), information elements (4.26), record addresses (9.1.8), causes (9.1.11), time formats (9.1.12, 9.1.13). Vendor documentation of a commercial master, cited as "Beckhoff TS650x manual, section x"; it is not the standard |
| **LOG** | innoWatt/icra-tarifes-optic `usr/tests/icra_201605_raw.txt`, `usr/lectures/girona/proves.txt` | commit `b76638cef85e345aba883776ab5b106554df5c5b` | GPL-3.0 repository; committed byte-identical in `iec102/testdata/innowatt` with provenance | recorded traffic of an Actaris SL762B meter: 749 frames of the station (session start, one C_CI_NU_2 read of a month of hourly increments); one request frame of a controlling station (ASDU 162) |
| **GV** | gisce/iec870ree tests (`tests/test_base_asdu.py`, `test_protocol.py`, `test_app_asdu.py`) | commit `993242daa3e998c9d86948881d97835438d8bef3` | AGPL-3.0 | the byte strings only, as observed protocol data, in `iec102/asdu_test.go` (origin noted there); some are said there to come from the REE specification ("segun el pdf") |
| **GI** | gisce/iec870ree (Python; derives from javierdelapuente/reeprotocol, one lineage) | as GV | AGPL-3.0 | read for comparison only, no code copied; master only |
| **DA** | daiiniel/IEC60870-5-102 (C#) | commit `a268d45b08652cd907ac00491ca7f5a9f97d1825` | **no licence** (all rights reserved) | read for comparison only, no code copied; master only |
| **101** | IEC 60870-5-101:2003, identical adoption GOST R IEC 60870-5-101-2006 | held | | the IEC 60870-5-1 and -2 rules that `ft12` and `link` implement |

Confidence: **high** = BK gives the layout and LOG or GV confirm it on the
wire; **medium** = BK alone, or two independent implementations agree;
**low** = one implementation only; **none** = no source.

## Classification

### Type identifications

| Type | Mnemonic | Class | Layout source | Confidence | In this module |
|---|---|---|---|---|---|
| 1 | M_SP_TA_2 | IEC 102 standard (BK 4.2) | BK 4.25.1 (SPA, SPQ+SPI, time b); GI agrees | medium | `iec102.SinglePoints` |
| 2..7 | M_IT_TA_2 .. M_IT_TF_2 | IEC 102 standard (BK 4.2) | BK 4.25.2 to 4.25.7 | medium | `iec102.IntegratedTotals` |
| 8 | M_IT_TG_2 | IEC 102 standard (BK 4.2) | BK 4.25.8; GI, DA agree | medium | `iec102.IntegratedTotals` |
| 9, 10 | M_IT_TH_2, M_IT_TI_2 | IEC 102 standard (BK 4.2) | BK 4.25.9, 4.25.10 | medium | `iec102.IntegratedTotals` |
| 11 | M_IT_TK_2 | IEC 102 standard (BK 4.2) | BK 4.25.11; LOG (744 frames), GV, GI, DA | high | `iec102.IntegratedTotals` |
| 12, 13 | M_IT_TL_2, M_IT_TM_2 | IEC 102 standard (BK 4.2) | BK 4.25.12, 4.25.13 | medium | `iec102.IntegratedTotals` |
| 14..69 | - | reserved (BK 4.2: "standard area") | - | - | `iec102.Raw` |
| 70 | M_EI_NA_2 | IEC 102 standard (BK 4.2) | **none** (BK has no layout, GI and DA do not implement it) | unresolved | `iec102.Raw` |
| 71 | P_MP_NA_2 | IEC 102 standard (BK 4.2) | GI only (standard octet, manufacturer, 4-octet product code); GV vector | low | `iec102.ProductSpec` |
| 72 | M_TI_TA_2 | IEC 102 standard (BK 4.2) | GI, DA (one time b); GV vector | medium | `iec102.SystemTime` |
| 73..99 | - | reserved (BK 4.2) | - | - | `iec102.Raw` |
| 100, 101, 103 | C_RD_NA_2, C_SP_NA_2, C_TI_NA_2 | IEC 102 standard (BK 4.2) | BK 4.22.6, 4.22.7, 4.22.9 (no object) | medium | `iec102.Read` |
| 102 | C_SP_NB_2 | IEC 102 standard (BK 4.2) | BK 4.22.8 (two time a); GI agrees | medium | `iec102.Read` |
| 104..119 | C_CI_NA_2 .. C_CI_NQ_2 | IEC 102 standard (BK 4.2) | BK 4.22.10 to 4.22.25 | medium | `iec102.Read` |
| 120, 121 | C_CI_NR_2, C_CI_NS_2 | IEC 102 standard (BK 4.2) | BK 4.22.26, 4.22.27 | medium | `iec102.Read` |
| 122 | C_CI_NT_2 | IEC 102 standard (BK 4.2) | BK 4.22.28; GV, GI, DA | medium | `iec102.Read` |
| 123 | C_CI_NU_2 | IEC 102 standard (BK 4.2) | BK 4.22.29; LOG (confirmation and termination), GV, GI, DA | high | `iec102.Read` |
| 124..127 | - | reserved (BK 4.2) | - | - | `iec102.Raw` |
| 128, 130, 131 | M_DS_TA_2, M_DS_TB_2, M_CH_TA_2 | REE profile (named in BK 4.2 without description) | none | unresolved | `iec102.Raw` |
| 129 | P_ME_NA_2 | REE profile (BK 4.2 name) | DA only | low | `iec102.Raw` |
| 132 | C_PK_2 | REE profile (BK 4.2 name) | none | unresolved | `iec102.Raw` |
| 133 | C_TA_VC_2 | REE profile (BK 4.2) | GI (no object) | low | `ree.Request` |
| 134 | C_TA_VM_2 | REE profile (BK 4.2) | GI (two time a); GV vector | low | `ree.Request` |
| 135, 136 | M_TA_VC_2, M_TA_VM_2 | REE profile (BK 4.2) | GI (63-octet record); GV vector | low | `ree.TariffInfo` |
| 137 | C_TA_CP_2 | REE profile (BK 4.2) | none | unresolved | `iec102.Raw` |
| 139, 140 | M_IB_TG_2, M_IB_TK_2 | REE profile (BK 4.2) | GI (140: blocks per object 9, 10, 11), DA (139, 140) | low | `iec102.Raw` |
| 141 | C_RM_NA_2 | REE profile (BK 4.2) | GI (no object) | low | `ree.Request` |
| 142 | M_RM_NA_2 | REE profile (BK 4.2) | GI, DA (long configuration record) | low | `iec102.Raw` |
| 143 | C_MR_NA_2 | REE profile (BK 4.2) | none | unresolved | `iec102.Raw` |
| 144, 147 | C_PC_NA_2, C_DF_NA_2 | REE profile (BK lists the numbers without description) | GI (no object) | low | `ree.Request` |
| 145 | M_PC_NA_2 | REE profile (BK number only) | GI (address + 4-octet power, then time a) | low | `ree.ContractedPowers` |
| 146, 149 | C_MC_NA_2, C_MF_NA_2 | REE profile (BK number only) | none | unresolved | `iec102.Raw` |
| 148 | M_DF_NA_2 | REE profile (BK number only) | GI (time a per day) | low | `ree.SpecialDays` |
| 150, 151, 156, 157, 162, 163 | P_TA_IN_2, R_TA_IN_2, P_RM_R2_2, R_RM_R2_2, P_IN_VA_2, R_IN_VA_2 | REE profile extension (GI: "ampliación de protocolo"; not in BK) | GI; LOG has one 162 request | low | `iec102.Raw` |
| 180, 184..186, 188 | C_DS_TA_2, C_DS_TB_2, C_CH_TA_2, C_MH_TA_2, C_MP_NA_2 | REE profile (BK number only) | none | unresolved | `iec102.Raw` |
| 181 | C_CS_TA_2 | REE profile (BK 4.2) | GI (time b), GV vector; DA disagrees on time b (below) | low | `ree.SetClock` |
| 182 | C_PI_NA_2 | REE profile (BK 4.2) | DA only | low | `iec102.Raw` |
| 183 | C_AC_NA_2 | REE profile (BK 4.2) | LOG (confirmation), GV, GI, DA (4-octet key) | high | `ree.AccessKey` |
| 187 | C_FS_NA_2 | REE profile (BK 4.2) | GI, DA (no object) | medium | `ree.Request` |
| 189, 190 | C_CB_NT_2, C_CB_UN_2 | REE profile (BK 4.2) | GI (190), DA (189): object + two time a | medium | `ree.Request` |
| other 128..255 | - | private range | - | - | `iec102.Raw` |

Totals: **40 standard type identifications** (BK 4.2), of which 37 have a
BK layout (2 confirmed on the wire: 11 and 123), 2 rest on implementations
only (71, 72) and 1 is unresolved (70); **38 REE-profile types** (32 named
or numbered in BK 4.2, 6 extension types from GI only), of which 14 have a
typed body (one confirmed on the wire: 183; 162 is on the wire but stays
raw), the rest raw; **no vendor-specific type** (BK marks no type as Beckhoff's
own); **unresolved**: 70 and the 13 REE types without any layout.

### Causes of transmission and data unit identifier

| Item | Class | Source | Confidence |
|---|---|---|---|
| cause octet: cause 0..63 (bits 0-5), P/N (bit 6), T (bit 7) | IEC 102 standard | BK 3.2, 9.1.9; GI agrees; DA decodes only 5 bits (`& 0x1F`) | medium |
| 3 spontaneous, 4 initialization, 5 request/requested, 6 activation, 7 activation confirmation, 8 deactivation, 9 deactivation confirmation, 10 activation termination | IEC 102 standard | BK 9.1.11; LOG uses 5, 7, 10; DA same numbers | high for 5, 7, 10; medium for the rest |
| 13 record not available, 14 type not available/unknown, 15 unknown record number, 16 unknown record address, 17 information object not available, 18 integration period not available | IEC 102 standard | BK 9.1.11; DA same numbers with different names (14 "ASDU not available", 16 "ASDU unknown"); GI handles 14, 16, 17, 18 as errors | medium |
| 1, 2, 11, 12, 19..63 | unresolved (BK lists them without meaning) | - | none |
| VSQ: SQ (bit 7), number of objects (bits 0-6) | IEC 102 standard | BK 9.1.9 | medium |
| common address of ASDU, 1 or 2 octets (REE: "measuring point address", 2) | IEC 102 standard; size 2 REE profile | BK 3.2, 9.3 (example 2 octets); LOG, GI, DA (2) | high for 2 |
| record address, 1 octet, values per BK 9.1.8 (0 default, 1, 11..13, 21..23, 31..33, 41..43, 50..55; 128..255 private) | IEC 102 standard; 134..139 REE profile | BK 9.1.8; LOG (11), GI/DA (11, 12, 21, 52..55, 134..139) | high for 11 |

### Information elements

| Item | Class | Source | Confidence |
|---|---|---|---|
| time a (CP40Time2a): minute + TIS + IV, hour + RES1 + SU, day + day of week, month + ETI + PTI, year + RES2 | IEC 102 standard (not an IEC 60870-5-4 type, so in `iec102`, not `cp`) | BK 9.1.12; LOG, GV, GI, DA agree on all bit positions | high |
| time b (CP56Time2b): milliseconds 0..999 in bits 0-9 and seconds in bits 10-15 of the first word, then time a | IEC 102 standard | BK 9.1.13 (prints "bits 11..15", a misprint: 5 bits cannot hold 59); GI and the GV vectors agree; **DA disagrees** | medium |
| year 0..99 read as 2000..2099 | interpretation | GI, DA; BK names no century | low |
| day of week 1 = Monday .. 7 = Sunday, 0 unused | IEC 102 standard | BK 9.1.12 (0 not used, 1..7); LOG (Sunday 7, Wednesday 3), GI; **DA writes .NET DayOfWeek (Sunday 0)** | high |
| integrated total object: address, CR4/CR3/CR2 little-endian, quality descriptor, optional signature octet | IEC 102 standard | BK 4.25.2 ff.; LOG (6 octets, no signature) | high for CR4 without signature, medium otherwise |
| quality descriptor: IV (7), CA (6), CY (5), sequence number (0-4) | IEC 102 standard | BK 4.25.8, 4.26.32 | medium |
| quality bits 0-4 as VH, MP, IN, AL, RES | REE profile | DA encoder, LOG labels "iv,ca,cy,vh,mp,in,al,res" | medium; `ree.Quality` |
| signature octet: presence | IEC 102 standard (optional) | BK 4.25.x "Signature (optional)" | medium; detected from the length |
| signature octet: computation | unresolved | none | not checked |
| counter readings unsigned | interpretation | GI, DA read unsigned 32-bit values | low |
| single point: SPA (128..255 private), SPI bit 0, SPQ bits 1-7 (0 unspecified, 1..127 vendor specific) | IEC 102 standard; SPQ values vendor-specific | BK 4.25.1, 4.26.1, 4.26.2; GI (masks `0xF7`, apparently a slip) | medium |
| integrated total addresses 1 active import, 2 export, 3..6 reactive quadrants, 7, 8 reserved | REE profile | GI, DA, LOG label for 1 | medium; `ree.Addr*` |
| P_MP_NA_2 first octet as month/year of the standard edition; manufacturer and product codes | vendor-specific values, layout low confidence | GI only | low |

### Link layer

| Item | Class | Source | Confidence |
|---|---|---|---|
| FT 1.2: fixed and variable frames, single character E5h, Hamming distance 4 | IEC 102 standard | BK 3.2; 101 clause 6.1 for the frame rules | high |
| link address 0, 1 or 2 octets | IEC 102 standard | BK 3.2 | medium |
| link address 2 octets | REE profile | LOG, GV, GI, DA | high |
| unbalanced transmission, controlling station primary | IEC 102 standard (by the sources' use) | BK 9 (master only), LOG, GI, DA | medium |
| PRM=1 functions 0 (reset of remote link), 3 (user data, confirm), 9 (request status of link), 11 (request class 2) | observed | GI, DA use exactly these; LOG shows ACK (0) and user data (8) answers | high |
| function 10 (request class 1) | unresolved | neither GI nor DA polls class 1; our client polls it while ACD is set, as IEC 60870-5-2 (101 clause 6.2) provides; our server never sets ACD | - |
| function 4 (send/no reply), broadcast | unresolved | no source | not used |
| maximum ASDU 253 octets | IEC 102 standard | BK 9.1.6 (`asduSize` 253 for 101/102/103) | medium |

Where 102 differs from the 101 profile of `link`: the link address of two
octets is the norm (101 leaves 1 or 2 to the system); only PRM=1 functions
0, 3, 9, 11 are seen in use; the application layer has its own ASDU. None
of it needs a change of `link`: the stations set `link.Config.FT12` from
their `LinkAddrSize`.

### Application procedures

BK describes no application procedure (its 102 library is "low level
only", section 9). The procedures below come from LOG and GI and are
**interpretations**:

| Procedure | Client | Server | Source |
|---|---|---|---|
| link initialization: request status of link, reset of remote link | `Client.Init`, also before the first request and after the link was lost | `link.Secondary` | GI, DA; 101 clause 6.2 |
| a request: user data with confirm (function 3), then polls until the answer is complete | `Client.Request` with `UntilReply`, `UntilConfirmation`, `UntilTermination` | answers as class 2 data | GI, LOG |
| C_RD_NA_2, C_TI_NA_2 with cause 5, answered directly by P_MP_NA_2 / M_TI_TA_2 with cause 5 | `ReadProductSpec`, `ReadTime` | yes | GI |
| C_CI_*, C_SP_NA_2/NB_2 with cause 6: confirmation (7), data (5), termination (10) | `ReadTotals`, `ReadSinglePoints` | yes, from `server.Store` | LOG (123: 7, 744 x 11 with 5, 10), GI |
| refusal: the request mirrored with a cause 13..18 or P/N | `*iec102.RefusedError` | 16 unknown record or common address, 14 no totals of the family / unknown type, 18 no period, 17 no object in the address range | causes BK 9.1.11; which cause when is our choice |
| session start C_AC_NA_2 (cause 6, key), confirmed with 7 | `ree.Login` | `ree.Session` (wrong key: 7 with P/N, our choice) | LOG, GI, DA |
| end of session C_FS_NA_2, set clock C_CS_TA_2 (cause 6, confirmed with 7) | `ree.Logout`, `ree.SetTime` | `ree.Session` | GI |
| VSQ of a request: 0 without object, 1 with one | `iec102.New` follows BK; `ree` request bodies count 1, except those without information elements (0) | - | BK 4.22 (nObj); GI on the wire (interop suite) |

## Source conflicts

Recorded, not silently resolved:

1. **Time b, first two octets.** BK 9.1.13 and GI: milliseconds 0..999 in
   bits 0-9, seconds in bits 10-15. DA: seconds*1000+milliseconds, as
   CP56Time2a of IEC 60870-5-4. The GV vectors (`00 90` = 36 s 0 ms,
   `00 64` = 25 s 0 ms, `0a 2a` = 10 s 522 ms) only make sense in the first
   reading; we follow BK. BK's own "bits 11..15" for the seconds is a
   misprint.
2. **Quality descriptor bits 0-4.** BK: sequence number. REE (DA, LOG
   labels): VH, MP, IN, AL, reserved. The octet is kept; `iec102.Quality`
   reads it the BK way, `ree.Quality` the REE way. DA's own decoder masks
   the bits inconsistently (`0x03` .. `0x08`), its encoder is used as the
   reference.
3. **VSQ of requests.** BK: 0 for requests without object, 1 otherwise
   (C_SP_NB_2: 1). GI: `ceil(data_length / 6)`, which is 0 for its
   requests without information elements (C_TI_NA_2 `67 00 05`, C_FS_NA_2
   `BB 00 06`, C_TA_VC_2: seen on the wire in the interop suite), 1 for
   those with them, and 0 for C_SP_NB_2. This document said until v0.3.0
   that GI writes 1 for every request, and `ree.Request` counted 1 object
   for C_FS_NA_2, C_TA_VC_2, C_RM_NA_2, C_PC_NA_2 and C_DF_NA_2; it now
   counts 0 for them, as BK and GI do (`TestREERequestVSQ`). C_SP_NB_2 is
   the one remaining difference (BK 1, GI 0). The codec writes what
   `ASDU.VSQ` holds; `iec102.New` follows BK.
4. **Cause width.** BK and GI: 6 bits. DA: 5 bits. We use 6.
5. **Names of causes 13..18** differ between BK and DA (numbers agree).
6. **Day of week.** BK, GI, LOG: 1..7, Monday 1. DA: Sunday 0.
7. **Single-point qualifier.** BK: SPQ is 7 bits above SPI. GI masks with
   `0xF7`, dropping bit 3; we follow BK.

## Verification

| Level | What | Where |
|---|---|---|
| recorded traffic | all 749 frames of LOG decode (FT 1.2, then the ASDU, with and without `ree.Profile`) and encode back to the same octets; the first frames' fields equal the log's own reading; the hourly periods are contiguous | `iec102/capture_test.go` |
| vectors | GV frames, times, P_MP_NA_2, M_TI_TA_2, C_CS_TA_2, C_CI_NT_2/NU_2, M_IT_TK_2, C_TA_VM_2, M_TA_VM_2 | `iec102/asdu_test.go` |
| unit | octets written from the BK layouts for every body; error paths | `iec102/codec_test.go` |
| fuzz | `FuzzDecode`: whatever decodes encodes back to the same octets (plain, REE, one-octet common address) | `iec102/codec_test.go` |
| e2e | `iec102/client` against `iec102/server` over `net.Pipe`: every read selection, address ranges, splitting, signatures, refusals, REE session, link sizes 0/1/2, link loss | `iec102/e2e` |
| interop | **independent (gisce, REE profile)**: the controlling station of gisce/iec870ree v0.14.1 against `iec102/server` with `ree.Session`, 18 checks, every result document compared with the harness's fixture (below) | `iec102/interop` (`make interop-iec102`) |

The e2e tests show that our two sides agree with each other; the interop
suite that our server agrees with one independent master of the REE
profile. Nothing shows that the client agrees with an independent meter.

## Independent evidence: gisce/iec870ree

`iec102/interop` (build tag `interop`, `make interop-iec102`) runs the
`iec102-gisce` image of otfabric/iec60870-5-interop (contract 2.2):
gisce/iec870ree v0.14.1 (commit `993242da`, AGPL-3.0, Python), a master of
the Spanish REE profile, in a container of its own; nothing of it is linked
into this module. Until the harness release v0.3.0 publishes the image the
suite uses the local `:dev` build (`IEC102_INTEROP_GISCE_IMAGE` selects
another); afterwards it is pinned by digest in `iec102/interop/harness.go`.

The station is `iec102/server` with `ree.Profile`, `ree.Session` (key from
the fixture, `OnSetTime` setting the station's clock) and, behind it, a
handler of the suite for the REE tariff requests (`ree.TariffInfo`, one
record per ASDU, as gisce decodes only the first record of an ASDU). It
serves the harness's baseline fixture, read from the image
(`print-fixture`): measuring point address 1, three hourly periods of six
totals on record 11, absolute and incremental, with quality bits set;
tariff information of register 134; a clock that stands still until set.
Each connection gets a fresh server on a listener on `0.0.0.0:0`, which
the container reaches as `host.docker.internal`; the store and the clock
are shared.

| Check | gisce sends | Result |
|---|---|---|
| link start-up | request status of link (9), reset of remote link (0) | pass; another link address: `link-init-timeout` |
| `connect`, `connect --profile ree` | C_AC_NA_2 (cause 6, VSQ 1, key), C_FS_NA_2 (cause 6, VSQ 0) | pass: both confirmed with cause 7 |
| wrong access key | C_AC_NA_2 | pass: cause 7 with P/N, `login-refused` |
| `read-product` | C_RD_NA_2 (cause 5) | pass: P_MP_NA_2 equal to the fixture |
| `read-time` | C_TI_NA_2 (cause 5, VSQ 0) | pass: M_TI_TA_2 with the fixture's clock |
| `read-totals` absolute, incremental (in a session), periods 2..3 and addresses 1..3 | C_CI_NT_2 / C_CI_NU_2 (cause 6, record 11, first, last, two times a) | pass: confirmation, one M_IT_TG_2 / M_IT_TK_2 per period equal to the fixture (addresses, values, quality octets, period end, SU and IV 0), termination |
| refusals | C_CI_NT_2 out of the periods, of record 12, of addresses 7..8, to common address 2 | pass: causes 18, 16, 17, 16, nothing else |
| `read-tariff` current, stored (range), unknown register | C_TA_VC_2 (VSQ 0), C_TA_VM_2 (two times a) on record 134; record 135 | pass: confirmation, each record equal to the fixture, termination; cause 16 |
| `set-time`, then `read-time` | C_CS_TA_2 (cause 6, time b; SU from the Europe/Madrid rules) | pass: confirmed, the station's clock set to the time sent, read back |

Known differences: none (`iec102/interop/testdata/known-bugs.tsv`, a copy
of the harness's, is empty). Library defects found: none in the server.
The run corrected one claim of this document and one encoding of the
client (source conflict 3: VSQ 0 for REE requests without information
elements).

What it does not show: link address or common address of one octet (gisce
has two only); the read commands other than C_CI_NT_2 and C_CI_NU_2; single
points (C_SP_NB_2), blocks (C_CB_UN_2), configuration and contracted power
requests; signatures; that the REE profile as gisce implements it is the
REE profile; and anything about `iec102/client`, which has no independent
peer. gisce cannot parse the single character E5h, so a station configured
with `link.Config.SingleCharAck` would fail with it (not run: our server
answers with fixed frames by default).

## Interoperability candidates

Both open implementations are controlling stations (masters) only, so
they could only test our server:

- **gisce/iec870ree** (Python, AGPL-3.0): in use, see above. It runs as a
  separate container of the interop repository (iec60870-5-interop), never
  linked into this module. javierdelapuente/reeprotocol is its ancestor and
  not an independent second reference.
- **daiiniel/IEC60870-5-102** (C#): **no licence**, so it cannot be
  redistributed or packaged in an interop image; usable only for reading.

No IEC 102 controlled station is available as open source to test our
client against; the recorded traffic is the only evidence on that side.

## Not implemented

- M_EI_NA_2 layout (raw), the REE types marked raw above, the REE
  extension objects (150..163).
- Checking the signature octet (its computation is not documented).
- SQ = 1 for lists (decoded raw).
- Class 1 data in the server; spontaneous transmission; broadcast.
- Session enforcement in the server (reads do not need an open session).
- One controlling station polling several stations on a shared line (one
  client serves one link address).
