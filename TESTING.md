# Testing

How to run each test tier of go-iec60870-5. What is implemented and at which evidence level: [docs/FEATURE-STATUS.md](docs/FEATURE-STATUS.md). Interop results: [INTEROPERABILITY.md](docs/iec104/INTEROPERABILITY.md#verification-status).

## Tiers

| Tier | Command | Prerequisites | Runs in CI |
|---|---|---|---|
| Unit and package tests, race detector | `make test` | none | CI workflow, Go 1.26 and 1.27 |
| IEC 104 end-to-end, on loopback TCP (`iec104/e2e`) | `make e2e` (also part of `make test`) | none | CI workflow |
| IEC 101 end-to-end: client against server over in-memory and lossy lines, unbalanced and balanced, hostile peers, short chaos (`iec101/e2e`) | `make test` | none | CI workflow |
| IEC 103 end-to-end: controlling system against protection equipment, lossy and muted lines, hostile peers, short chaos (`iec103/e2e`); annex B examples (`iec103`) | `make test` | none | CI workflow |
| IEC 102 end-to-end: controlling station against integrated total station, hostile peers, short chaos (`iec102/e2e`); recorded meter traffic and gisce/iec870ree vectors (`iec102`, testdata/innowatt) | `make test` | none | CI workflow |
| Coverage gate | `make coverage-check` | none | CI workflow |
| Per-package coverage floor | `make coverage-pkg-check` | none | CI workflow, `Per-package coverage` |
| Fuzz | `make fuzz` | none | CI workflow, 10s per discovered target; `Extended` workflow, 5 minutes per target |
| Long chaos: IEC 104 over TCP, IEC 101 (unbalanced and balanced), 102 and 103 over a faulty in-memory line | `make chaos` (`CHAOS_PKGS` narrows it) | none | `Extended` workflow, one job per standard; `make test` runs the short default |
| Benchmarks | `make bench` | none | no |
| Examples | `make examples` | none | CI workflow, job Examples |
| Cross-compilation | `make cross` | none | no |
| Recorded traffic, committed captures | `make test` (the capture suites below) | none | CI workflow |
| Recorded traffic, files unchanged | `make captures-verify` (the SHA256SUMS of `iec104`, `iec101`, `iec103` and `iec102/testdata/captures`) | `sha256sum` or `shasum` | CI workflow, job Captures |
| Recorded traffic, the full recordings | `make captures-extended` | network (GitHub), 251 MB | no |
| Recorded traffic, regenerate the tshark oracle, the manifest and the fuzz seeds | `make captures-oracle`, `make captures-manifest` | `tshark` for the oracle | no |
| IEC 104 interop with reference images | `make interop` | Docker | Interop workflow, every push and pull request except Markdown-only changes |
| IEC 101 interop (lib60870-C, cB-Sigma) | `make interop-iec101` | Docker | Interop workflow, job `iec101` |
| IEC 103 interop (vitusdong, ARIEC103, Open103, go-iecp5) | `make interop-iec103` | Docker | Interop workflow, job `iec103` |
| IEC 102 interop (gisce/iec870ree against the server) | `make interop-iec102` | Docker; the image is a local `:dev` build until iec60870-5-interop v0.3.0 publishes it | Interop workflow, job `iec102` (pulls the v0.3.0 tag, so it fails until that release) |
| IEC 62351-5 against cB-Sigma/iec60870, in process | `make test-cbsigma` | Go 1.26 (`iec104/interop/cbsigma` is a module of its own) | Interop workflow, job cB-Sigma |

`make check` is the local gate: format, lint, vet, vulnerability scan, capture checksums, tests, per-package coverage, examples and `docs-check` (docsync, as the Docs workflow runs it). It needs `staticcheck`, `golangci-lint` and `govulncheck` on the `PATH`. It fails on the per-package floor; `make coverage-check` fails on the floor of the total.

The `Extended` workflow runs nightly: every fuzz target for 5 minutes and each chaos suite for 20 seeds of 30 seconds. A fuzz failure uploads its inputs as the artifact `fuzz-failures`. It can be started by hand with other durations.

## What each suite does

- **IEC 104 package tests**: `iec104/apci`, `asdu`, `sec` and `iec104/internal/link` test the codecs and the protocol machine; `internal/link` also runs against scripted peers, for what a correct peer never sends.
- **`iec104/e2e`**: the IEC 104 client against the IEC 104 server of this module.
  - `e2e_test.go` runs the station scenarios on the baseline fixture (`internal/station/baseline.json`) over plain TCP, TLS, `k = w = 1`, IEC 62351-5 with HMAC, and IEC 62351-5 with AES-256-GCM over TLS.
  - `matrix_test.go` sends every type identification through both codecs and both protocol machines, repeats this with other field sizes, and wraps the sequence numbers.
  - `features_test.go` covers states, logs, metrics, retries, admission, the event queue and failing requests.
  - `statemachine_test.go` covers start, stop, close and switchover races.
  - `chaos_test.go` runs seeded random operations while connections are cut.
  - `hostile_test.go` sends broken framing, control fields, sequence numbers, idle and flooding peers, malformed ASDUs and out-of-turn file transfer to the server, and plays a hostile station against the client; each case checks the time bound, that a legitimate client is then served, and goroutines. `FuzzServerStream` (in `hostile_fuzz_test.go`) feeds arbitrary octets to a running server over an in-memory connection.
- **Serial standards against hostile peers and faulty lines** (`iec101/e2e`, `iec102/e2e`, `iec103/e2e`), on the in-memory line of `internal/testutil/serialline`, whose directions drop, duplicate, corrupt, delay, split, truncate and reorder writes and add noise, and whose raw peer writes FT 1.2 octets straight onto it:
  - `hostile_test.go` plays a raw primary against each server (line noise, truncated, oversized and corrupted frames, other and broadcast addresses, secondary frames, function codes the station does not serve, stuck FCB and repetitions with other content, ASDUs of the other standards, malformed ASDUs of every length, request floods, a half-open line with the longest length, slow drip, a reset in the middle of an exchange, a peer that vanishes) and a raw station against each client (silence, noise, wrong functions and addresses, truncated and dripped answers, DFC and ACD floods, class 1 floods, answers that never end, stray answers, undecodable data, NACK and function 14 forever). Each case checks the station's answer or silence, bounded queues, deadlines, that a peer that behaves is served afterwards, that Close returns, and goroutines.
  - `chaos_test.go` (`TestChaos`) runs client and server over the faulty line with concurrent requests and spontaneous data and checks that no command is executed twice, that every request ends, and that the link comes up again when the faults stop. `E2E_CHAOS_SECONDS` and `E2E_CHAOS_SEEDS` apply as for IEC 104.
  - The fuzz targets of the link procedures (`link`: `FuzzSecondary`, `FuzzBalanced` with arbitrary frame sequences, `FuzzPrimary` with arbitrary answers), of `telecontrol.Requester.Dispatch` (`FuzzDispatch`) and of the station receive paths (`iec101/client`, `iec101/server`: `FuzzReceive`; `iec102/server`: `FuzzServe`; `iec102/e2e`: `FuzzClientAnswers`; `iec103/server`: `FuzzDispatch`; `iec103/client`: `FuzzReceive`).
- **Recorded traffic, shared readers**: `internal/pcapio` reads pcap and pcapng (gzip or not; Ethernet, loopback, Linux cooked, raw IPv4 and SEL RTAC serial-line frames) and reassembles TCP per direction; `internal/serialcap` splits FT 1.2 frames out of RTAC lines and TCP streams of serial port servers and checks the link procedures (FCV per function, FCB toggled and kept for repetitions, FCB 1 after a reset, every request answered by a matching response); `internal/testutil/corpus` holds the manifest, SHA256SUMS and tshark-oracle helpers of the serial corpora.
- **Recorded IEC 101** ([iec101/testdata/captures](iec101/testdata/captures/README.md)): `iec101/internal/capture` decodes every frame and ASDU with the manifest's field sizes, encodes both again, checks the link analysis and compares with Wireshark (`TestCorpus`, `TestOracleTshark`, `TestManifest`); `iec101/client/capture_test.go` replays the recorded controlled stations against the client (the client makes the recorded requests through `Interrogate`, `Command` and `Send`; every request must equal the recording and every station ASDU must reach the handler), `iec101/server/capture_test.go` sends the recorded requests to the server and compares the confirmations and terminations it builds.
- **Recorded IEC 103** ([iec103/testdata/captures](iec103/testdata/captures/README.md)): `iec103/internal/capture` does the same for the SEL RTAC excerpts and the ARIEC103 vectors, with the frames the codec refuses or does not re-encode exactly listed with their reason; no replay.
- **IEC 102 frames** ([iec102/testdata/captures](iec102/testdata/captures/README.md)): `iec102/internal/capture` checks FT 1.2 framing and checksum of every frame of a meter log and of the gisce tests.
- **Recorded IEC 104** ([iec104/testdata/captures](iec104/testdata/captures/README.md)): `iec104/internal/capture` reads the captures (pcap, pcapng, gzip), reassembles TCP and checks every conversation's sequence numbers, acknowledgements and procedures; `TestCorpusReencode` decodes and encodes every APDU; `TestOracleTshark` and `TestOracleZeek` compare the decoded fields with Wireshark's (the `*.tshark.tsv.gz` files next to the captures) and with CERT.LV's Zeek logs; `TestManifest` checks testdata/captures/manifest.json. `client/capture_test.go` plays every recorded controlled station against the client (through `Send`, and through the request and file methods), `server/capture_test.go` every recorded controlling station against the server. The comparison rules: sequence numbers are rebuilt from the live link, ASDU octets are compared as recorded, and every difference is classified in the test that finds it. `IEC104_CAPTURE_EXTENDED=1` adds the files of `make captures-fetch`.
- **`iec104/interop`** (build tag `interop`): this module as client and as server against the IEC 104 images of [otfabric/iec60870-5-interop](https://github.com/otfabric/iec60870-5-interop) v0.2.1 (lib60870-C, OpenMUC j60870, wendy512/iec104, cB-Sigma/iec60870, pascaldekloe/part5), pinned by digest in `iec104/interop/harness.go`, the Makefile and `.github/workflows/interop.yml`; keep the three in step.
- **`iec101/interop`, `iec103/interop`, `iec102/interop`** (build tag `interop`): the serial standards against the images of the same harness, pinned in each package's `harness.go`; results in [INTEROPERABILITY.md](INTEROPERABILITY.md), [docs/iec103/INTEROPERABILITY.md](docs/iec103/INTEROPERABILITY.md) and [docs/iec102/INTEROPERABILITY.md](docs/iec102/INTEROPERABILITY.md).
- **`iec104/interop/cbsigma`**: `sec` against cB-Sigma/iec60870 in memory.

## Options

| Variable or flag | Effect |
|---|---|
| `E2E_CHAOS_SECONDS` | Duration of each chaos seed, default 2 |
| `E2E_CHAOS_SEEDS` | Number of chaos seeds, default 3 |
| `-short` | Reduces chaos (all four suites) to one seed of one second, skips the 40,000-frame sequence wrap, and reduces the rounds of the crosstalk test in `iec104/client` |
| `FUZZTIME` | Duration of each fuzz target in `make fuzz`, default 30s |
| `CHAOS_SECONDS`, `CHAOS_SEEDS` | Duration and number of seeds of `make chaos`, default 30 and 20 |
| `CHAOS_PKGS` | Chaos suites of `make chaos`, default `./iec104/e2e/ ./iec101/e2e/ ./iec102/e2e/ ./iec103/e2e/` |
| `IEC104_REQUIRE_INTEROP=1` | An interop test that no selected reference can run fails instead of skipping. The Interop workflow sets it |
| `IEC104_INTEROP_ADAPTERS` | Comma-separated subset of `lib60870`, `openmuc`, `wendy512`, `cbsigma`, `part5`, `otfabric` |
| `IEC104_INTEROP_<ADAPTER>_IMAGE` | Image of that reference, to test another build; `IEC104_INTEROP_OTFABRIC_IMAGE` adds an image of this module's own stack (unset by default) |
| `IEC101_REQUIRE_INTEROP`, `IEC102_REQUIRE_INTEROP`, `IEC103_REQUIRE_INTEROP` | As `IEC104_REQUIRE_INTEROP` for the serial suites; the `make interop-iec10x` targets set them to 1 |
| `IEC101_INTEROP_ADAPTERS`, `IEC103_INTEROP_ADAPTERS`, `IEC10x_INTEROP_<STACK>_IMAGE`, `IEC102_INTEROP_GISCE_IMAGE` | Subset and images of the serial suites (see each `harness.go`) |

## Skips

The tests that can skip are:

- Interop tests skip what a reference image does not declare in `print-capabilities`, and the read cases of j60870 listed as a known bug. Those cases fail when the bug no longer reproduces ([INTEROPERABILITY.md](docs/iec104/INTEROPERABILITY.md#verification-status)). A test that no reference at all can run skips only without `IEC104_REQUIRE_INTEROP=1`.
- Three tests in `iec104/client` skip when their port cannot be bound again.
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
| `internal/testutil/serialline` | Faulty in-memory line, raw FT 1.2 peers and the goroutine check of the serial hostile and chaos suites, no production code |

The `interop` packages are outside the default build by their build tag, and `iec104/interop/cbsigma` is another module.

## Fuzz regressions

The targets include `FuzzParse` in `iec104/apci`, `FuzzDecode` in `asdu`, `ft12`, `iec103` and `sec`, and the protocol targets of the serial standards listed above. The seeds `testdata/fuzz/<Target>/capture-*` of `apci`, `asdu`, `ft12` and `iec103` are one APDU, frame or ASDU of every shape in the recorded captures of levels A to D (`make captures-manifest` writes them). A new decoder gets a target; `make fuzz` discovers it.

When a target fails, `go test` writes the input to `testdata/fuzz/<Target>/` in the package; CI uploads the same directory as the artifact `fuzz-failures`. To keep it as a regression test:

1. Copy the file into `testdata/fuzz/<Target>/` if it came from CI.
2. Reproduce it: `go test -run '<Target>/<file>' ./<package>`.
3. Fix the decoder and commit the file with the fix. Plain `go test` then runs it on every push.
