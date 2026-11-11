# Roadmap

Phases of go-iec60870-5 and otfabric/iec60870-5-interop. Status as of
2026-11-11. Detailed state and next steps: [PROGRESS.md](../PROGRESS.md).

| Phase | Scope | Status |
|---|---|---|
| 0 | Audit of go-iec104/iec104-interop/iec104ctl, architecture, standards matrix, reference-stack survey, scaffolding | done ([MIGRATION_AUDIT.md](MIGRATION_AUDIT.md), [STANDARDS.md](STANDARDS.md), interop docs/REFERENCE_STACKS.md) |
| 1 | IEC 104 in the new layout with all tests; shared `cp` and `telecontrol` | done; interop parity recorded in [IEC104_PARITY.md](IEC104_PARITY.md) |
| 2 | IEC 101: `ft12`, `link`, client and server, unbalanced and balanced | done, checked against IEC 60870-5-101:2003; interop with lib60870-C and cB-Sigma in CI; digitalpetri and non-default sizes pending |
| 3 | IEC 103: own ASDU codec (types 1-11, 20-31, generic services), protection-specific stations on `link` | done, checked against IEC 60870-5-103:1997 and compared with six implementations; interop adapters (vitusdong, ARIEC103, Open103, go-iecp5 cs103) in progress |
| 4 | IEC 102: integrated totals model | implemented without the standard's text (vendor documentation, recorded meter traffic); a gisce/iec870ree master adapter is next; no open controlled station exists |
| 5 | Interop and conformance coverage: all references, negative cases, capture corpus | capture corpora for all four standards in v0.2.0; ongoing |
| 6 | Hardening: performance, API review, security review, real devices | later |

## Open decisions

- Whether the IEC 104 client moves onto `telecontrol.Requester`.
- Whether IEC 101 gets file transfer and IEC TS 60870-5-7 security
  (`sec`), and in which order.
- Multidrop: one IEC 101 client polling several link addresses.
- A CLI for the module (not in scope now).
- Deprecation of go-iec104 (explicitly out of scope; decided later).
