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

- Phase 3: `iec103` codec, client and server done (commit b5e28e0): all
  compatible types, annex B examples, e2e incl. lossy line; coverage
  iec103 99.4 %, client 92.4 %, server 94.2 %. No independent evidence.
- IEC 104 interop of the moved packages: 31 tests / 482 subtests pass
  against iec104-interop v0.5.0 (local).

- IEC 101 independent interop (`make interop-iec101`, local): client vs
  lib60870/cbsigma servers, 4/4 pairings pass; reference clients vs our
  server 124/124 cases. Harness iec60870-5-interop a071afd (images :dev,
  unpublished).

Next:

1. Publish the harness images (owner), pin them by digest in
   iec101/interop/harness.go and add a CI job; cover non-default field
   sizes and the digitalpetri adapter.
2. IEC 103 adapters (ARIEC103, vitusdong/iec103-dotnet) in the harness and
   an `iec103/interop` consumer suite.
3. Phase 4, IEC 102.

Decisions:

- Package layout deviates from the original blueprint where the standards
  do: `asdu` is the 101/104 ASDU (not a universal one), `telecontrol` holds
  the shared request procedures, `iec104/{client,server}` keep go-iec104's
  split, IEC 101 mirrors it (`iec101/{client,server}`).
- Writes in `link` go through a writer goroutine (balanced stations would
  deadlock on synchronous transports otherwise).
