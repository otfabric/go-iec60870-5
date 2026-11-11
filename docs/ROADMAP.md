# Roadmap

Phases of go-iec60870-5 and otfabric/iec60870-5-interop. Status as of
2026-11-11. Detailed state and next steps: [PROGRESS.md](../PROGRESS.md).

| Phase | Scope | Status |
|---|---|---|
| 0 | Audit of go-iec104/iec104-interop/iec104ctl, architecture, standards matrix, reference-stack survey, scaffolding | done ([MIGRATION_AUDIT.md](MIGRATION_AUDIT.md), [STANDARDS.md](STANDARDS.md), interop docs/REFERENCE_STACKS.md) |
| 1 | IEC 104 in the new layout with all tests; shared `cp` and `telecontrol` | code and tests done; `make interop` run pending record in [IEC104_PARITY.md](IEC104_PARITY.md) |
| 2 | IEC 101: `ft12`, `link`, client and server, unbalanced and balanced | implemented with unit and e2e tests; independent interop (lib60870-C, cB-Sigma, digitalpetri) in progress in the interop harness |
| 3 | IEC 103: own ASDU codec (types 1-11, 20-31, generic services), protection-specific stations on `link` | implemented with unit, annex B and e2e tests; independent interop (ARIEC103, vitusdong/iec103-dotnet) next |
| 4 | IEC 102: integrated totals model | after 103; no open controlled-station reference exists, only REE-profile masters (gisce/iec870ree) |
| 5 | Interop and conformance coverage: all references, negative cases, capture corpus | ongoing |
| 6 | Hardening: performance, API review, security review, real devices | later |

## Open decisions

- Whether the IEC 104 client moves onto `telecontrol.Requester`.
- Whether IEC 101 gets file transfer and IEC TS 60870-5-7 security
  (`sec`), and in which order.
- Multidrop: one IEC 101 client polling several link addresses.
- A CLI for the module (not in scope now).
- Deprecation of go-iec104 (explicitly out of scope; decided later).
