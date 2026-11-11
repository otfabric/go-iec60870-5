# Feature status

What the IEC 60870-5-104 packages of this module (`iec104`, `iec104/apci`, `iec104/client`, `iec104/server`, with `asdu` and `sec`) implement and what evidence backs it, at v0.3.0. The interop suite runs against the IEC 104 images of [iec60870-5-interop v0.2.1](https://github.com/otfabric/iec60870-5-interop/releases/tag/v0.2.1). The type identifications and causes, clause by clause, are in [INTEROPERABILITY.md](INTEROPERABILITY.md); what is out of scope is in [LIMITATIONS.md](LIMITATIONS.md); the status of the other companion standards is in [docs/FEATURE-STATUS.md](../FEATURE-STATUS.md).

None of this is a conformance claim: nothing is certified, and nothing has been run against certified test equipment or field devices.

## 1. Versions

| go-iec60870-5 | Go | IEC 104 interop references | Supported |
|---|---|---|---|
| **v0.3.0** | 1.26 or later (CI: 1.26 and 1.27) | IEC 104 images of iec60870-5-interop v0.2.1 (five references) | see [SECURITY.md](../../SECURITY.md#supported-versions) |
| v0.1.0, v0.2.0 | 1.26 or later | images of iec104-interop v0.5.0 (four references) | see [SECURITY.md](../../SECURITY.md#supported-versions) |

The IEC 104 code was carried over from go-iec104 v0.3.0 ([IEC104_PARITY.md](../IEC104_PARITY.md)); go-iec104's own versions are not listed here.

## 2. Platforms

The library is pure Go with no dependencies outside the standard library.

| Platform | Tested | Notes |
|---|---|---|
| Linux amd64 | the full test suite, the interop suite and `iec104/interop/cbsigma` in CI | |
| Linux arm64, Linux arm, macOS arm64, Windows amd64 | `make cross`, build only, not in CI | tests are not run there by CI |

## 3. Feature support

✅ supported · ◐ partly (see the notes) · ⚙ codec only (encode and decode, no procedure) · — not implemented.

**E2E**: the client and server of this module exercise it in `iec104/e2e`, named by test. **Independent**: software that is not this library exchanged it with this library in the `iec104/interop` suite, against the images pinned by digest. "All five" means lib60870-C v2.4.1, OpenMUC j60870 1.8.0, wendy512/iec104 v1.0.4 on go-iecp5 v1.2.6, cB-Sigma/iec60870 v0.3.0 and pascaldekloe/part5 v0.3.0.

### Link and connection

| Feature | Status | E2E | Independent |
|---|---|---|---|
| I, S and U frames, sequence numbers | ✅ | `TestWireMatrix`, `TestSequenceNumberWrap` | all five |
| STARTDT, STOPDT | ✅ | `TestDataTransfer`, `TestStartStopStaysInStep` | all five |
| `k`, `w` windows | ✅ | `TestNarrowParameters` | all five, at `k = w = 1` |
| `t3` idle test | ✅ | `TestIdleConnection` | all five, both directions |
| `t1` closes a silent link | ✅ | `TestSilentLink` | none |
| Client reconnect | ✅ | `TestReconnect` | all five, after a station restart |
| Client retries of read-only requests | ✅ | `TestRetry` | none |
| Concurrent sessions | ✅ | `TestConcurrentSessions` | all five |
| TLS (IEC 62351-3) | ✅ | the `tls` variant of the station scenarios, `TestMutualTLS` | none |

### Application functions

| Feature | Status | E2E | Independent |
|---|---|---|---|
| Station interrogation | ✅ | `TestInterrogation` | all five |
| Counter interrogation | ✅ | `TestInterrogation` | all five |
| Read | ✅ | `TestRead` | lib60870-C, wendy512, cB-Sigma, part5; j60870 closes the connection (known bug) |
| Clock synchronization, test command | ✅ | `TestSystemCommands` | all five |
| Commands, direct and select-before-operate, deactivation | ✅ | `TestCommands`, `TestSelectBeforeOperate` | all five |
| Commands with time tag | ✅ | `TestCommands` | lib60870-C, j60870, cB-Sigma, part5; go-iecp5 drops them |
| Refusals (unknown type, cause, common address, object) | ✅ | `TestRefusals` | all five |
| Types outside the reference fixture (protection events, packed single points, bitstring commands, parameters, reset process, delay acquisition, end of initialization) | ✅ | `TestWireMatrix` | none: this repository's tests only |
| Other field sizes, IEC 60870-5-101 widths | ✅ | `TestWireMatrixOtherParameters` | none |
| Private types 128..255, raw payload | ✅ | `TestWireMatrix` | none |

### File transfer

| Feature | Status | E2E | Independent |
|---|---|---|---|
| Download, `Client.GetFile` | ✅ | `TestFileTransfer` | lib60870-C, cB-Sigma |
| Download, `server.FileServer` | ✅ | `TestFileTransfer` | lib60870-C, j60870, cB-Sigma |
| Upload, directory | ✅ | `TestFileUploadAndDirectory` | lib60870-C, j60870, cB-Sigma both ways; wendy512 as upload client and for the directory (`TestClientUpload`, `TestServerUpload`, `TestClientDirectory`, `TestServerDirectory`) |
| File deletion, query log | ⚙ | none | none |

### Redundancy and events

| Feature | Status | E2E | Independent |
|---|---|---|---|
| Client redundancy group, failover, switchover | ✅ | `TestRedundancyGroup`, `TestSwitchoverStaysInStep` | one group across two reference stacks |
| Server redundancy groups | ✅ | `TestServerRedundancyGroups`, `TestReconnectingStandbyDoesNotEvictActive`, `TestFailoverThenRejoinDoesNotEvict` | none |
| Event queue with acknowledged delivery, in memory | ✅ | `TestEventQueue`, `TestEventQueueOverflow` | reference clients (`TestServerEventQueue`) |
| Combined station on one connection | — | | |

### Application-layer security (IEC 62351-5:2023 with IEC TS 60870-5-7:2025)

| Feature | Status | E2E | Independent |
|---|---|---|---|
| Station Association, Session Key Change, Secure Data, both roles | ✅ | the `secure` and `secure-gcm-tls` variants of the station scenarios | cB-Sigma only, container and in process |
| Role-based access control | ✅ | `TestSecurityRoles` | cB-Sigma only |
| Several controlling stations, refused station | ✅ | `TestSecurityStations`, `TestSecurityRefusedStation` | cB-Sigma only, for a refused AIM |
| Security statistics reported as M_IT_TB_1 (`sec.Config.Reports`) | ✅ | `TestSecurityTS7`; unit tests in `sec/ts7_test.go` | none |
| Broadcast session key, IEC 62351-8 tokens, certificate revocation lists, secp256k1, X448 (mandatory in the TS PICS), brainpool | — | | |

The security was derived from cB-Sigma/iec60870, not from the IEC documents, so that one implementation is not a test of it against the standard. Its framing has since been checked against the text of IEC TS 60870-5-7:2025; the gaps found are listed in [SECURITY.md](../../SECURITY.md#checked-against-iec-ts-60870-5-72025).

### Observability

| Feature | Status | E2E | Independent |
|---|---|---|---|
| Structured logging, metrics hooks, state callbacks | ✅ | `TestStatesLogsAndMetrics` | n/a |

### Recorded traffic

Recordings of other implementations, replayed against the client and the server ([testdata/captures](../../iec104/testdata/captures/README.md)). Evidence level B is credibly documented physical-device traffic (the MIO8 RTU of Wireshark issue 17209), C a recording of unknown origin, D a software testbed, E generated; none is A.

| Feature | Recorded, replayed | Level |
|---|---|---|
| APCI framing, TCP reassembly, sequence numbers, `k`/`w` | every committed capture (counts per file in [manifest.json](../../iec104/testdata/captures/manifest.json)) | B, C, D, E |
| STARTDT, STOPDT, TESTFR both ways | QTester104 ↔ IEC TestServer, CERT.LV traces | C, D |
| Station and counter interrogation | QTester104 ↔ IEC TestServer, CERT.LV traces | C, D |
| Interrogation answered with parameters (P_ME_*) | CERT.LV `errors` | D |
| Commands (single, double, step, set points, bitstring), confirmation, termination | CERT.LV `interrogation-commands`, UOWM attacks | C, D |
| Read, refused | CERT.LV `errors`, UOWM attack | D |
| Clock synchronization to the broadcast address | CERT.LV traces | D |
| File download, upload | CERT.LV `file-read`, `file-write` (three files each) | D |
| All type identifications, causes, SQ | Wireshark's synthetic capture | E |
| TLS, IEC 62351-5 in real traffic, redundancy, directory, select-before-operate | none of the recordings we found | — |

## 4. Interop results

Peer versions, digests and per-direction results are in [INTEROPERABILITY.md](INTEROPERABILITY.md#verification-status). The Interop workflow runs on every push and pull request that changes more than Markdown.

## 5. Not verified

- Certified test equipment, field devices and any conformance test. Of the recorded traffic in `iec104/testdata/captures` only the MIO8 recordings (level B) come from a physical device, and its make and firmware are not documented.
- `t1` and `t2` behaviour against another implementation.
- Redundancy over TLS against another implementation: the interop suite has no case for it.
- IEC 62351-5 against anything other than cB-Sigma/iec60870.
- Any platform other than Linux amd64, in CI.
