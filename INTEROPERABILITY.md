# Interoperability: otfabric/go-iec60870-5

What is implemented per companion standard, and how it was verified. IEC
60870-5-104, clause by clause with its interop results, is in
[docs/iec104/INTEROPERABILITY.md](docs/iec104/INTEROPERABILITY.md). This page
covers IEC 60870-5-101. IEC 60870-5-103 is in
[docs/iec103/INTEROPERABILITY.md](docs/iec103/INTEROPERABILITY.md); no
independent IEC 103 implementation has been tested against it yet.

Evidence levels used below:

- **unit**: codec or procedure tests in this repository
- **e2e**: this library's client against this library's server (not
  independent)
- **independent**: a passing run against a pinned image of
  otfabric/iec60870-5-interop

Independent evidence for IEC 101 comes from `make interop-iec101`
(`iec101/interop`), run locally on 2026-11-11 against the lib60870-C v2.4.1
and cB-Sigma/iec60870 v0.3.0 images of otfabric/iec60870-5-interop
(contract 2.0, commit a071afd, local builds, not yet published), with the
contract's defaults (1-octet link address, COT 1, CA 1, IOA 2):

- this module's client against each reference server, unbalanced and
  balanced: 4 pairings x 5 scenarios (link start-up, interrogation,
  counter interrogation, read incl. unknown IOA and unknown CA, clock
  synchronization and C_TS_NA_1, commands direct/SBO/cancel with their
  reports): all pass;
- each reference client replaying the harness's 31 IEC 101 cases against
  this module's server, unbalanced and balanced: 124 of 124 match the
  harness's expected result exactly.

Not covered by independent runs: other field sizes, 2-octet and 0-octet
link addresses, balanced mode with distinct addresses, single character
acknowledgement, multidrop. cB-Sigma's IEC 101 was debugged against
lib60870, so the two references are not fully independent of each other.

We do not hold the text of IEC 60870-5-1, -2 or -101; the behaviour below
follows the extracts of IEC 60870-5-1 and -2 quoted in IEC 60870-5-103
clause 6 (GOST R IEC 60870-5-103-2005, an identical translation) and the
documented behaviour of lib60870-C. See [docs/STANDARDS.md](docs/STANDARDS.md).

## Physical and link layer

| Item | Implemented | Evidence |
|---|---|---|
| FT 1.2 single character E5h, fixed and variable frames | yes | unit (`ft12`), fuzz |
| Checksum, start/stop, double length check | yes | unit |
| Resynchronization after noise or a broken frame | yes | unit (`TestDecoderResync`) |
| Line idle interval / inter-character gap detection | **no** | - |
| Link address size 0, 1, 2 octets | yes (0 only balanced) | unit, e2e (1 and 2), unit balanced 0 |
| Unbalanced: request status of link, reset of remote link, reset of user process | yes | unit, e2e, independent (status, reset of remote link) |
| Unbalanced: send/confirm, send/no reply (broadcast), request class 1/2 | yes | unit, e2e, independent (send/confirm, class 1/2) |
| ACD-driven class 1 polling, DFC holds back user data | yes | unit, e2e, independent (ACD) |
| FCB/FCV, repetition answered with the stored response | yes | unit (`TestRepetitionNotProcessedTwice`) |
| Single character E5h as ACK / no-data NACK (configurable) | yes | unit |
| NACK (1), not functioning (14), not implemented (15) | yes | unit |
| Balanced: both directions initialized, test function for link, DIR bit | yes | unit, e2e, independent |
| Multiple secondaries on one line (multidrop) polled by one client | link layer yes, `iec101/client` **no** (one link address per client) | unit (address filtering) |

## Application layer

ASDU field sizes: cause of transmission 1 or 2 octets, common address 1 or
2, information object address 1, 2 or 3 (`asdu.Params`); every type
identification of the `asdu` package (IEC 104's 67 plus the 101-only
C_TS_NA_1 and C_CD_NA_1) encodes under any of them.

| Function | Client | Server | Evidence |
|---|---|---|---|
| End of initialization (M_EI_NA_1) after the first link reset and after reset of user process | delivered to the handler | sent | e2e |
| General and group interrogation (C_IC_NA_1) | `Interrogate` | handler | e2e, independent (station; groups e2e only) |
| Counter interrogation (C_CI_NA_1) | `CounterInterrogate` | handler | e2e, independent |
| Read (C_RD_NA_1) | `Read` | handler | e2e, independent |
| Clock synchronization (C_CS_NA_1) | `ClockSync` | handler | e2e, independent |
| Test command (C_TS_NA_1, fixed test pattern) | `TestCommand` | handler | e2e, independent |
| Reset process (C_RP_NA_1) | `ResetProcess` | handler | e2e |
| Delay acquisition (C_CD_NA_1) | `DelayAcquisition` | handler | e2e |
| Process commands, direct and select-before-operate, deactivation | `Command`, `Deactivate` | handler | e2e, independent (C_SC, C_DC, C_RC, C_SE_NA/NB/NC) |
| Spontaneous (class 1) and cyclic (class 2) data | handler | `Send`, `SendClass` | e2e, independent (class 1 reports) |
| Negative confirmations and "unknown ..." causes | `*telecontrol.NegativeError` | `Negative`, `Reject` | e2e, independent |
| File transfer | **no** | **no** | - |
| IEC 62351-5 / IEC TS 60870-5-7 security | **no** | **no** | - |
