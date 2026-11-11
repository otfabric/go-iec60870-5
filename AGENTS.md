# AGENTS.md — go-iec60870-5

## What this is

Pure Go implementation of the IEC 60870-5 telecontrol family with no dependencies outside the standard library. IEC 60870-5-104 (carried over from go-iec104 v0.3.0: APCI, ASDU codec, file transfer, redundancy, TLS, IEC 62351-5) and IEC 60870-5-101 (FT 1.2, IEC 60870-5-2 link procedures, unbalanced and balanced stations). IEC 60870-5-103 (own ASDU codec, controlling system and protection equipment, docs/iec103). IEC 60870-5-102 (own ASDU codec, controlling and integrated total station, opt-in Spanish REE profile, docs/iec102). Profile: **protocol-library**. Nothing is certified; see LIMITATIONS.md.

## Organisation standards

This repository follows **otfabric/engineering policy 1.4**, profile **protocol-library** (`.otfabric/policy.yaml`, mode `enforce`). Organisation skills: `otfabric` Claude Code plugin. Where this file and the organisation standards disagree, this file wins here — say so in your report.

## Where the truth lives

| Question | Source |
|---|---|
| Status per companion standard and evidence | docs/FEATURE-STATUS.md |
| Standards, editions, which texts we hold | docs/STANDARDS.md |
| Requirements and the tests that trace them | docs/REQUIREMENTS.md |
| Packages, layering, import rules | ARCHITECTURE.md |
| IEC 104 clause by clause, verification | docs/iec104/INTEROPERABILITY.md |
| IEC 101 clause by clause | INTEROPERABILITY.md |
| What came from go-iec104, parity | docs/MIGRATION_AUDIT.md, docs/IEC104_PARITY.md |
| Next phases | docs/ROADMAP.md, PROGRESS.md |
| Errors | ERRORS.md |
| Release history | RELEASE.md (`## vX.Y.Z` … `---`) |

## Hard rules

1. Never lower the go-ci `coverage-min` (90) or the per-package floor, add lint suppressions, or skip/delete tests to make CI pass.
2. Never invent protocol semantics; cite the standard and clause. Where we do not hold the text (docs/STANDARDS.md), say so and mark the behaviour as an interpretation.
3. Interop claims only from passing runs against pinned independent images. The library is never its own oracle.
4. The module stays free of dependencies outside the standard library; `iec104/interop/cbsigma` is a module of its own.
5. Never import github.com/otfabric/go-iec104 and never modify the go-iec104, iec104-interop or iec104ctl repositories from work in this one.
6. IEC 102 and IEC 103 keep their own ASDU models; share only `cp`, `ft12` and `link`.
7. The code must build and pass on the Go version in go.mod.

## Commands

| When you change… | Run |
|---|---|
| anything | `make check` |
| a codec | `make fuzz` |
| IEC 104 interop-relevant behaviour | `make interop` (Docker) |
| application-layer security | `make test-cbsigma` (Go 1.26) |

## Definition of Done

`make check` green; tests for new behaviour including error paths; fuzz target for any new decoder; FEATURE-STATUS, INTEROPERABILITY and API docs updated in the same change (`docsync api` regenerates docs/api); RELEASE.md section for user-visible changes.

## Git and releases

Branches and PRs. Do not push to `main`, tag or run the Release workflow unless the maintainer asks in the session.
