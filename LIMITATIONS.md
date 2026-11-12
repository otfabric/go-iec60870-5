# Limitations

What go-iec60870-5 does not do, or does only partly. IEC 60870-5-104 has its
own list: [docs/iec104/LIMITATIONS.md](docs/iec104/LIMITATIONS.md).

## Scope

- Nothing is certified or conformance tested. Interoperability runs are not
  conformance tests.
- **Standard texts not held** ([docs/STANDARDS.md](docs/STANDARDS.md)):
  IEC 60870-5-102, Amendment 1 of IEC 60870-5-101, edition 2 and
  Amendment 1 of IEC 60870-5-104, parts 1 to 5 (only as profiled by the
  101 and 103 texts) and IEC 62351-5. Behaviour that depends on them is an
  interpretation and marked as such in the per-standard documents.

## Security

- Only IEC 60870-5-104 has security (TLS, and IEC 62351-5 with IEC TS
  60870-5-7). IEC 101, 102 and 103 have none; IEC TS 60870-5-7 covers 101
  and 104 only, and `sec` is not wired into `iec101`.
- Open gaps of `sec` against IEC TS 60870-5-7:2025, detailed in
  [SECURITY.md](SECURITY.md#checked-against-iec-ts-60870-5-72025): the
  curves secp256k1 and X448 (mandatory in the PICS), the contradictory
  width of the controlled station's random data (CGL; we use one octet, as
  cB-Sigma does), crossing Session Initiation Requests accepted, the
  revocation statistics stay 0, and no broadcast session key, IEC 62351-8
  tokens, brainpool, BLAKE2s or certificate revocation.
- The procedures that IEC TS 60870-5-7 delegates to IEC 62351-5 were
  derived from cB-Sigma/iec60870 and are verified against that one
  implementation only.

## IEC 60870-5-101

- **Independent interoperability evidence** covers the default field sizes
  and the sets 2/2/2/3, 1/2/1/3, 2/1/2/2 and (balanced) 0/1/1/2 against
  lib60870-C, cB-Sigma and digitalpetri/iec60870 (iec60870-5-interop v0.5.0,
  contract 2.3, pinned by digest); it is interop with these stacks, not
  with field devices. Balanced against a digitalpetri
  station nothing beyond the link start-up interworks: the station does not
  initialize its own direction (an upstream defect, known-bugs.tsv). See
  [INTEROPERABILITY.md](INTEROPERABILITY.md).
- **Line idle after an error.** IEC 60870-5-101:2003 relaxes the
  inter-character rule (6.1: the receiver need not measure gaps), but after
  a receive error a station looks for a line idle of 33 bits (figures 6 and
  9). `ft12.Decoder` cannot see character timing on an `io.Reader`; it
  hunts for the next start character instead, and the checksum rejects
  false frames. What it can see is the time between reads: the stations
  discard a frame whose next octet comes later than `link.Config.FrameGap`
  (default `Timeout`), so a start character in line noise or a peer that
  stopped mid-frame does not swallow the frames that follow. A transport
  that pauses longer than that inside a frame needs a larger `FrameGap`.
- **The FCB is one bit** (IEC 60870-5-2 5.1; also IEC 60870-5-102 and
  -103): a repetition that a transport delivers after the primary's next
  message is a new message to the secondary and is executed again. A
  serial line delivers in order; over a transport that may reorder or hold
  back frames (a terminal server, a radio link with retransmission) keep
  `link.Config.Timeout` above its worst delay. The chaos suites found it
  when they let frames of the primary overtake each other; they now keep
  primary messages in order (`link.TestRepetitionAfterTheNextMessage`).
- **One controlled station per client.** The link layer filters by address
  and a `link.Primary` can poll several secondaries, but `iec101/client`
  serves one link address. Multidrop polling needs one client per address on
  separate lines, or the link layer directly.
- **No file transfer** and **no application-layer security** over IEC 101,
  although the ASDU codec carries the file transfer types.
- **Class assignment** is the application's: `server.Send` queues class 1,
  `server.SendClass` chooses. The library does not move class 2 data to
  class 1 when class 2 is empty.
- **Balanced test function** is sent every `TestInterval` regardless of
  other traffic.
- **Open deviations from IEC 60870-5-101:2003:**
  - While busy, reset of remote link (0) is still acknowledged, where
    figures 6 and 9 show NACK with DFC = 1.
  - The default link time-out (1 s) is short for slow lines: 101 6.2.2
    gives the formula (e.g. 2.25 s at 1200 bit/s with 240-octet frames).
    Set `link.Config.Timeout` from the line speed.
  - The 101 profile checks are opt-in (`iec101.Params.Strict`).
  - No automatic interrogation after end of initialization or link
    recovery (7.4.5): the application issues it. The server does not apply
    the transmission delay to clock synchronization (7.4.6, 7.4.12).
  - The balanced receiver address (6.2.1.2) is backed by the text and a
    manual run against lib60870-C only.

## IEC 60870-5-102

- **We do not hold the standard's text.** All layouts and procedures are
  interpretations of vendor documentation (Beckhoff TS650x), recorded
  traffic of one meter and other implementations; see
  [docs/iec102/INTEROPERABILITY.md](docs/iec102/INTEROPERABILITY.md).
- **Independent evidence for the server only, and only for the REE
  profile as one master uses it**: gisce/iec870ree (the iec102-gisce image
  of iec60870-5-interop, contract 2.2) runs its session, integrated totals
  (C_CI_NT_2, C_CI_NU_2), clock, product and tariff requests against
  `iec102/server` with `ree.Session` (18 pass, 0 fail, 0 known
  differences; `make interop-iec102`). Two octets of link and common
  address only; the other read commands (C_CI_NA_2 .. C_CI_NS_2), single
  points, blocks and the REE configuration requests are not exercised; the
  tariff answers come from the suite's handler, not from `iec102/server`.
  The client has no independent peer: we found no open controlled station
  (meter), so its evidence is the recorded traffic of one meter.
- The layout of M_EI_NA_2 (70) is unknown (carried raw); signatures are not
  checked; the server has no class 1 or spontaneous data and does not
  enforce a session; one station per client.

## IEC 60870-5-103

- Independent evidence is mostly for the server: the clients of
  vitusdong/iec103-dotnet, ARIEC103, Open103 and riclolsen/go-iecp5 replay
  the harness cases against it (53 pass, 0 fail, 7 skipped: 6 known
  client differences, 1 feature a client does not declare). The client has only vitusdong's server to run against,
  which deviates from the standard in its GI answers, command
  acknowledgements and identification: link start-up, clock
  synchronization and the GI termination pass, the rest is unverified
  against an independent server. The server's disturbance transfer is
  exercised by Open103 (its values hit a known Open103 bug); generic
  services, the disturbance list and the client side of both have no
  conforming reference; broadcast synchronization is exercised by no
  reference; the cause 12
  report after a command only towards the reference clients. See
  [docs/iec103/INTEROPERABILITY.md](docs/iec103/INTEROPERABILITY.md).
- One controlling system polls one protection device per client (no shared
  line with several devices).
- Test mode and monitor-direction blocking are left to the application.
- The 23 h rule for the IV bit is tested with a shortened timeout only.
- The time correction covers the frame time, not internal delays.
- The client cannot abort a disturbance transfer.
- A generic data element is never split over several ASDUs (the GDD CONT
  bit); long answers are split by element.
- The 15 s limit on DFC (cl. 6.3) is not enforced.
- Interpretations of the text are listed in
  [docs/iec103/INTEROPERABILITY.md](docs/iec103/INTEROPERABILITY.md).
