# Limitations

What go-iec60870-5 does not do, or does only partly. IEC 60870-5-104 has its
own list: [docs/iec104/LIMITATIONS.md](docs/iec104/LIMITATIONS.md).

## Scope

- **IEC 60870-5-102 is not implemented.** See [docs/ROADMAP.md](docs/ROADMAP.md).
- Nothing is certified or conformance tested. Interoperability runs are not
  conformance tests.

## IEC 60870-5-101

- **Independent interoperability evidence covers the default field sizes
  only**, against two references that are not fully independent of each
  other, run locally (not in CI: the reference images are not published
  yet). See [INTEROPERABILITY.md](INTEROPERABILITY.md).
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
- **Open deviations from IEC 60870-5-101:2003** (found by the 2026-11-11
  check against the text; fixed then: delay acquisition, counter freeze):
  - Balanced address field (6.2.1.2): the text says it carries the
    address "of the receiver" in primary and secondary messages; secondary
    frames here carry the station's own address, as lib60870 does. The
    wording is ambiguous in the translation we hold; not changed. Distinct
    balanced addresses have not been run against a reference.
  - Link states (figures 5, 6, 8, 9): a secondary answers before the first
    reset of remote link instead of answering only function 9 and 0; while
    the server's input queue is busy DFC is set on the NACK only, not on
    every response; a primary initializes without waiting for DFC = 0.
  - Function codes (table 3): access demand (8) is answered with 15
    instead of status (11); reset of FCB (7, an IEC 60870-5-103 extension)
    is acknowledged on 101 links too.
  - The default link time-out (1 s) is short for slow lines: 101 6.2.2
    gives the formula (e.g. 2.25 s at 1200 bit/s with 240-octet frames).
    Set `link.Config.Timeout` from the line speed.
  - The codec accepts the 104-only types under 101 parameters and does not
    enforce the 101 rules on SQ, single objects of system types, cause 0,
    common address 0 and broadcast use; the client passes unknown types,
    causes and addresses to the handler instead of ignoring them; empty
    user data goes out in a variable frame.
  - No automatic interrogation after end of initialization or link
    recovery (7.4.5): the application issues it. The server does not apply
    the transmission delay to clock synchronization (7.4.6, 7.4.12).

## IEC 60870-5-103

- **No independent interoperability evidence yet.**
- One controlling system polls one protection device per client (no shared
  line with several devices).
- Test mode and monitor-direction blocking are not server features; the
  application can send those causes itself.
- A generic data element is never split over several ASDUs (the GDD CONT
  bit); long answers are split by element.
- The controlling side does not send abort orders during a disturbance
  transfer.
- The 15 s limit on DFC (cl. 6.3) is not enforced.
- Interpretations of the text are listed in
  [docs/iec103/INTEROPERABILITY.md](docs/iec103/INTEROPERABILITY.md).

