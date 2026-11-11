# Standards coverage

Which parts of IEC 60870-5 this module implements, which editions we hold
the text of, and how sure each claim is. Three kinds of claim are kept
apart throughout the documentation:

1. **implemented**: the code does it and tests in this repository exercise it
2. **interoperable**: a passing run against an independent implementation
   pinned in an interop harness
3. **conformant**: formally tested against the standard by an accredited
   party. **Nothing in this module is.**

## Texts we hold

| Document | Edition held | Form | Notes |
|---|---|---|---|
| IEC 60870-5-103 | 1997 (ed. 1) | GOST R IEC 60870-5-103-2005, "IDT" (identical translation, Russian) | Clause 6 quotes the IEC 60870-5-1 FT 1.2 rules and the IEC 60870-5-2 unbalanced procedures that 103 uses; this is our only text for parts 1 and 2. |
| IEC 60870-5-101 | 2003 (ed. 2) | GOST R IEC 60870-5-101-2006, "IDT" (identical translation, Russian) | Obtained 2026-11-11; `ft12`, `link`, `iec101`, `telecontrol` checked against it, open deviations in LIMITATIONS.md. Clause 6 profiles IEC 60870-5-1 and -2, including the balanced procedures (figures 7 to 9). Amendment 1:2015 not held. |
| IEC TS 60870-5-7 | 2025 (ed. 2.0) | PD IEC TS 60870-5-7:2025 (English) | Obtained 2026-11-11; `sec` checked against it, gaps in SECURITY.md. IEC 62351-5:2023, to which it delegates algorithms and procedures, is not held. |
| IEC 60870-5-104 | 2000 (ed. 1) | GOST R IEC 60870-5-104-2004, "full authentic text" of IEC 60870-5-104:2000 (Russian) | `iec104/apci`, `iec104/internal/link`, `iec104/client`, `iec104/server` and `asdu` as 104 uses it checked clause by clause against it on 2026-11-11 (docs/iec104/INTEROPERABILITY.md); five deviations fixed. Ed. 2 (2006) and Amd. 1 (2016) are not held: the code was written against ed. 2 by go-iec104, and what ed. 2 adds or changes (STOPDT con after acknowledgement, redundancy clause 10, type 127) is marked "ed. 2 behaviour, not verifiable from the held text". |

Everything else below is **not held**. Behaviour that depends on it is
marked "interpretation" and rests on the extracts quoted by the held texts,
on the documented and observed behaviour of independent implementations
(lib60870-C, cB-Sigma, digitalpetri, j60870) and, for IEC 104, on the
interoperability results.

## Matrix

| Standard | Subject | Status in this module | Text held | Basis |
|---|---|---|---|---|
| IEC 60870-5-1 | Transmission frame formats | FT 1.2 implemented (`ft12`); other formats (FT 1.1, 2, 3) not needed by any companion standard here | no (FT 1.2 rules profiled by 101 cl. 6.1 and 103 cl. 6.1) | 101 cl. 6.1; 103 cl. 6.1, 6.2.1; 33-bit idle after errors not implemented |
| IEC 60870-5-2 | Link transmission procedures | unbalanced and balanced (`link`) | no (profiled by 101 cl. 6.2, both modes, and 103 cl. 6.2-6.3) | 101 cl. 6.2 and figures 5-9; 103 cl. 6.2, 6.3 |
| IEC 60870-5-3 | General structure of application data | via the ASDU codecs | no | 101/104 ASDU layout as implemented by go-iec104; 103 cl. 7.1 |
| IEC 60870-5-4 | Application information elements | `cp` time tags; elements in `asdu` | no | 103 cl. 7.2 (CP32Time2a, CP56Time2a definitions); go-iec104 for the rest |
| IEC 60870-5-5 | Basic application functions | station initialization, interrogation, read, clock sync, commands, counters, test, file transfer (104) | no | 104 cl. 7 and 9 (held, ed. 1); 103 cl. 7.4 |
| IEC 60870-5-101 (2003 + Amd. 1 2015) | Telecontrol, serial | implemented (`iec101`) | **2003 yes**, Amd. 1 no | checked against the 2003 text; open deviations in LIMITATIONS.md; interop against lib60870-C, cB-Sigma and digitalpetri/iec60870, default and four other sets of field sizes |
| IEC 60870-5-102 (1996) | Integrated totals | implemented (`iec102`, `iec102/client`, `iec102/server`; REE profile opt-in in `iec102/ree`) | **no** | Beckhoff TS650x manual (vendor documentation), recorded Actaris SL762B traffic, gisce/iec870ree vectors; classification in docs/iec102/INTEROPERABILITY.md; interop: the gisce/iec870ree controlling station (REE profile) against the server only |
| IEC 60870-5-103 (1997) | Protection equipment | implemented (`iec103`); see docs/iec103/INTEROPERABILITY.md for interpretations | **yes** | checked against the 1997 text; clause references cite the GOST R translation's numbering, which follows the IEC text; interop: four independent clients against the server, the client against one deviating server |
| IEC 60870-5-104 (2006 + Amd. 1 2016) | Telecontrol over TCP/IP | implemented (`iec104`) | ed. 1 (2000) only | **checked against ed. 1** clause by clause (docs/iec104/INTEROPERABILITY.md); ed. 2 differences marked, not verified; interop against five independent stacks (lib60870-C, j60870, wendy512/go-iecp5, cB-Sigma, part5) |
| IEC 60870-5-6 | Conformance testing guidelines | not used | no | - |
| IEC TS 60870-5-601 | Conformance test cases for 101 | not used | no | - |
| IEC TS 60870-5-604 | Conformance test cases for 104 | not used | no | - |
| IEC TS 60870-5-7:2025 | Security extensions for 101/104 | 104 only (`sec`) | **yes** | framing checked against the text (SECURITY.md); differential tests against cB-Sigma |
| IEC 62351-3 | TLS profile | 104 only (TLS 1.2/1.3 via crypto/tls) | no | see SECURITY.md |
| IEC 62351-5:2023 | Application-layer security | 104 only (`sec`) | no | see SECURITY.md |

## What the held IEC 103 text settles for the shared layers

From GOST R IEC 60870-5-103-2005 clause 6 (numbering as in IEC 60870-5-103):

- 6.1: only FT 1.2 is allowed, fixed and variable frames and the single
  character E5h; user data at most 255 octets.
- 6.2.1: the single character A2h is not used.
- 6.2.2: all three transmission services S1, S2, S3 are used.
- 6.2.3: unbalanced only; the control system is always primary; RES bit
  not used; function codes PRM=1: 0, 3, 4, 9, 10, 11; PRM=0: 0, 1, 8, 9,
  11; the address field is always one octet; broadcast address 255 (send/no
  reply only).
- 6.2.4: loop delay T_LD 50 ms; 9.6 or 19.2 kbit/s.
- 6.3: function code 7 (PRM=1) resets FCB, with FCB and FCV 0; function
  14 and 15 answers toggle the expected FCB; DFC set at most 15 s.

These are implemented as options and hooks of `link` (address size 1,
`ResetFCB`, `FCBResetHandler`, `FlowControlHandler`, `FunctionFilter`) and
enforced by the IEC 103 stations, not by `link` itself. The 15 s DFC limit
is not enforced.
