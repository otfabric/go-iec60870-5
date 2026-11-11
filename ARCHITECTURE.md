# Architecture: otfabric/go-iec60870-5

How the module is layered, what is shared between the companion standards
and why. The IEC 104 connection machinery (APCI, timers, redundancy,
security in the loop) is described in
[docs/iec104/ARCHITECTURE.md](docs/iec104/ARCHITECTURE.md).

## Layers

IEC 60870-5 is a family: parts 1 to 5 define frame formats, link
procedures, data structures, information elements and application
functions; the companion standards (101, 102, 103, 104) select from them.
The packages follow that split, and share code only where the companion
standards share the definition.

```text
iec103/client iec103/server  iec101/client  iec101/server        iec104/client  iec104/server
       \        /              \           /                      \           /
        iec103 (cp)            iec101  ----------- telecontrol ---  iec104 -- iec104/apci
                        |                     |                       (TCP, APCI)
                        |                    asdu  ------------------ sec
                        |                     |
                       link (IEC 60870-5-2)   cp (IEC 60870-5-4 time tags)
                        |
                       ft12 (IEC 60870-5-1)
                        |
                    io.ReadWriter: serial port, terminal server TCP stream, pipe
```

| Package | Shared by | Why there and not elsewhere |
|---|---|---|
| `cp` | 101, 104, 103 | IEC 60870-5-4 defines the time tags once; 103 uses CP32Time2a and CP56Time2a with the same octets. |
| `ft12` | 101, 102, 103 | IEC 60870-5-1 FT 1.2 is the only frame format the three serial companion standards allow (103: clause 6.1). |
| `link` | 101, 102, 103 | IEC 60870-5-2 procedures. The companion standards restrict them (function codes, address size, single character) through `link.Config` and caller choices; nothing standard-specific is built in. The one extension, function code 7 "reset FCB" of IEC 60870-5-103 clause 6.3, is a separate method (`Primary.ResetFCB`) a 101 station never calls. |
| `asdu` | 101, 104 | The telecontrol ASDU (type identifications 1..127 of IEC 60870-5-101 clause 7 and 104 clause 9) with configurable field sizes. IEC 102 and IEC 103 define different ASDUs and will have their own codecs. |
| `iec103` | 103 only | Its own ASDU (one-octet data unit identifier, FUN/INF addressing, types 1-31), IEC 60870-5-103 cl. 7. |
| `telecontrol` | 101 (104 later) | How a controlling station recognises the answers to its requests. Neither 101 nor 104 has a transaction identifier; both use the same matching rules. |
| `iec104/...` | 104 only | APCI framing, sequence numbers, k/w, t0..t3, TLS, redundancy groups: nothing of it applies to a serial line. |
| `sec` | 104 (101 planned) | IEC TS 60870-5-7 profiles IEC 62351-5 for 101 and 104; it is wired into 104 only. |

What is deliberately not shared: there is no universal client or server
interface across the companion standards. IEC 103 (protection equipment,
function type and information number) and IEC 102 (integrated totals) get
APIs that follow their own application models.

## Import rules

- `cp`, `ft12` import only the standard library.
- `link` imports `ft12`.
- `asdu` imports `cp`; `telecontrol` imports `asdu`.
- `iec101` imports `asdu`, `ft12`, `link`; `iec101/client` and
  `iec101/server` add `telecontrol` and `internal/logx`.
- `iec103` imports `cp`; `iec103/client` and `iec103/server` add `ft12`,
  `link` and `internal/logx`.
- The `iec104` tree imports `asdu`, `sec` and the root package; nothing
  outside `iec104/` imports it.
- The root package `iec60870` holds only the `Logger` interfaces; every
  package may import it.
- The graph is acyclic; `go vet` enforces it.

## IEC 101 stations

### Transport

Every serial station takes an `io.ReadWriter` and owns it: `Close` closes
it when it is an `io.Closer`. A station runs two goroutines on it:

- a reader that decodes FT 1.2 frames (`ft12.Decoder`, resynchronizing
  after noise) and hands each frame to the station logic;
- a writer that sends queued frames, so a station never blocks its receive
  path on a full transport. Balanced stations answer from the reader while
  their peer does the same; with synchronous transports (`net.Pipe`) a
  direct write would deadlock.

IEC 60870-5-1 also requires a receiver to discard a frame interrupted by a
line idle interval. A generic `io.Reader` does not expose character timing,
so `ft12` validates structure and checksum only; see
[LIMITATIONS.md](LIMITATIONS.md).

### Link layer

`link.Primary` (unbalanced) serializes exchanges: one message out, wait
`Config.Timeout` for the response, repeat the identical frame (same FCB)
up to `Config.Retries` times. It toggles FCB per secondary for each new
message with FCV set and clears it after reset of remote link.

`link.Secondary` keeps its expected FCB; a message with FCV set whose FCB
equals the previous one is a repetition and is answered with the stored
previous response without calling the user again. This is what keeps a
lost ACK from executing a command twice (test
`TestRepetitionNotProcessedTwice`).

IEC 103 adds optional secondary hooks a `SecondaryHandler` may implement:
`FCBResetHandler` (function 7, 103 cl. 6.3), `FlowControlHandler` (DFC on
every response) and `FunctionFilter` (function codes outside 103 cl. 6.2.3
answered with 15, FCB still advancing). IEC 101 does not use them.

`link.Balanced` is a primary for its own direction and a secondary for the
peer's, over a full-duplex line, with the DIR bit set by station A.

### Application layer

`iec101/client` runs one goroutine per role:

- unbalanced: a poller that initializes the link (request status of link,
  reset of remote link), sends queued user data, and polls class 1 while
  the station reports ACD, class 2 otherwise, pausing `PollInterval` when
  the station has nothing; DFC holds back user data;
- balanced: a supervisor that initializes this station's direction and
  sends the test function for link when idle.

Received ASDUs go to `telecontrol.Requester.Dispatch`; what no request
consumes is delivered to the application's handler on a goroutine of its
own, in order, so a handler may issue requests.

`iec101/server` holds class 1 and class 2 queues (unbalanced) or a send
queue (balanced). Control-direction ASDUs are queued to a handler goroutine;
when that queue is full the link answers NACK with DFC set, as
IEC 60870-5-2 provides. After the first reset of remote link it sends end of
initialization (M_EI_NA_1).

## Testing

See [TESTING.md](TESTING.md). The IEC 101 layers are tested bottom up:
`ft12` with known frames, every truncation and a fuzz target; `link` over
`net.Pipe` with lossy wrappers and scripted peers that send wrong or negative
answers; `iec101/e2e` runs client against server in both modes, including
link loss and recovery. Interoperability with independent implementations
runs from otfabric/iec60870-5-interop.
