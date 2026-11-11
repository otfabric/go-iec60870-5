# Interoperability: otfabric/go-iec60870-5

What is implemented per companion standard, and how it was verified. IEC
60870-5-104, clause by clause with its interop results, is in
[docs/iec104/INTEROPERABILITY.md](docs/iec104/INTEROPERABILITY.md). This page
covers IEC 60870-5-101. IEC 60870-5-103 is in
[docs/iec103/INTEROPERABILITY.md](docs/iec103/INTEROPERABILITY.md), with the
results against four independent IEC 103 clients and one server
(`make interop-iec103`).

Evidence levels used below:

- **unit**: codec or procedure tests in this repository
- **e2e**: this library's client against this library's server (not
  independent)
- **independent**: a passing run against a pinned image of
  otfabric/iec60870-5-interop
- **recorded**: traffic of other implementations captured by others
  ([iec101/testdata/captures](iec101/testdata/captures/README.md), levels B
  and C only): every frame decodes and encodes back byte for byte, the link
  procedures check out, Wireshark agrees, and the recorded controlled
  stations are replayed against the client (and the stations' answers to
  recorded requests built by the server). The recordings are of an SEL RTAC
  and of three stations of a utility (Energisa), with 1-octet link address,
  COT 1, CA 1, IOA 2

Independent evidence for IEC 101 comes from `make interop-iec101`
(`iec101/interop`), run locally on 2026-11-11 against the lib60870-C v2.4.1
and cB-Sigma/iec60870 v0.3.0 images of otfabric/iec60870-5-interop
(v0.1.0, contract 2.0, images pinned by digest in `iec101/interop/harness.go`), with the
contract's defaults (1-octet link address, COT 1, CA 1, IOA 2):

- this module's client against each reference server, unbalanced and
  balanced: 4 pairings x 5 scenarios (link start-up, interrogation,
  counter interrogation, read incl. unknown IOA and unknown CA, clock
  synchronization and C_TS_NA_1, commands direct/SBO/cancel with their
  reports): all pass;
- each reference client replaying the harness's 31 IEC 101 cases against
  this module's server, unbalanced and balanced: 124 of 124 match the
  harness's expected result exactly.

The run was repeated on 2026-11-11 after the link-state changes below
(not-reset state, DFC handling, response filtering, FCB repetition by
function and content, receiver address in balanced responses): all pass,
20 of 20 client scenarios and 124 of 124 reference-client cases.

Balanced mode with distinct addresses (station 1 and 2) was run once by
hand against the lib60870-C image (`server --link-address 2
--remote-link-address 1`): lib60870-C answers with its own address and
accepts responses carrying either address, so the default of
IEC 60870-5-101:2003 6.2.1.2 (the receiver's address) interworks with it.
cB-Sigma's adapter takes one link address for both directions and cannot
show the difference. The harness itself runs equal addresses only.

Not covered by independent runs: other field sizes, 2-octet and 0-octet
link addresses, single character acknowledgement, multidrop, DFC set by
the peer. cB-Sigma's IEC 101 was debugged against
lib60870, so the two references are not fully independent of each other.

The implementation was checked clause by clause on 2026-11-11 against
IEC 60870-5-101:2003 (GOST R IEC 60870-5-101-2006, an identical
translation), which also profiles the FT 1.2 rules of IEC 60870-5-1 and the
link procedures of IEC 60870-5-2 (clause 6). Amendment 1:2015 is not held.
The deviations found and still open are listed in
[LIMITATIONS.md](LIMITATIONS.md#iec-60870-5-101). See
[docs/STANDARDS.md](docs/STANDARDS.md).

## Physical and link layer

| Item | Implemented | Evidence |
|---|---|---|
| FT 1.2 single character E5h, fixed and variable frames | yes | unit (`ft12`), fuzz, recorded |
| Checksum, start/stop, double length check | yes | unit, recorded |
| Resynchronization after noise or a broken frame | yes | unit (`TestDecoderResync`) |
| Line idle interval / inter-character gap detection | **no** | - |
| Link address size 0, 1, 2 octets | yes (0 only balanced) | unit, e2e (1 and 2), unit balanced 0 |
| Unbalanced: request status of link, reset of remote link, reset of user process | yes | unit, e2e, independent (status, reset of remote link), recorded (status) |
| Unbalanced: send/confirm, send/no reply (broadcast), request class 1/2 | yes | unit, e2e, independent (send/confirm, class 1/2), recorded (send/confirm, class 1/2) |
| Request for access demand (8) answered with status of link (11), table 3 | yes | unit (`TestAccessDemand`) |
| Reset of FCB (7): not in table 3, answered 15 unless the handler opts in (`link.FCBResetHandler`, IEC 60870-5-103) | yes | unit (`TestResetFCBOptIn`) |
| "Secondary link not reset" (figures 6, 9): only functions 9 and 0 answered until the first reset of remote link | yes | unit (`TestNotResetState`), independent |
| ACD-driven class 1 polling, DFC holds back user data | yes | unit, e2e, independent (ACD), recorded (ACD) |
| Primary waits for DFC = 0 before reset (figures 5, 8); balanced primary requests status while the peer's DFC is set | yes | unit (`TestInitWaitsForDFC`, `TestBalancedSendWaitsForDFC`) |
| "Secondary link busy" (figures 6, 9): DFC in every response while the input queue is more than half full, user data refused with NACK | yes | unit (`TestBusyRefusesUserData`), e2e (`TestServerBusySetsDFC`) |
| Balanced server sends a refused ASDU again (NACK or DFC) | yes | e2e (`TestServerBalancedResendsAfterNack`) |
| Responses that cannot answer the request (a late repetition) discarded, not taken as the answer | yes | unit (`TestLateResponseDiscarded`) |
| FCB/FCV, repetition answered with the stored response; a repetition is the same function and data with the FCB not toggled | yes | unit (`TestRepetitionNotProcessedTwice`, `TestSameFCBOtherRequest`), recorded (FCB/FCV; no repetition in the recordings) |
| Single character E5h as ACK / no-data NACK (configurable) | yes | unit, recorded (E5h to class 2 polls, by a station on an RTAC's line) |
| NACK (1), not functioning (14), not implemented (15) | yes | unit |
| Empty user data refused (`link.ErrNoData`): without an ASDU a frame is fixed-length (6.1) | yes | unit (`TestEmptyUserData`) |
| Balanced: both directions initialized, test function for link, DIR bit | yes | unit, e2e, independent |
| Balanced address field: responses carry the receiver's address (6.2.1.2); `link.Config.SenderAddr` for the own address; either accepted | yes | unit (`TestBalancedReceiverAddr`), independent (lib60870-C, by hand) |
| Multiple secondaries on one line (multidrop) polled by one client | link layer yes, `iec101/client` **no** (one link address per client) | unit (address filtering), recorded (shared RTAC line, decoded only) |

## Application layer

ASDU field sizes: cause of transmission 1 or 2 octets, common address 1 or
2, information object address 1, 2 or 3 (`asdu.Params`); every type
identification of the `asdu` package encodes under any of them. The codec
does not restrict itself to the 101 profile: the IEC 104 types 58..64
(CP56Time2a commands), C_TS_TA_1 and 127 are reserved in IEC 60870-5-101:2003
(7.2.1.1) and must not be sent on a 101 link; the 101-only C_TS_NA_1 and
C_CD_NA_1 are supported. `iec101.Params.Strict` (off by default) applies
`iec101.Validate` to every ASDU sent and received: reserved types
(7.2.1.1), cause 0 (7.2.3), common address 0 and the global address
outside C_IC/C_CI/C_CS/C_RP (7.2.4), one object at IOA 0 with SQ = 0 for
M_EI_NA_1 and the system commands except read (7.3.3, 7.3.4), and time
tags on interrogated information (7.3.1). The strict client drops and logs
what fails; the strict server mirrors a control-direction ASDU negatively
with the fitting "unknown ..." cause (7.2.3) or drops it.

| Function | Client | Server | Evidence |
|---|---|---|---|
| End of initialization (M_EI_NA_1) after the first link reset and after reset of user process | delivered to the handler | sent | e2e |
| General and group interrogation (C_IC_NA_1) | `Interrogate` | handler | e2e, independent (station; groups e2e only), recorded (station) |
| Counter interrogation (C_CI_NA_1): request (mode B) and freeze/reset commands confirmed without readings (modes C/D, 7.4.8) | `CounterInterrogate` | handler | e2e; independent for the request |
| Read (C_RD_NA_1) | `Read` | handler | e2e, independent |
| Clock synchronization (C_CS_NA_1) | `ClockSync` | handler | e2e, independent |
| Test command (C_TS_NA_1, fixed test pattern) | `TestCommand` | handler | e2e, independent |
| Reset process (C_RP_NA_1) | `ResetProcess` | handler | e2e |
| Delay acquisition (C_CD_NA_1, 7.4.12): measurement with cause activation, setting the delay with cause spontaneous | `MeasureDelay`, `SetDelay` | handler (the clock correction is the application's) | e2e |
| Process commands, direct and select-before-operate, deactivation | `Command`, `Deactivate` | handler | e2e, independent (C_SC, C_DC, C_RC, C_SE_NA/NB/NC), recorded (C_DC_NA_1 select and execute) |
| Spontaneous (class 1) and cyclic (class 2) data | handler | `Send`, `SendClass` | e2e, independent (class 1 reports), recorded (CP24Time2a and CP56Time2a types, periodic M_ME_NC_1) |
| Negative confirmations and "unknown ..." causes | `*telecontrol.NegativeError` | `Negative`, `Reject` | e2e, independent |
| 101 profile checks (`Params.Strict`) | drops and logs | mirrors with 44..47, or drops | unit (`TestValidate`), e2e (`TestStrictProfile`) |
| File transfer | **no** | **no** | - |
| IEC 62351-5 / IEC TS 60870-5-7 security | **no** | **no** | - |
