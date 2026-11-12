# Release notes

## v0.5.1

**Date:** 2026-11-12
**Previous release:** v0.5.0

Fixes a silent loss of data on a noisy line in every serial standard (IEC
101, 102, 103): the FT 1.2 decoder could take an E5 octet inside the user
data of a corrupted frame for a single-character answer.

### Fixed

- `ft12`: after rejecting a frame, `Decoder` resynchronized on the next
  start character inside the rejected octets. A single character (E5) has
  no checksum, so an E5 in the user data of a corrupted variable frame was
  returned as a frame: the controlling station took it for "no data"
  (or a positive acknowledgement), toggled FCB on its next request, and the
  message the corrupted frame carried was lost while the procedure ended
  successfully (otfabric/iec60870ctl's chaos test: 7 of 8 general
  interrogation messages). Since v0.1.0. Now a rejected variable frame
  whose two length octets agree is dropped whole (4 + L + 2 octets, also
  when the rest arrives in later reads), and after any other error a
  single character is accepted only from octets that arrive after a pause
  of `ErrorIdle` (10 ms), the line idle IEC 60870-5-1 asks for after an
  error. A real answer follows a request sent after the error, so it is
  not affected; a frame dropped in error is repeated by the link layer.

### Added

- `ft12.ErrorIdle`.

### Tests

- `ft12`: `TestDecoderNoSingleFromRejectedFrame` (five corruptions, one read
  and one octet per read), `TestDecoderDropsTheRestOfARejectedFrame`,
  `TestDecoderSingleAfterIdle`; `TestDecoderResync` now expects the E5 read
  with a rejected header to be dropped.

---

## v0.5.0

**Date:** 2026-11-12
**Previous release:** v0.4.1

Two lifecycle fixes, found by otfabric/iec60870ctl's tests: a link station
no longer outlives its Close, and the IEC 101 and IEC 103 clients no longer
wait in vain for a link whose transport has failed.

### Added

- `iec103/client`: `Client.Done` and `Client.Err`, as `iec101/client` has
  them: Done is closed when the transport failed or the client was closed,
  Err returns the transport error.

### Fixed

- `link`: `Close` of a `Primary`, `Secondary` or `Balanced` waits for the
  writer goroutine as well as the reader. `Config.OnFrame` is no longer
  called, and no frame is written, after `Close` returned; a frame queued
  before the station stopped is not sent after it.
- `iec101/client`, `iec103/client`: `WaitUp` returns the transport error
  when the transport fails before the link came up (a peer that closed the
  connection, say), instead of waiting until its context ended; it returns
  `ErrClosed` after `Close`. The client stops initializing the link on a
  failed transport instead of retrying every `RetryInterval`.
  (`iec102/client.Init` already returned the error.)

### Tests

- `link`: `TestCloseWaitsForTheWriter`; `iec101/client`, `iec103/client`:
  `TestWaitUpTransportClosedBeforeLinkUp`, `TestWaitUpCloseWhileWaiting`.
  Each fails on v0.4.1.

---

## v0.4.1

**Date:** 2026-11-12
**Previous release:** v0.4.0

`iec103/client` keeps every identification a station sends after a reset,
so the one with cause 4 is not lost behind a start/restart (cause 5).

### Added

- `iec103/client`: `Client.IdentificationFor(cause)` returns the last
  identification (ASDU 5) with that cause (IEC 60870-5-103:1997 7.4.1,
  table 8). `Identification` still returns the last one of any cause.

### Fixed

- `iec103/interop`: the identification case reads the cause 4
  identification with `IdentificationFor`. A station that follows clause
  7.4.1 and sends start/restart (cause 5) after it, as the fixed
  vitusdong/iec103-dotnet (vitusdong/iec103-dotnet#10) does, failed the
  case.

---

## v0.4.0

**Date:** 2026-11-12
**Previous release:** v0.3.0

More independent evidence for the serial standards: IEC 101 against a third
reference, digitalpetri/iec60870, and with four non-default sets of field
sizes; IEC 103 generic services and disturbance data against the harness's
contract 2.3, with the server's disturbance data transfer evidenced by
Open103. `iec104/client` returns from a counter freeze or reset at the
confirmation and shares the matching rules of `telecontrol`. SAST stays at
zero. All interop suites pin the images of iec60870-5-interop v0.4.0
(contract 2.3) by digest.

### Fixed

- `iec104/client`: `CounterInterrogate` with a freeze or reset qualifier
  returns at the activation confirmation with no ASDUs, instead of waiting
  for a termination the station does not send (IEC 60870-5-104 7.8;
  IEC 60870-5-101:2003 7.4.8 mode C, figures 95 and 96). It is still never
  retried. A termination or totals a station sends anyway reach the
  Handler.
- `telecontrol`: the test sequence counter of C_TS_TA_1 starts at 0
  (IEC 60870-5-104 8.8), as in `iec104/client`.

### Changed

- `iec104/client` uses the matching rules of `telecontrol` (`Mirrored`,
  `FromStation`) instead of its own copy; behaviour is unchanged. It keeps
  its own request handling; see docs/IEC104_PARITY.md for why it does not
  use `telecontrol.Requester`.

### Security hardening

- `internal/testutil/serialline`: the noise octet of the faulty test line is
  bounded to a byte before conversion (gosec G115). Its use of `math/rand`
  (G404, a seeded, reproducible test line, not security relevant) is
  recorded as a false positive in the organisation's triage records. SAST
  (gosec, opengrep) at the lowest thresholds: 0 findings, also in the nested
  module `iec104/interop/cbsigma`.

### Tests

- All interop suites pin the images of otfabric/iec60870-5-interop v0.4.0
  (contract 2.3) by multi-arch index digest (`iec101`, `iec102`, `iec103`
  and `iec104/interop/harness.go`, the Makefile, the Interop workflow); the
  testdata copies of its case and known-bug tables are those of v0.4.0. On
  those images (2026-11-12, linux/arm64): IEC 101 490 subtests passed and 5
  skipped (known bug), IEC 102 18 passed, IEC 103 75 passed and 38 skipped
  (known bugs, undeclared features), IEC 104 519 passed and 35 skipped, the
  cB-Sigma IEC 62351-5 tests pass; 0 failures.
- `iec101/interop`: digitalpetri/iec60870 as third reference, and the field
  sizes 2/2/2/3, 1/2/1/3, 2/1/2/2 and (balanced) 0/1/1/2 (link address,
  cause, common address, object address). This module's client: 5 + 5
  scenarios and 12 + 16 field-size runs pass against lib60870-C and against
  cB-Sigma (unbalanced + balanced), against digitalpetri 5 scenarios and 12
  field-size runs unbalanced; balanced against a digitalpetri station is
  skipped as the harness's known bug
  `digitalpetri-balanced-server-no-link-reset` (`testdata/known-bugs.tsv`),
  checked to reproduce on every run. The reference clients replay the
  harness's 64 cases (33 with field sizes) against this module's server:
  61 + 64 pass for lib60870-C and for cB-Sigma, 55 + 64 for digitalpetri (6
  cases with a common address other than its link address not run). 0
  failures ([INTEROPERABILITY.md](INTEROPERABILITY.md)).
- `iec103/interop`: the 7 cases of generic services and disturbance data and
  5 value checks. Open103's client against `iec103/server`: the disturbance
  transfer case passes and the description, tags and channel ratings match
  the fixture; its values hit the known Open103 bug `open103-asdu30-nfe`.
  vitusdong's client: the disturbance list case passes; the generic cases,
  the transfer and the value checks are known vitusdong bugs. This module's
  client against vitusdong's server: generic read, generic write and the
  list are known server bugs, asserted on the octets; the transfer is
  skipped (vitusdong declares no `disturbanceDataServer`). The bugs no
  harness pairing shows are in `testdata/known-bugs-consumer.tsv` (six
  entries); none is counted as a pass
  ([docs/iec103/INTEROPERABILITY.md](docs/iec103/INTEROPERABILITY.md)).
- Unit and e2e: the counter freeze returning at the confirmation
  (`iec104/client`), the C_TS_TA_1 counter starting at 0 (`telecontrol`),
  a full IEC 103 class 1 queue (`iec103/server`), a broadcast on a closed
  IEC 103 client.

### Documentation

- docs/IEC104_PARITY.md: the decision that `iec104/client` shares the
  matching rules of `telecontrol` but not `telecontrol.Requester`, and why.
- docs/iec103/INTEROPERABILITY.md: interpretation 12, the numbering of the
  disturbance values (NFE counts from 0, as TAP counts tag positions), and
  the contract 2.3 results.
- README, LIMITATIONS, ROADMAP, FEATURE-STATUS, STANDARDS: the IEC 101 and
  IEC 103 evidence of this release.

### Behaviour changes

`CounterInterrogate` with a freeze or reset qualifier returns at the
confirmation.

---

## v0.3.0

**Date:** 2026-11-11
**Previous release:** v0.2.0

Hardening: SAST to zero, chaos, hostile-peer and fuzz suites for the serial
standards (and the defects they found), IEC 104 checked against its text,
independent IEC 102 interoperability, and the IEC 104 suite on the harness
images. Several behaviours change; see "Behaviour changes".

### Fixed

- `iec101/client`, `iec103/client`: `WaitUp` returns only after
  `Config.OnState` for the change to `StateUp` has returned (the cause of a
  rare `TestBusy` failure under load: 2 in 200 runs, none in 260 after the
  fix). `State` still reports a change first.
- `link`: line noise or a peer stopping in the middle of a frame no longer
  silences a station: an unfinished frame followed by a pause longer than
  `Config.FrameGap` (default `Timeout`; negative disables) is discarded
  (`ft12.Decoder.Gap` for direct decoder users).
- `link`: when only answers that cannot answer the message arrive until the
  last time-out, `ErrUnexpected` also wraps `ErrTimeout`, so the IEC 101,
  102 and 103 clients take the link down and initialize it again instead of
  polling a confused station forever.
- IEC 101 and IEC 103 client requests return at their context deadline even
  while the link layer is still repeating their user data; after a failed
  poll the clients wait `PollInterval` before polling again.
- `iec103/client`: `Broadcast` and `BroadcastClockSync` return
  `iec103.ErrLinkDown` when the link goes down before the broadcast was
  sent, instead of blocking until Close.
- IEC 60870-5-104, checked against edition 1 (2000):
  - `apci`: an I frame without ASDU is refused on receipt (the connection
    closes) and on send (clause 5);
  - `apci`: `Params.Validate` accepts `w` up to 32767 independently of `k`
    (5.5, clause 9);
  - `iec104/client`: the test sequence counter of C_TS_TA_1 starts at 0
    (8.8);
  - `iec104/server`: `Mux` refuses a deactivation of C_CS_NA_1, C_RP_NA_1,
    C_TS_TA_1 and P_ME_NA/NB/NC_1 with cause 45 (7.1, 7.6, 7.9, 7.10, 8.8).
- `iec102/ree`: requests without information elements (C_FS_NA_2,
  C_TA_VC_2, C_RM_NA_2, C_PC_NA_2, C_DF_NA_2) encode VSQ 0, as the Beckhoff
  manual says and gisce sends (found on the wire).
- `iec103`: a generic data value whose DATASIZE or NUMBER exceeds 255 fails
  to encode instead of being announced modulo 256; `iec103/server` ends a
  disturbance transfer with TOO 34 for a record with more than 255 channels
  or 65535 values per channel instead of announcing a truncated count.

### Security hardening

- SAST (gosec, opengrep) at the lowest thresholds: 44 findings to 0. 34
  integer conversions are bounded or masked to their field widths (`cp`,
  `ft12`, `iec102`, `iec103`, `sec`, `telecontrol`); encodings are
  byte-identical. 10 findings in test-only capture tooling are recorded as
  false positives in the organisation's triage records.
- `iec101/examples/serial`: `-link` and `-ca` above 65535 are refused.

### Tests

- Hostile-peer suites for IEC 101 (both modes), 102 and 103 (noise, broken
  and foreign frames, FCB abuse, floods, half-open and slow lines, resets in
  the middle of exchanges); chaos suites over a line that drops,
  duplicates, corrupts, delays, splits and reorders (`make chaos`, Extended
  workflow, one job per standard); fuzz targets for the link layer
  (`FuzzSecondary`, `FuzzBalanced`, `FuzzPrimary`), `telecontrol`
  (`FuzzDispatch`) and the station receive paths of IEC 101, 102 and 103.
  Soak: 40 minutes under -race, about 400,000 frames, no duplicate
  execution, no leak.
- IEC 102 interop (`iec102/interop`, `make interop-iec102`, Interop job
  `iec102`): the gisce/iec870ree controlling station (REE profile,
  iec60870-5-interop contract 2.2) against `iec102/server`: 18 pass, 0
  fail. The first independent IEC 102 evidence, for the server only.
- All interop suites pin the images of otfabric/iec60870-5-interop v0.3.0
  (contract 2.2). The IEC 104 suite runs against its IEC 104 images
  instead of iec104-interop v0.5.0, with
  pascaldekloe/part5 as a fifth independent reference: 519 subtests pass,
  0 fail.

### Documentation

- docs/iec104/INTEROPERABILITY.md: clause-by-clause result against IEC
  60870-5-104 edition 1 (63 rows) and a clause 9 PICS summary; edition 2
  behaviour marked as not verifiable from the held text.
- New documented limit: the one-bit FCB of IEC 60870-5-2 cannot recognise a
  repetition delivered after the next message.
- All documents read against the code, the tests and each other: stale
  go-iec104 and iec104-interop references, the IEC 102, 103 and 104
  evidence, the serial stations in OBSERVABILITY.md and API.md, the
  security gaps and texts not held in LIMITATIONS.md.

- Logs: the serial stations log with component `iec101.client`,
  `iec101.server`, `iec102.*`, `iec103.*` (they carried an `iec104.`
  prefix) and name the error field `error` (was `err`), as IEC 104 does.
  The Makefile builds every example under its own name, `make e2e` runs
  the end-to-end suites of all four standards, `make bench` the codec
  benchmarks.

### Behaviour changes

The frame gap in `link`, `ErrUnexpected` wrapping `ErrTimeout`, requests
returning at their deadline, the IEC 103 broadcast error, the IEC 104 I
frame and `w` validation, the C_TS_TA_1 counter start, and the IEC 104 Mux
refusals.

---

## v0.2.0

**Date:** 2026-11-11
**Previous release:** v0.1.0

IEC 60870-5-102, conformance fixes from checking IEC 101, IEC 103 and the
application-layer security against the standard texts, recorded traffic for
every companion standard, and a defect it found. Several defaults and
behaviours change; see "Behaviour changes".

### IEC 60870-5-102 (new)

- Packages `iec102` (codec: integrated totals, single points, product
  specification, system time, read requests; everything else carried raw,
  octet for octet), `iec102/client`, `iec102/server`, opt-in Spanish REE
  profile `iec102/ree` (session login/logout, time set, tariffs, contracted
  power, special days), quickstart example.
- We do not hold the IEC 60870-5-102 text: every layout and procedure is
  traced to the Beckhoff TS650x manual (vendor documentation), recorded
  meter traffic (an Actaris SL762B: 749 frames decode and re-encode byte for
  byte) and the gisce/iec870ree test vectors; the classification standard /
  REE / vendor / unresolved and seven source conflicts are in
  docs/iec102/INTEROPERABILITY.md. No independent interoperability run yet.

### IEC 60870-5-101 and the link layer (checked against IEC 60870-5-101:2003)

- A secondary answers only status of link (9) and reset (0) before the
  first reset of remote link (figures 6, 9).
- While busy, DFC is set in every response and user data is refused
  (`link.FlowControlHandler`; the IEC 101 server reports busy above half its
  queue); `Init` waits for DFC = 0 and a balanced `Send` waits after DFC
  (figures 5, 8).
- Access demand (8) is answered with status of link (11); reset of FCB (7)
  only for a handler that implements `link.FCBResetHandler`, otherwise 15.
- Responses that cannot answer the request (a late repetition) are
  discarded; a frame is a repetition only when the FCB is unchanged and the
  function and data match the stored request.
- Empty user data fails with `link.ErrNoData`.
- Balanced responses carry the receiver's address (6.2.1.2);
  `link.Config.SenderAddr` restores the own address; either is accepted.
- Opt-in 101 profile checks: `iec101.Params.Strict`, `iec101.Validate`.

### IEC 60870-5-103 (checked against IEC 60870-5-103:1997 and six independent implementations)

- Server: the disturbance list after a GI carries the equipment's function
  type; a new GI or generic GI aborts the running cycle without its
  termination; TOO 34 ends a transfer and the updated list follows; user
  data is refused with NACK while DFC is set; time sync is corrected by the
  frame time at `Config.BitRate` (default 9600; negative `SyncDelay`
  disables); a broadcast time sync is not answered unless
  `AnswerBroadcastSync`; DCO 0/3, and OFF for INF 19 and 23-26, are
  acknowledged negatively, and so is, for a compatible function type, an
  INF outside table 17 (clause 7.2.5.2), which the client refuses to send;
  `Config.Fun` is required.
- Client: a write with execution over several ASDUs sends INF 248 ... 250
  with one RII; staged writes with `BeginWrite`; every ASDU 31 of a
  disturbance transfer is acknowledged (TOO 37/40 negatively and the
  transfer continues; TOO 34 with 65); time sync is stamped at transmission.
- Codec: VSQ, GDD and NDE ranges are checked (VSQ 01H is accepted for a
  single object, as ARIEC103 and Wireshark send it); RES1 of the time tags
  is ignored; INF 73 is accepted with ASDU 4; ASDU 3 and 9 carry up to 127
  measurands (recorded equipment sends 14 and 16; the standard says four
  and nine).
- Interop suite `iec103/interop` (`make interop-iec103`): the clients of
  vitusdong/iec103-dotnet, ARIEC103, Open103 and go-iecp5 replay the
  iec60870-5-interop cases against the server (53 pass, 0 fail, 6 known
  client differences, all ARIEC103's); the client runs against the vitusdong server, whose
  deviations are asserted as known differences.

### Application-layer security (checked against IEC TS 60870-5-7:2025)

- Security statistics reported as M_IT_TB_1, cause spontaneous, at
  configured information object addresses with thresholds
  (`sec.Config.Reports`, `Stat.DefaultThreshold`); counters wrap at 2^31;
  an expired remote certificate is counted as such.
- File deletion needs FILEMNGT (`sec.RequiredPermissionOf`,
  `Endpoint.PermitsASDU`).
- No monitor-direction data to an association whose roles lack MONITOR
  DATA (`sec.ErrNotPermitted`).
- Key usage defaults 24 h / 65535 messages (controlling station; twice that
  at the controlled station), from TS Table 9.
- Over TCP/IP only the 16-octet MACs are accepted by default
  (`sec.Config.Serial` for serial links).

### Behaviour changes

Link-state gating before reset, the balanced receiver address, the MAC
algorithms accepted by default, the key usage defaults, the IEC 103 time
correction default and the unanswered broadcast time sync. Each has an
option that restores the old behaviour where the standard allows it.

### Recorded traffic

- `telecontrol`: an activation termination that arrives right after a
  command's confirmation, before `Command` returned, now reaches the
  handler instead of being consumed by the finished request (found by
  replaying a recorded SEL RTAC select-before-operate against
  `iec101/client`; it depended on timing). Each request knows the answer
  that ends it; nothing is handed to a request after that.
- IEC 101 corpus (`iec101/testdata/captures`): SEL RTAC serial-line
  captures and an Energisa utility's lines (level B), generated captures
  (E). Every frame decodes and encodes back byte for byte, the link
  procedures check out, Wireshark agrees; the recorded stations are
  replayed against `iec101/client` and `iec101/server`.
- IEC 103 corpus (`iec103/testdata/captures`): SEL RTAC excerpts (B) and
  the ARIEC103 vectors (E). ASDU 9 with 14 and 16 measurands, sent by the
  recorded equipment, is refused as before (LIMITATIONS.md).
- IEC 102: a meter's frame log and the gisce test frames kept for the
  coming work, FT 1.2 checked.
- IEC 104 corpus: 36 more public captures (Industroyer2, an MIO8 RTU,
  Wireshark issues and wiki, ITI, nDPI, EmreEkin) under the corpus, oracle
  and replay suites.
- Shared capture reading in `internal/pcapio` (with the SEL RTAC serial
  link type) and FT 1.2 link analysis in `internal/serialcap`; fuzz seeds
  for `ft12`, `asdu` and `iec103` from the recordings.
- `make captures-verify` covers every corpus; `make captures-extended`
  runs the IEC 104 suites again (it pointed at removed paths), and the
  capture cache is git-ignored where the script writes it.

---

## v0.1.0

**Date:** 2026-11-11
**Previous release:** none

First version of go-iec60870-5.

- IEC 60870-5-104: the client, server, APCI, ASDU codec, file transfer,
  redundancy, TLS and IEC 62351-5 security of go-iec104 v0.3.0 (4a375b3),
  under `iec104/`, `asdu` and `sec`, with all of its tests, captures and the
  interop suite against iec104-interop v0.5.0. The `Logger` interfaces moved
  to the root package `iec60870`; see docs/IEC104_PARITY.md.
- `cp`: the IEC 60870-5-4 time tags, now shared; `asdu.Timestamp` is an
  alias of `cp.Timestamp`. Adds CP16Time2a and CP32Time2a.
- `ft12`: FT 1.2 frames and a resynchronizing stream decoder.
- `link`: IEC 60870-5-2 link procedures, unbalanced primary and secondary,
  balanced station.
- `telecontrol`: the request procedures IEC 101 and IEC 104 share. A
  counter freeze command returns at its confirmation (101:2003 7.4.8);
  delay acquisition is `MeasureDelay` (activation) and `SetDelay`
  (spontaneous), per 101:2003 7.4.12.
- `iec101`: IEC 60870-5-101 controlling station (`iec101/client`) and
  controlled station (`iec101/server`), unbalanced and balanced, with
  `Done`/`Err`, checked against IEC 60870-5-101:2003 (open deviations in
  LIMITATIONS.md). Interop suite `iec101/interop` (`make interop-iec101`)
  against the lib60870-C and cB-Sigma images of iec60870-5-interop.
- `iec103`: IEC 60870-5-103 codec for every compatible type, controlling
  system (`iec103/client`) and protection equipment (`iec103/server`),
  quickstart example. No independent interoperability evidence yet.
- `link` gains optional secondary hooks for IEC 103: `FCBResetHandler`,
  `FlowControlHandler`, `FunctionFilter`.

---
