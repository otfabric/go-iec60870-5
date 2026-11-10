# Limitations

What go-iec60870-5 does not do, or does only partly. IEC 60870-5-104 has its
own list: [docs/iec104/LIMITATIONS.md](docs/iec104/LIMITATIONS.md).

## Scope

- **IEC 60870-5-103 and IEC 60870-5-102 are not implemented.** See
  [docs/ROADMAP.md](docs/ROADMAP.md).
- Nothing is certified or conformance tested. Interoperability runs are not
  conformance tests.

## IEC 60870-5-101

- **No independent interoperability evidence yet**
  ([INTEROPERABILITY.md](INTEROPERABILITY.md)).
- **Character timing.** IEC 60870-5-1 requires a receiver to reject a frame
  with an idle interval between its characters and a line idle interval
  between frames. `ft12.Decoder` works on an `io.Reader` and cannot see
  timing; it validates structure and checksum and resynchronizes. On a real
  line, configure the serial port's read timeout accordingly.
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
- **Text we do not hold.** IEC 60870-5-2 and -101 are not in our document
  set; the link procedures follow the extracts quoted in IEC 60870-5-103 and
  the behaviour of independent implementations
  ([docs/STANDARDS.md](docs/STANDARDS.md)).
