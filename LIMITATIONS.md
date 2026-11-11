# Limitations

What go-iec60870-5 does not do, or does only partly. IEC 60870-5-104 has its
own list: [docs/iec104/LIMITATIONS.md](docs/iec104/LIMITATIONS.md).

## Scope

- Nothing is certified or conformance tested. Interoperability runs are not
  conformance tests.

## IEC 60870-5-101

- **Independent interoperability evidence covers the default field sizes
  only**, against two references that are not fully independent of each
  other (the Interop workflow runs the suite). See [INTEROPERABILITY.md](INTEROPERABILITY.md).
- **Line idle after an error.** IEC 60870-5-101:2003 relaxes the
  inter-character rule (6.1: the receiver need not measure gaps), but after
  a receive error a station looks for a line idle of 33 bits (figures 6 and
  9). `ft12.Decoder` cannot see timing on an `io.Reader`; it hunts for the
  next start character instead, and the checksum rejects false frames.
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
  The client has no independent peer: no open controlled station (meter)
  exists, so its evidence is the recorded traffic of one meter. The image
  is a local build until the harness release (v0.3.0) publishes it.
- The layout of M_EI_NA_2 (70) is unknown (carried raw); signatures are not
  checked; the server has no class 1 or spontaneous data and does not
  enforce a session; one station per client.

## IEC 60870-5-103

- Independent evidence is mostly for the server: the clients of
  vitusdong/iec103-dotnet, ARIEC103, Open103 and riclolsen/go-iecp5 replay
  the harness cases against it (53 pass, 0 fail, 6 known client
  differences). The client has only vitusdong's server to run against,
  which deviates from the standard in its GI answers, command
  acknowledgements and identification: link start-up, clock
  synchronization and the GI termination pass, the rest is unverified
  against an independent server. Generic services, disturbance data and
  broadcast synchronization are exercised by no reference; the cause 12
  report after a command only towards the reference clients. The images are not yet published
  (`make interop-iec103` uses local builds until the harness v0.2.0
  release); see docs/iec103/INTEROPERABILITY.md.
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
