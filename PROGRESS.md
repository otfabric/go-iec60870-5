# Progress

Working log of the IEC 60870-5 program (go-iec60870-5 and
otfabric/iec60870-5-interop). Newest first. Plan: [docs/ROADMAP.md](docs/ROADMAP.md).

## 2026-11-11

State:

- Repository local only (no GitHub remote yet; visibility to be chosen by
  the owner).
- Phase 0 done: audit, standards matrix, requirements, parity, roadmap,
  reference-stack survey (in the interop repository).
- Phase 1: go-iec104 v0.3.0 carried over; all tests green; `make check`
  green; `make test-cbsigma` green. `make interop` against iec104-interop
  v0.5.0: see IEC104_PARITY.md (to be recorded).
- Phase 2: `cp`, `ft12`, `link`, `telecontrol`, `iec101` client/server
  with unit and e2e tests above the 90 % per-package floor. No
  independent evidence yet.
- Interop harness: being built (contract v2.0, IEC 101 adapters lib60870-C
  and cB-Sigma first).

Next:

1. Record the IEC 104 interop run; write the IEC 101 consumer suite
   (`iec101/interop`, build tag `interop`) against the harness images.
2. Phase 3, IEC 103 (text held): `iec103` codec for types 1-11, 20-31,
   generic services; controlling station (polling, GI, time sync, general
   commands, disturbance data transfer) and controlled station; scripted
   tests from the standard's examples; adapters ARIEC103 and
   vitusdong/iec103-dotnet in the harness.
3. Phase 4, IEC 102.

Decisions:

- Package layout deviates from the original blueprint where the standards
  do: `asdu` is the 101/104 ASDU (not a universal one), `telecontrol` holds
  the shared request procedures, `iec104/{client,server}` keep go-iec104's
  split, IEC 101 mirrors it (`iec101/{client,server}`).
- Writes in `link` go through a writer goroutine (balanced stations would
  deadlock on synchronous transports otherwise).
