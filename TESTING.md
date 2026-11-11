# Testing

How to run each test tier of go-iec60870-5. What is implemented and at which evidence level: [docs/FEATURE-STATUS.md](docs/FEATURE-STATUS.md). Interop results: [INTEROPERABILITY.md](docs/iec104/INTEROPERABILITY.md#verification-status).

## Tiers

| Tier | Command | Prerequisites | Runs in CI |
|---|---|---|---|
| Unit and package tests, race detector | `make test` | none | CI workflow, Go 1.26 and 1.27 |
| End-to-end, on loopback TCP | `make e2e` (also part of `make test`) | none | CI workflow |
| IEC 101 end-to-end: client against server over in-memory and lossy lines, unbalanced and balanced (`iec101/e2e`) | `make test` | none | CI workflow |
| IEC 103 end-to-end: controlling system against protection equipment, lossy and muted lines (`iec103/e2e`); annex B examples (`iec103`) | `make test` | none | CI workflow |
| IEC 102 end-to-end: controlling station against integrated total station (`iec102/e2e`); recorded meter traffic and gisce/iec870ree vectors (`iec102`, testdata/innowatt) | `make test` | none | CI workflow |
| Coverage gate | `make coverage-check` | none | CI workflow |
| Per-package coverage floor | `make coverage-pkg-check` | none | CI workflow, `Per-package coverage` |
| Fuzz | `make fuzz` | none | CI workflow, 10s per discovered target; `Extended` workflow, 5 minutes per target |
| Long chaos | `make chaos` | none | `Extended` workflow |
| Benchmarks | `make bench` | none | no |
| Examples | `make examples` | none | CI workflow, job Examples |
| Cross-compilation | `make cross` | none | no |
| Recorded traffic, committed captures | `make test` (the capture suites below) | none | CI workflow |
| Recorded traffic, files unchanged | `make captures-verify` (the SHA256SUMS of `iec104`, `iec101`, `iec103` and `iec102/testdata/captures`) | `sha256sum` or `shasum` | CI workflow, job Captures |
| Recorded traffic, the full recordings | `make captures-extended` | network (GitHub), 251 MB | no |
| Recorded traffic, regenerate the tshark oracle, the manifest and the fuzz seeds | `make captures-oracle`, `make captures-manifest` | `tshark` for the oracle | no |
| Interop with reference images | `make interop` | Docker | Interop workflow, every push and pull request |
| IEC 62351-5 against cB-Sigma/iec60870, in process | `make test-cbsigma` | Go 1.26 (`interop/cbsigma` is a module of its own) | Interop workflow, job cB-Sigma |

`make check` is the local gate: format, lint, vet, vulnerability scan, tests, coverage and examples. It needs `staticcheck`, `golangci-lint` and `govulncheck` on the `PATH`. It fails on the per-package floor; `make coverage-check` fails on the floor of the total.

The `Extended` workflow runs nightly: every fuzz target for 5 minutes and the chaos test for 20 seeds of 30 seconds. A fuzz failure uploads its inputs as the artifact `fuzz-failures`. It can be started by hand with other durations.

## What each suite does

- **Package tests**: `apci`, `asdu`, `sec` and `internal/link` test the codecs and the protocol machine; `internal/link` also runs against scripted peers, for what a correct peer never sends.
- **`e2e`**: go-iec60870-5 client against go-iec60870-5 server.
  - `e2e_test.go` runs the station scenarios on the baseline fixture (`internal/station/baseline.json`) over plain TCP, TLS, `k = w = 1`, IEC 62351-5 with HMAC, and IEC 62351-5 with AES-256-GCM over TLS.
  - `matrix_test.go` sends every type identification through both codecs and both protocol machines, repeats this with other field sizes, and wraps the sequence numbers.
  - `features_test.go` covers states, logs, metrics, retries, admission, the event queue and failing requests.
  - `statemachine_test.go` covers start, stop, close and switchover races.
  - `chaos_test.go` runs seeded random operations while connections are cut.
  - `hostile_test.go` sends broken framing, control fields, sequence numbers, idle and flooding peers, malformed ASDUs and out-of-turn file transfer to the server, and plays a hostile station against the client; each case checks the time bound, that a legitimate client is then served, and goroutines. `FuzzServerStream` (in `hostile_fuzz_test.go`) feeds arbitrary octets to a running server over an in-memory connection.
- **Recorded traffic, shared readers**: `internal/pcapio` reads pcap and pcapng (gzip or not; Ethernet, loopback, Linux cooked, raw IPv4 and SEL RTAC serial-line frames) and reassembles TCP per direction; `internal/serialcap` splits FT 1.2 frames out of RTAC lines and TCP streams of serial port servers and checks the link procedures (FCV per function, FCB toggled and kept for repetitions, FCB 1 after a reset, every request answered by a matching response); `internal/testutil/corpus` holds the manifest, SHA256SUMS and tshark-oracle helpers of the serial corpora.
- **Recorded IEC 101** ([iec101/testdata/captures](iec101/testdata/captures/README.md)): `iec101/internal/capture` decodes every frame and ASDU with the manifest's field sizes, encodes both again, checks the link analysis and compares with Wireshark (`TestCorpus`, `TestOracleTshark`, `TestManifest`); `iec101/client/capture_test.go` replays the recorded controlled stations against the client (the client makes the recorded requests through `Interrogate`, `Command` and `Send`; every request must equal the recording and every station ASDU must reach the handler), `iec101/server/capture_test.go` sends the recorded requests to the server and compares the confirmations and terminations it builds.
- **Recorded IEC 103** ([iec103/testdata/captures](iec103/testdata/captures/README.md)): `iec103/internal/capture` does the same for the SEL RTAC excerpts and the ARIEC103 vectors, with the frames the codec refuses or does not re-encode exactly listed with their reason; no replay.
- **IEC 102 frames** ([iec102/testdata/captures](iec102/testdata/captures/README.md)): `iec102/internal/capture` checks FT 1.2 framing and checksum of every frame of a meter log and of the gisce tests.
- **Recorded IEC 104** ([iec104/testdata/captures](iec104/testdata/captures/README.md)): `iec104/internal/capture` reads the captures (pcap, pcapng, gzip), reassembles TCP and checks every conversation's sequence numbers, acknowledgements and procedures; `TestCorpusReencode` decodes and encodes every APDU; `TestOracleTshark` and `TestOracleZeek` compare the decoded fields with Wireshark's (the `*.tshark.tsv.gz` files next to the captures) and with CERT.LV's Zeek logs; `TestManifest` checks testdata/captures/manifest.json. `client/capture_test.go` plays every recorded controlled station against the client (through `Send`, and through the request and file methods), `server/capture_test.go` every recorded controlling station against the server. The comparison rules: sequence numbers are rebuilt from the live link, ASDU octets are compared as recorded, and every difference is classified in the test that finds it. `IEC104_CAPTURE_EXTENDED=1` adds the files of `make captures-fetch`.
- **`interop`** (build tag `interop`): go-iec60870-5 as client and as server against the images of [otfabric/iec104-interop](https://github.com/otfabric/iec104-interop). The images are pinned by digest in `interop/harness.go`, the Makefile and `.github/workflows/interop.yml`; keep the three in step.
- **`interop/cbsigma`**: `sec` against cB-Sigma/iec60870 in memory.

## Options

| Variable or flag | Effect |
|---|---|
| `E2E_CHAOS_SECONDS` | Duration of each chaos seed, default 2 |
| `E2E_CHAOS_SEEDS` | Number of chaos seeds, default 3 |
| `-short` | Reduces chaos to one seed of one second, skips the 40,000-frame sequence wrap, and reduces the rounds of the crosstalk test in `client` |
| `FUZZTIME` | Duration of each fuzz target in `make fuzz`, default 30s |
| `CHAOS_SECONDS`, `CHAOS_SEEDS` | Duration and number of seeds of `make chaos`, default 30 and 20 |
| `IEC104_REQUIRE_INTEROP=1` | An interop test that no selected reference can run fails instead of skipping. The Interop workflow sets it |
| `IEC104_INTEROP_ADAPTERS` | Comma-separated subset of `lib60870`, `openmuc`, `wendy512`, `cbsigma` |
| `IEC104_INTEROP_<ADAPTER>_IMAGE` | Image of that reference, to test another build |

## Skips

The tests that can skip are:

- Interop tests skip what a reference image does not declare in `print-capabilities`, and the read cases of j60870 listed as a known bug. Those cases fail when the bug no longer reproduces ([INTEROPERABILITY.md](docs/iec104/INTEROPERABILITY.md#verification-status)). A test that no reference at all can run skips only without `IEC104_REQUIRE_INTEROP=1`.
- Three tests in `client` skip when their port cannot be bound again.
- `make interop` without Docker fails; it does not skip.

## Coverage policy

The gate is 90% of statements in total, the same value as `coverage-min` in `.github/workflows/ci.yml` and `COVER_MIN` in the Makefile. Each package has a floor of its own, `COVER_PKG_MIN` (90%), checked by `make coverage-pkg-check`. Coverage is measured over `COVER_PKGS` at once, so the e2e suite counts for the packages it exercises; `make coverpkg-check` keeps `coverpkg` in the workflow equal to it. `TEST_PKGS` in the Makefile and `test-exclude-regex` in the workflow define the same packages. Excluded:

| Package | Why |
|---|---|
| `examples/...` | Programs, run by `make examples` |
| `internal/testutil` | Test helpers, no production code |
| `internal/station` | Test station for the suites, no production code |
| `iec104/internal/capture`, `iec101/internal/capture`, `iec103/internal/capture`, `iec102/internal/capture` | Readers and replay of the recorded captures, test infrastructure; their own tests run |
| `internal/testutil/corpus` | Manifest and oracle helpers of the capture suites, no production code |

`interop` is outside the default build by its build tag, and `interop/cbsigma` is another module.

## Fuzz regressions

The targets include `FuzzParse` in `apci`, `FuzzDecode` in `asdu`, `ft12`, `iec103` and `sec`. The seeds `testdata/fuzz/<Target>/capture-*` of `apci`, `asdu`, `ft12` and `iec103` are one APDU, frame or ASDU of every shape in the recorded captures of levels A to D (`make captures-manifest` writes them). A new decoder gets a target; `make fuzz` discovers it.

When a target fails, `go test` writes the input to `testdata/fuzz/<Target>/` in the package; CI uploads the same directory as the artifact `fuzz-failures`. To keep it as a regression test:

1. Copy the file into `testdata/fuzz/<Target>/` if it came from CI.
2. Reproduce it: `go test -run '<Target>/<file>' ./<package>`.
3. Fix the decoder and commit the file with the fix. Plain `go test` then runs it on every push.
