# Progress

Working log of the IEC 60870-5 program (go-iec60870-5 and
otfabric/iec60870-5-interop). Newest first. Plan: [docs/ROADMAP.md](docs/ROADMAP.md).

## v0.4.0 (released 2026-11-12)

- IEC 101 interop against digitalpetri/iec60870 as third reference and
  with four non-default sets of field sizes (iec60870-5-interop contract
  2.3; iec60870-5-interop v0.4.0, released 2026-11-12, pinned by digest
  in every interop suite). The
  balanced digitalpetri station is a known upstream bug
  (`digitalpetri-balanced-server-no-link-reset`).
- IEC 103 interop for generic services and disturbance data (contract 2.3):
  the server's disturbance transfer evidenced by Open103 (values: known bug
  `open103-asdu30-nfe`); generic services, the disturbance list and the
  client side have no conforming reference (vitusdong only, with known bugs
  on every pairing); `known-bugs-consumer.tsv`.
- `iec104/client`: a counter freeze or reset returns at the confirmation;
  the matching rules of `telecontrol` instead of its own copy, not the
  `Requester` (decided, docs/IEC104_PARITY.md). `telecontrol`: the C_TS_TA_1
  counter starts at 0.
- SAST: `internal/testutil/serialline` G115 fixed, G404 triaged as a false
  positive; 0 findings, also in the nested cbsigma module.
- Vetting on release/v0.4.0: e2e of all four standards, `make fuzz` (20
  targets, 30 s), `make chaos` (4 standards, 20 seeds, 30 s), `make check`;
  every interop suite on the pinned v0.4.0 images, 0 failures.
- Next: an IEC 102 client peer (no open controlled station exists); the
  open deviations ([LIMITATIONS.md](LIMITATIONS.md)); real devices; the
  IEC 103 open items (a server serving the fixture's disturbance values,
  generic reads by heading, group or directory, the generic interrogation,
  writes with confirmation or abort, aborts and more than one record).

## v0.3.0 (released 2026-11-11)

- Hardening: SAST (gosec, opengrep) 44 findings to 0; hostile-peer, chaos
  and fuzz suites for IEC 101, 102 and 103 and the defects they found
  (link `FrameGap`, stray answers ending as a time-out, requests ending at
  their deadline, `WaitUp` after `OnState`, IEC 103 broadcast
  `ErrLinkDown`); the one-bit FCB limit documented.
- IEC 104 checked clause by clause against edition 1 (2000), four fixes;
  the interop suite moved to the IEC 104 images of iec60870-5-interop
  with pascaldekloe/part5 as fifth reference. All suites pin the
  iec60870-5-interop v0.3.0 images.
- IEC 102: first independent evidence, gisce/iec870ree (REE profile)
  against `iec102/server` (`make interop-iec102`, contract 2.2), on the
  image of iec60870-5-interop v0.3.0 pinned by digest.
- Next: IEC 101
  digitalpetri and non-default field sizes; IEC 103 generic services and
  disturbance data in the harness contract; the open deviations
  ([LIMITATIONS.md](LIMITATIONS.md)).

## 2026-11-11 (v0.2.0)

- v0.2.0: IEC 60870-5-102 (from vendor documentation and recorded meter
  traffic; the standard's text is not held), the conformance fixes from
  checking IEC 101:2003, IEC 103:1997 and IEC TS 60870-5-7:2025 against
  their texts (and IEC 103 against six independent implementations),
  capture corpora for all four standards, and the IEC 103 interop suite
  against iec60870-5-interop v0.2.1 (vitusdong, ARIEC103, Open103,
  go-iecp5; pinned by digest, in CI).
- Next: IEC 102 interop (a gisce/iec870ree master adapter), IEC 101
  digitalpetri and non-default field sizes, IEC 103 generic services and
  disturbance data in the harness contract, the remaining open deviations
  (LIMITATIONS.md).

## 2026-11-11 (later)

- Repositories public on GitHub (otfabric/go-iec60870-5,
  otfabric/iec60870-5-interop). iec60870-5-interop v0.1.0 released (7
  multi-arch images, digests in its manifest.json); IEC 101 interop pinned
  to them and in the Interop workflow.
- Standard texts obtained: IEC 60870-5-101:2003 (GOST R IDT) and IEC TS
  60870-5-7:2025. Read-only audits against both: 101 fixes applied (delay
  acquisition, counter freeze), remaining 101 deviations and the TS-7
  gaps are listed in LIMITATIONS.md and SECURITY.md. The same `sec` gaps
  exist in go-iec104 (not modified here).
- Beckhoff TwinCAT TS650x manual (IEC 60870-5-101/-102/-103/-104, 511 pages)
  obtained: vendor documentation of a commercial implementation, the main
  available source for IEC 102 types until the standard is held.
- Public captures for 101/102/103/104 surveyed (sources noted for the
  capture hardening).

Next: v0.2.0 = capture (pcap/pcapng) hardening for 101/103/104, the TS-7
gaps (statistics reporting, RBAC file delete, MONITOR DATA, key-usage
defaults, MAC defaults), the 101 link-state deviations; then IEC 102.

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
