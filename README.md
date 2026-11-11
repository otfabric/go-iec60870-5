# go-iec60870-5 - IEC 60870-5 telecontrol protocols for Go

[![Go Version](https://img.shields.io/badge/Go-1.26%2B-00ADD8?style=flat&logo=go)](https://go.dev/)
[![Go Reference](https://pkg.go.dev/badge/github.com/otfabric/go-iec60870-5.svg)](https://pkg.go.dev/github.com/otfabric/go-iec60870-5)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![CI Status](https://github.com/otfabric/go-iec60870-5/actions/workflows/ci.yml/badge.svg)](https://github.com/otfabric/go-iec60870-5/actions/workflows/ci.yml)
[![Interop](https://github.com/otfabric/go-iec60870-5/actions/workflows/interop.yml/badge.svg)](https://github.com/otfabric/go-iec60870-5/actions/workflows/interop.yml)
[![Code Coverage](https://codecov.io/gh/otfabric/go-iec60870-5/graph/badge.svg)](https://codecov.io/gh/otfabric/go-iec60870-5)
[![Latest Release](https://img.shields.io/github/v/release/otfabric/go-iec60870-5?label=release)](https://github.com/otfabric/go-iec60870-5/releases)

A pure Go implementation of the IEC 60870-5 protocol family: no cgo, no
dependencies outside the standard library.

| Companion standard | Package | Status |
|---|---|---|
| IEC 60870-5-104 (TCP/IP) | [`iec104`](iec104), `iec104/client`, `iec104/server` | Production code carried over from go-iec104 v0.3.0, with its tests, captures and interop suite. See [docs/IEC104_PARITY.md](docs/IEC104_PARITY.md). |
| IEC 60870-5-101 (serial) | [`iec101`](iec101), `iec101/client`, `iec101/server` | New. Controlling and controlled station, unbalanced and balanced. Client and server pass independent interop runs against lib60870-C and cB-Sigma in both modes, default field sizes (`make interop-iec101`, local; [INTEROPERABILITY.md](INTEROPERABILITY.md)). |
| IEC 60870-5-103 (protection equipment) | [`iec103`](iec103), `iec103/client`, `iec103/server` | New. Own ASDU codec for every compatible type (cl. 7.3), controlling system and protection equipment: initialization, time sync, general interrogation, general commands, generic services, disturbance data transfer. Unit, annex B and e2e evidence; **no independent interoperability evidence yet**. See [docs/iec103](docs/iec103/README.md). |
| IEC 60870-5-102 (integrated totals) | - | Not implemented yet. See [docs/ROADMAP.md](docs/ROADMAP.md). |

What is implemented, clause by clause, and what evidence backs it:
[docs/FEATURE-STATUS.md](docs/FEATURE-STATUS.md). Nothing here is
certified; interoperability tests are not conformance tests.

## Packages

The packages follow the layers of IEC 60870-5, sharing what the companion
standards share and nothing more ([ARCHITECTURE.md](ARCHITECTURE.md)):

| Package | Standard | What it is |
|---|---|---|
| `cp` | IEC 60870-5-4 | time tags CP16Time2a, CP24Time2a, CP32Time2a, CP56Time2a |
| `ft12` | IEC 60870-5-1 | FT 1.2 frames and a resynchronizing stream decoder (101, 102, 103) |
| `link` | IEC 60870-5-2 | link procedures: unbalanced primary and secondary, balanced station, FCB/FCV, retries |
| `asdu` | IEC 60870-5-101/-104 | the telecontrol ASDU model and codec, configurable field sizes |
| `telecontrol` | IEC 60870-5-5 as used by 101/104 | matching answers to requests: confirmations, interrogation, read, commands |
| `iec101`, `iec101/client`, `iec101/server` | IEC 60870-5-101 | serial telecontrol stations |
| `iec103`, `iec103/client`, `iec103/server` | IEC 60870-5-103 | protection equipment: own ASDU (FUN/INF), stations on `link` |
| `iec104`, `iec104/apci`, `iec104/client`, `iec104/server` | IEC 60870-5-104 | TCP/IP telecontrol stations |
| `sec` | IEC 62351-5 / IEC TS 60870-5-7 | application-layer security (used by `iec104`) |
| root `iec60870` | - | the `Logger` interfaces |

API reference generated from the source: [docs/api](docs/api/README.md);
guide to the API: [API.md](API.md).

## Install

```sh
go get github.com/otfabric/go-iec60870-5
```

Requires Go 1.26 or later.

## IEC 60870-5-101 quickstart

The transport is any `io.ReadWriter`: a serial port (for example
[go-serial](https://github.com/otfabric/go-serial)), a TCP connection to a
terminal server that passes the line through unchanged, or a pipe in tests.
IEC 101 over such a TCP stream is still IEC 101, not IEC 104.

```go
p := iec101.DefaultParams() // unbalanced, 1-octet link address, COT 1, CA 1, IOA 2
c, err := client.New(port, client.Config{
	Params:   p,
	LinkAddr: 3, // link address of the controlled station
	Handler:  func(a *asdu.ASDU) { fmt.Println(a) }, // spontaneous and polled data
})
if err != nil {
	return err
}
defer c.Close()
if err := c.WaitUp(ctx); err != nil { // link initialized
	return err
}
data, err := c.Interrogate(ctx, 1, asdu.QOIStation)
```

A runnable version with both stations is
[iec101/examples/quickstart](iec101/examples/quickstart/main.go).

## IEC 60870-5-104

The IEC 104 client and server are documented in
[docs/iec104/README.md](docs/iec104/README.md), carried over from go-iec104
with their examples in [iec104/examples](iec104/examples).

## Development

```sh
make check    # format, lint, vet, govulncheck, tests with -race, coverage floors, examples, docs
make fuzz     # fuzz targets
make interop  # IEC 104 against the pinned reference images (Docker)
```

[CONTRIBUTING.md](CONTRIBUTING.md), [TESTING.md](TESTING.md),
[ERRORS.md](ERRORS.md), [OBSERVABILITY.md](OBSERVABILITY.md),
[SECURITY.md](SECURITY.md), [RELEASE.md](RELEASE.md).

Independent reference implementations live in
[otfabric/iec60870-5-interop](https://github.com/otfabric/iec60870-5-interop)
(IEC 101/102/103/104) and, for IEC 104 today,
[otfabric/iec104-interop](https://github.com/otfabric/iec104-interop).

## Relation to go-iec104

[go-iec104](https://github.com/otfabric/go-iec104) stays maintained and
unchanged; this module does not import it. The `iec104` packages here began
as a copy of go-iec104 v0.3.0 so that IEC 101 could share its ASDU model.
How the two relate, and what a migration would involve, is in
[docs/MIGRATION_AUDIT.md](docs/MIGRATION_AUDIT.md).

## License

MIT, see [LICENSE](LICENSE).
