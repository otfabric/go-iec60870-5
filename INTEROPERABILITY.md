# Interoperability: otfabric/go-iec60870-5

What is implemented per companion standard, and how it was verified. IEC
60870-5-104, clause by clause with its interop results, is in
[docs/iec104/INTEROPERABILITY.md](docs/iec104/INTEROPERABILITY.md). This page
covers IEC 60870-5-101.

Evidence levels used below:

- **unit**: codec or procedure tests in this repository
- **e2e**: this library's client against this library's server (not
  independent)
- **independent**: a passing run against a pinned image of
  otfabric/iec60870-5-interop

No IEC 101 behaviour has independent evidence yet: the adapters
(lib60870-C, cB-Sigma, digitalpetri) are being built in
otfabric/iec60870-5-interop. Until then every row below is unit or e2e
only.

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
| Unbalanced: request status of link, reset of remote link, reset of user process | yes | unit, e2e |
| Unbalanced: send/confirm, send/no reply (broadcast), request class 1/2 | yes | unit, e2e |
| ACD-driven class 1 polling, DFC holds back user data | yes | unit, e2e |
| FCB/FCV, repetition answered with the stored response | yes | unit (`TestRepetitionNotProcessedTwice`) |
| Single character E5h as ACK / no-data NACK (configurable) | yes | unit |
| NACK (1), not functioning (14), not implemented (15) | yes | unit |
| Balanced: both directions initialized, test function for link, DIR bit | yes | unit, e2e |
| Multiple secondaries on one line (multidrop) polled by one client | link layer yes, `iec101/client` **no** (one link address per client) | unit (address filtering) |

## Application layer

ASDU field sizes: cause of transmission 1 or 2 octets, common address 1 or
2, information object address 1, 2 or 3 (`asdu.Params`); every type
identification of the `asdu` package (IEC 104's 67 plus the 101-only
C_TS_NA_1 and C_CD_NA_1) encodes under any of them.

| Function | Client | Server | Evidence |
|---|---|---|---|
| End of initialization (M_EI_NA_1) after the first link reset and after reset of user process | delivered to the handler | sent | e2e |
| General and group interrogation (C_IC_NA_1) | `Interrogate` | handler | e2e |
| Counter interrogation (C_CI_NA_1) | `CounterInterrogate` | handler | e2e |
| Read (C_RD_NA_1) | `Read` | handler | e2e |
| Clock synchronization (C_CS_NA_1) | `ClockSync` | handler | e2e |
| Test command (C_TS_NA_1, fixed test pattern) | `TestCommand` | handler | e2e |
| Reset process (C_RP_NA_1) | `ResetProcess` | handler | e2e |
| Delay acquisition (C_CD_NA_1) | `DelayAcquisition` | handler | e2e |
| Process commands, direct and select-before-operate, deactivation | `Command`, `Deactivate` | handler | e2e |
| Spontaneous (class 1) and cyclic (class 2) data | handler | `Send`, `SendClass` | e2e |
| Negative confirmations and "unknown ..." causes | `*telecontrol.NegativeError` | `Negative`, `Reject` | e2e |
| File transfer | **no** | **no** | - |
| IEC 62351-5 / IEC TS 60870-5-7 security | **no** | **no** | - |
