# Release notes

## v0.2.0

**Date:** not released (prepared)
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
