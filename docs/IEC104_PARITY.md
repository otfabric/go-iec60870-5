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

No protocol code was rewritten.

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
| APCI: I/S/U frames, 15-bit sequence numbers, k/w, t0..t3 | same code | none |
| Client: STARTDT/STOPDT, interrogation, counter interrogation, read, clock sync, commands with SBO, test, reset process, retries, reconnect | same code | none |
| Server: sessions, Mux routing and rejection, spontaneous data, broadcast | same code | none |
| Redundancy groups and event queue | same code | none |
| File transfer (GetFile, PutFile, ListFiles, FileServer) | same code | none |
| ASDU codec, 67 type identifications plus C_TS_NA_1/C_CD_NA_1, configurable sizes | same code; time tags in `cp` | `asdu.Timestamp` is now an alias (assignable both ways; no API break for users of `asdu`) |
| TLS (IEC 62351-3) | same code | none |
| IEC 62351-5 / TS 60870-5-7 application-layer security | same code | none |
| Structured logging, metrics hooks | same code | logger constructors under `iec60870.` instead of `iec104.` |

## Tests

| | go-iec104 v0.3.0 | go-iec60870-5 (iec104 subtree) |
|---|---|---|
| Test, fuzz, benchmark and example functions, whole module | 333 | 371 (all 333 carried over; 38 new for cp, ft12, link, telecontrol, iec101) |
| Interop functions (build tag `interop`) | 37 | 37, pinned to the same iec104-interop v0.5.0 digests |
| Committed captures | as listed in SHA256SUMS | identical files and SHA256SUMS |
| `make check` (lint, vet, race, coverage floors, examples, docs) | green | green (2026-11-11, local) |
| `make interop` against iec104-interop v0.5.0 images (lib60870, openmuc, wendy512, cbsigma, same digests) | green in go-iec104 CI | green, local 2026-11-11 (Docker linux/arm64): 31 top-level tests, 482 subtests passed, 0 failed; 22 subtests skipped where an image does not declare the feature (openmuc: read; wendy512: time-tagged commands, upload, redundancy, TLS server); run with `IEC104_REQUIRE_INTEROP=1`, 381 s |
| `make test-cbsigma` | green | green (local, 2026-11-11) |

## Known limitations

Those of go-iec104 v0.3.0, unchanged: [iec104/LIMITATIONS.md](iec104/LIMITATIONS.md).

## Open items before parity can be called demonstrated

- Run the same suite in CI (it ran locally only).
- Move the interop suite onto otfabric/iec60870-5-interop images once that
  harness publishes IEC 104 adapters with the same coverage.
- Decide whether `iec104/client` adopts `telecontrol.Requester` (shared with
  IEC 101) — only with the full IEC 104 suite green before and after.
