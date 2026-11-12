# Contributing to go-iec60870-5

Thank you for your interest in contributing to `go-iec60870-5`! This document covers the guidelines for contributing.

## Getting Started

1. Fork the repository on GitHub
2. Clone your fork locally:
   ```sh
   git clone git@github.com:<your-username>/go-iec60870-5.git
   cd go-iec60870-5
   ```
3. Create a feature branch:
   ```sh
   git checkout -b feature/my-change
   ```

## Development Setup

### Prerequisites

- Go 1.26+ (see [go.mod](go.mod)); the code must build and pass on Go 1.26, so do not use newer language or standard-library features
- [staticcheck](https://staticcheck.dev/), [golangci-lint](https://golangci-lint.run/) and [govulncheck](https://pkg.go.dev/golang.org/x/vuln/cmd/govulncheck) for the checks
- No hardware and no external simulator for `make check`: tests run client and server against each other and against scripted peers, over loopback TCP (IEC 104) or in-memory lines (IEC 101, 102, 103). The interop suites need Docker

### Build & Test

```sh
# Run all checks (format, lint, vet, vulnerability scan, tests, coverage, examples, docs)
make check

# Run the library tests with the race detector
make test

# Run every fuzz target (FUZZTIME=30s by default)
make fuzz

# Build the examples into ./bin
make build

# IEC 104 against lib60870-C, OpenMUC j60870, wendy512/iec104, cB-Sigma and part5 (needs Docker)
make interop

# The serial standards against the references of iec60870-5-interop (needs Docker)
make interop-iec101 interop-iec103 interop-iec102

# Any standard against an own-stack image of iec60870-5-interop (the manual
# Self-interop workflow does this with :latest; not independent evidence)
IEC104_INTEROP_ADAPTERS=otfabric IEC104_INTEROP_OTFABRIC_IMAGE=ghcr.io/otfabric/iec60870-5-interop-iec104-otfabric:latest make interop
IEC101_INTEROP_ADAPTERS=otfabric IEC101_INTEROP_OTFABRIC_IMAGE=ghcr.io/otfabric/iec60870-5-interop-iec101-otfabric:latest make interop-iec101
```

Run `make` (or `make help`) for the full list of targets.

## Making Changes

### Code Style

- Follow standard Go conventions (`gofmt`, `go vet`)
- Start every Go file with `// SPDX-License-Identifier: MIT`
- No dependencies outside the standard library
- Type identifications keep the mnemonics of the standard (`M_SP_NA_1`); everything else follows Go naming
- Respect the package boundaries and import rules in [ARCHITECTURE.md](ARCHITECTURE.md): `iec104/apci`, `asdu`, `ft12`, `cp`, `iec102` and `iec103` are pure codecs, protocol state lives in `link` (serial) and `iec104/internal/link`, and no `client` package imports a `server` package or the other way round

### Formatting

`make fmt` formats with the `gofmt` of the Go version in `go.mod`, which is the version CI uses, not necessarily the one you have installed. `make fmt-check` (part of `make check`) fails if either that `gofmt` or your local one would change a file. If they disagree, restructure the code (for example, move trailing comments onto their own lines) rather than picking a side.

### Tests

- Every change to the wire format needs a byte-level vector in the codec package (`iec104/apci`, `asdu`, `ft12`, `iec102`, `iec103`), taken from the standard, vendor documentation or a capture, not from the encoder's own output
- A new `asdu` type identification must appear in the round-trip test (`TestRoundTripAllTypes` fails for a registered type without coverage) and in the INTEROPERABILITY document of its standard
- Protocol machine behaviour is tested in `link` and `iec104/internal/link` with scripted peers, where the exact frames are visible
- Tests must pass with `-race` and must not depend on timing beyond generous upper bounds
- A change to a protocol machine or to a type a reference fixture covers must keep the interop suite of its standard green (the Interop workflow runs them on every push except Markdown-only changes). The `interop` packages hold this repository's scenarios and assertions; the reference stacks belong to [otfabric/iec60870-5-interop](https://github.com/otfabric/iec60870-5-interop) and are consumed as images only

### Documentation

Update the document that owns the topic: [API.md](API.md) for signatures, [ERRORS.md](ERRORS.md) for error behaviour, [OBSERVABILITY.md](OBSERVABILITY.md) for logs and metrics, the INTEROPERABILITY document of the standard ([IEC 101](INTEROPERABILITY.md), [IEC 102](docs/iec102/INTEROPERABILITY.md), [IEC 103](docs/iec103/INTEROPERABILITY.md), [IEC 104](docs/iec104/INTEROPERABILITY.md)) and [docs/FEATURE-STATUS.md](docs/FEATURE-STATUS.md) for protocol coverage, [LIMITATIONS.md](LIMITATIONS.md) for what is missing, and add an entry to [RELEASE.md](RELEASE.md).

## Submitting

1. Run `make check`
2. Commit with a message that says what changed and why
3. Open a pull request against `main` describing the change and how it was tested

## License

By contributing you agree that your contributions are licensed under the [MIT License](LICENSE).
