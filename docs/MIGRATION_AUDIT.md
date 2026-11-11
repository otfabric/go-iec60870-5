# Migration audit: go-iec104, iec104-interop, iec104ctl

Audit of the three existing IEC 104 repositories (read only; none was
modified), made 2026-11-10/11 at go-iec104 `4a375b3` (v0.3.0),
iec104-interop v0.6.0 and iec104ctl (on go-iec104 v0.3.0). It records what
was reused in go-iec60870-5 and otfabric/iec60870-5-interop and what
remains specific to IEC 104. It is a dated record: where the module has
moved on since, section 9 says so.

## 1. Inventory of go-iec104

| Package | Lines (Go, incl. tests) | Content | Maturity |
|---|---|---|---|
| root `iec104` | ~400 | sentinel errors, `NegativeError`, `State`, `Logger`/`FieldLogger`/`ContextLogger`, `Metrics` | released, documented |
| `apci` | ~1,000 | APDU framing, I/S/U control fields, 15-bit sequence arithmetic, parameters k, w, t0..t3 | released, fuzzed |
| `asdu` | ~3,300 | ASDU model and codec, 67 type identifications plus the 101-only C_TS_NA_1 and C_CD_NA_1, configurable COT/CA/IOA sizes, time tags | released, fuzzed, verified against recorded traffic and Wireshark |
| `client` | ~6,000 | controlling station: STARTDT/STOPDT, requests, SBO, retries, reconnect, redundancy group, file transfer | released, interop with 4 stacks (iec104-interop v0.5.0) |
| `server` | ~4,700 | controlled station: sessions, Mux, redundancy groups and event queue, file server, security | released, interop |
| `sec` | ~4,600 | IEC 62351-5:2023 / IEC TS 60870-5-7:2025 A-profile: association, session keys, HMAC/AES-GCM, RBAC, statistics | released, differential tests against cB-Sigma |
| `internal/link` | ~3,100 | the APCI protocol machine for one TCP connection, security in the loop | released |
| `internal/capture` | ~3,000 | pcap/pcapng/zeek replay and Wireshark oracle of recorded traffic | test infrastructure |
| `internal/station`, `internal/testutil`, `internal/logx` | ~1,000 | fixture station, certificates, logging helper | test/infra |
| `e2e` | ~5,700 | end-to-end, hostile peers, chaos, state machine, fuzz | test |
| `interop` (+ `interop/cbsigma` module) | ~3,400 | consumer suite against iec104-interop v0.5.0 images; in-process cB-Sigma differential tests | test |

Total ~39,800 lines of Go; 333 test/fuzz/benchmark/example functions.

## 2. Reused in go-iec60870-5

Everything was copied (MIT, same owner), see
[IEC104_PARITY.md](IEC104_PARITY.md) for the package map. Proven
functionality kept as is: APCI, client, server, file transfer,
redundancy, TLS, `sec`, the ASDU codec, the captures corpus and the
interop suite.

## 3. Reusable across companion standards (and how it was reused)

| Component | Reuse | Where |
|---|---|---|
| ASDU codec with configurable field sizes | IEC 101 uses it unchanged | `asdu` |
| CP24Time2a / CP56Time2a | extracted, plus CP16/CP32 for 101 and 103 | `cp` |
| Request matching (mirrors, interrogation collection, read, rejection) | re-expressed transport-independently for 101 | `telecontrol` (the 104 client keeps its own copy for now) |
| `NegativeError` semantics | same rules, new type `telecontrol.NegativeError` | `telecontrol` |
| Logger interfaces | shared by all stations | root `iec60870` |
| `sec` (IEC TS 60870-5-7 covers 101 and 104) | not yet wired into 101 | `sec` |
| File transfer procedures (101 cl. 7.4.11 and 104) | not yet wired into 101 | `iec104/client`, `iec104/server` |

## 4. Remains IEC 104-specific

APCI and its timers, k/w windows, STARTDT/STOPDT, TCP connection handling,
redundancy groups (104 cl. 10), TLS transport security, the multi-session
server. None of it applies to a serial link.

## 5. Not reusable for IEC 102 and IEC 103

The telecontrol ASDU model (`asdu`) does not fit IEC 103 (function type and
information number addressing, a fixed one-octet layout, its own type and
cause tables: 103 cl. 7.1-7.3) or IEC 102 (integrated totals records,
tariffs, its own type identifications). Both get their own codecs; only
`cp`, `ft12` and `link` are shared.

## 6. iec104-interop

Reused as the architecture of otfabric/iec60870-5-interop: the container
contract (server / client <op> / print-capabilities / print-fixture /
version, exit codes 0..4, JSON documents, readiness file), fixtures and
schemas, `versions.yaml` with digest pins and candidate builds, smoke and
N×N cross-stack scripts, interop-result-v1 reports, the release and
upstream workflows, and the adapter sources of the five IEC 104 stacks
(lib60870-C, OpenMUC j60870, wendy512/iec104, cB-Sigma/iec60870,
pascaldekloe/part5). Adapters are GPL-3.0-or-later in an MIT repository;
the split is kept. IEC 104-specific parts of the contract (port 2404, APCI
flags, STARTDT results, redundancy, TLS) are generalised for the serial
standards, which carry the FT 1.2 byte stream over one TCP connection.

## 7. iec104ctl

A cobra CLI (client read/interrogate/counters/command/clock/reset/
monitor/ping/scan/types/file, server start/generate-config, discover) on
go-iec104 v0.3.0, importing its root, `apci`, `asdu`, `client`, `server`
and `sec`. It is untouched. A CLI for the new module is out of scope; if
one is built, the import map in IEC104_PARITY.md is what changes.

## 8. Compatibility concerns

- Consumers of go-iec104 are unaffected: nothing there changed.
- Moving a consumer to this module changes import paths and the logger
  constructors (`iec104.NewSlogLogger` -> `iec60870.NewSlogLogger`);
  everything else keeps its name.
- Two copies of the IEC 104 code now exist. A fix in one must be ported to
  the other until go-iec104 is deprecated (a separate decision).

## 9. Known limitations and technical debt carried over

The current list for the `iec104` packages (go-iec104's, less the five
deviations fixed after the edition 1 check) is in
[iec104/LIMITATIONS.md](iec104/LIMITATIONS.md). Debt noticed in this audit:

- the IEC 104 client has its own request-matching code, duplicated in
  `telecontrol`;
- the IEC 104 interop suite pinned iec104-interop v0.5.0, while
  iec104-interop v0.6.0 added part5. Resolved in v0.3.0: the suite runs
  against the IEC 104 images of otfabric/iec60870-5-interop v0.2.1, part5
  included;
- `asdu` names some 101-only behaviour in IEC 104 terms (for example
  `asdu.IEC104` params, `MaxSize` default 249).

## 10. Licensing

go-iec104, iec104-interop (repository) and iec104ctl are MIT, copyright OT
Fabric: code moves freely. iec104-interop adapters are GPL-3.0-or-later
and stay in the interop repository. No third-party source was copied into
go-iec60870-5. Reference stacks studied for behaviour (lib60870-C GPL-3.0,
cB-Sigma Apache-2.0) were read, not copied.
