# IEC 104 parity with go-iec104

How the `iec104` packages of this module compare with
[go-iec104](https://github.com/otfabric/go-iec104) v0.3.0 (commit
`4a375b3`), from which they were copied on 2026-11-10. go-iec104 is not
modified and not imported.

## Method

`git archive` of go-iec104 at `4a375b3`, then mechanical changes only:

1. packages moved (table below) and import paths rewritten;
2. the `Logger` interfaces moved from the go-iec104 root package to the
   root package `iec60870` of this module;
3. CP24Time2a/CP56Time2a encoding moved from `asdu` to the shared `cp`
   package, with `asdu.Timestamp` an alias of `cp.Timestamp` and the
   unexported helpers kept as wrappers (same octets, same tests);
4. "go-iec104" renamed to "go-iec60870-5" in comments, test messages and
   the capture manifest description.

No protocol code was rewritten in the copy. Afterwards (2026-11-11) the
packages were checked clause by clause against IEC 60870-5-104 edition 1,
and five deviations were fixed (see Capabilities and
[iec104/INTEROPERABILITY.md](iec104/INTEROPERABILITY.md#checked-against-iec-60870-5-104-edition-1));
go-iec104 v0.3.0 still has them.

## Package map

| go-iec104 | go-iec60870-5 |
|---|---|
| `github.com/otfabric/go-iec104` (errors, `State`, `Metrics`, `Logger`) | `iec104` (errors, `State`, `Metrics`); `Logger`, `FieldLogger`, `ContextLogger`, `NopLogger`, `NewStdLogger`, `NewSlogLogger`, `NewSlogFieldLogger` in root `iec60870` |
| `apci` | `iec104/apci` |
| `asdu` | `asdu` (time tags via `cp`) |
| `client` | `iec104/client` |
| `server` | `iec104/server` |
| `sec` | `sec` |
| `internal/link`, `internal/capture`, `internal/station`, `internal/testutil` | `iec104/internal/...` |
| `internal/logx` | `internal/logx` |
| `e2e`, `interop`, `interop/cbsigma` (own module), `examples` | `iec104/e2e`, `iec104/interop`, `iec104/interop/cbsigma` (own module), `iec104/examples` |
| `testdata/captures` | `iec104/testdata/captures` (byte-identical, SHA256SUMS verified by `make captures-verify`) |

## Capabilities

| Capability (go-iec104 v0.3.0) | go-iec60870-5 | Difference |
|---|---|---|
| APCI: I/S/U frames, 15-bit sequence numbers, k/w, t0..t3 | same code, two fixes | an I frame without ASDU is refused (ed. 1 clause 5); `w` may exceed `k` (1..32767 each, ed. 1 5.5) |
| Client: STARTDT/STOPDT, interrogation, counter interrogation, read, clock sync, commands with SBO, test, reset process, retries, reconnect | same code, one fix | the TSC of C_TS_TA_1 starts at 0 (ed. 1 8.8) |
| Server: sessions, Mux routing and rejection, spontaneous data, broadcast | same code, one fix | `Mux` refuses a deactivation of C_CS, C_RP, C_TS and P_ME with cause 45 (ed. 1 7.1, 7.6, 7.9, 7.10, 8.8) |
| Redundancy groups and event queue | same code | none |
| File transfer (GetFile, PutFile, ListFiles, FileServer) | same code | none |
| ASDU codec, 67 type identifications plus C_TS_NA_1/C_CD_NA_1, configurable sizes | same code; time tags in `cp` | `asdu.Timestamp` is now an alias (assignable both ways; no API break for users of `asdu`) |
| TLS (IEC 62351-3) | same code | none |
| IEC 62351-5 / TS 60870-5-7 application-layer security | same code | none |
| Structured logging, metrics hooks | same code | logger constructors under `iec60870.` instead of `iec104.` |

## Tests

Counts and runs below are dated; they are history, not a live status. The
current interop pins and results are in
[iec104/INTEROPERABILITY.md](iec104/INTEROPERABILITY.md#verification-status).

| | go-iec104 v0.3.0 | go-iec60870-5 (iec104 subtree) |
|---|---|---|
| Test, fuzz, benchmark and example functions, whole module, at the copy (2026-11-10) | 333 | 371 (all 333 carried over; 38 new for cp, ft12, link, telecontrol, iec101) |
| Interop test functions (build tag `interop`) | 37, as counted at the copy | at v0.3.0: 31 top-level tests in `iec104/interop`, pinned to the IEC 104 images of iec60870-5-interop v0.3.0 (five references, part5 added), and 5 in the `iec104/interop/cbsigma` module |
| Committed captures | as listed in SHA256SUMS | identical files and SHA256SUMS |
| `make check` (lint, vet, race, coverage floors, examples, docs) | green | green (2026-11-11, local) |
| History: `make interop` against iec104-interop v0.5.0 images (lib60870, openmuc, wendy512, cbsigma, same digests) | green in go-iec104 CI | green, local 2026-11-11 (Docker linux/arm64): 31 top-level tests, 482 subtests passed, 0 failed; 22 subtests skipped where an image does not declare the feature (openmuc: read; wendy512: time-tagged commands, upload, redundancy, TLS server); run with `IEC104_REQUIRE_INTEROP=1`, 381 s. These were the pins of v0.2.0, whose Interop workflow also ran green in CI; replaced in v0.3.0 by the next row |
| `make interop` against the IEC 104 images of iec60870-5-interop v0.3.0 (lib60870, openmuc, wendy512, cbsigma, part5), the pins of v0.3.0 | not run (go-iec104 pins iec104-interop) | green, local 2026-11-11 (Docker linux/arm64), after the ed. 1 fixes: 31 top-level tests, 519 subtests passed, 0 failed, 35 skipped where an image does not declare the feature (openmuc: read, the known j60870 bug; wendy512: time-tagged commands, file download, upload, redundancy, TLS; part5: file transfer, TLS, redundancy). Passed per reference: lib60870 112, openmuc 107, wendy512 88, cbsigma 132, part5 68. `IEC104_REQUIRE_INTEROP=1`, 444 s |
| `make test-cbsigma` | green | green (local, 2026-11-11) |

## Known limitations

Those of go-iec104 v0.3.0 apart from the five deviations fixed above; the
current list is [iec104/LIMITATIONS.md](iec104/LIMITATIONS.md).

## Open items before parity can be called demonstrated

- History: moving the interop suite onto otfabric/iec60870-5-interop
  images was done on 2026-11-11 (v0.2.1, with part5 as a fifth reference;
  v0.3.0 since the library's v0.3.0, green in the Interop workflow on the
  v0.3.0 release commit).
- Decide whether `iec104/client` adopts `telecontrol.Requester` (shared with
  IEC 101), only with the full IEC 104 suite green before and after.
