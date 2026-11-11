# Release notes

## v0.2.0

**Date:** not released (prepared)
**Previous release:** v0.1.0

Recorded traffic for the serial companion standards, more for IEC 104,
and one defect it found.

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
