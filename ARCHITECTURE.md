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
iec102/client iec102/server (iec102/ree: REE profile, opt-in)
       \        /
        iec102 (time a, time b)
            |
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
| `asdu` | 101, 104 | The telecontrol ASDU (type identifications 1..127 of IEC 60870-5-101 clause 7 and 104 clause 9) with configurable field sizes. IEC 102 and IEC 103 define different ASDUs and have their own codecs. |
| `iec102` | 102 only | Its own ASDU (record address, integrated totals with a common time tag) and its own time formats (time a CP40Time2a, time b CP56Time2b), which are not IEC 60870-5-4 types and so not in `cp`. We do not hold the 102 text; sources and classification in docs/iec102/INTEROPERABILITY.md. The Spanish REE profile is `iec102/ree`, applied only through `iec102.Params.Profile` and its helpers. |
| `iec103` | 103 only | Its own ASDU (one-octet data unit identifier, FUN/INF addressing, types 1-31), IEC 60870-5-103 cl. 7. |
| `telecontrol` | 101 (104 later) | How a controlling station recognises the answers to its requests. Neither 101 nor 104 has a transaction identifier; both use the same matching rules. |
| `iec104/...` | 104 only | APCI framing, sequence numbers, k/w, t0..t3, TLS, redundancy groups: nothing of it applies to a serial line. |
| `sec` | 104 only | IEC TS 60870-5-7 profiles IEC 62351-5 for 101 and 104; it is wired into 104 only. `Config.Serial` selects the MAC lengths of serial links. The `Endpoint` also emits the statistic reports (M_IT_TB_1 Secure Data) with the output of the call that made them due, and refuses monitor-direction data for roles without MONITOR DATA in `Protect`, so the transport needs no extra wiring. |

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
- `iec102` imports `ft12` (link address sizes); `iec102/client` and
  `iec102/server` add `link` and `internal/logx`; `iec102/ree` imports
  `iec102`, `iec102/client` and `iec102/server`.
- `iec103` imports `cp`; `iec103/client` and `iec103/server` add `ft12`,
  `link` and `internal/logx`.
- `sec` imports `asdu`.
- The `iec104` tree imports `asdu`, `sec`, `internal/logx` and the root
  package; nothing outside `iec104/` imports it.
- The root package `iec60870` holds only the `Logger` interfaces; every
  package may import it.
- Test infrastructure of the capture suites: `internal/pcapio` (pcap,
  pcapng, TCP reassembly, RTAC serial lines) imports only the standard
  library; `internal/serialcap` adds `ft12` and `link`;
  `internal/testutil/corpus` adds `pcapio`. The `<standard>/internal/capture`
  packages build on them; no production package imports any of them.
- The graph is acyclic; the Go toolchain refuses import cycles.

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

IEC 60870-5-101:2003 (6.1) does not require a receiver to measure gaps
between characters, but after a receive error it looks for a 33-bit line
idle. A generic `io.Reader` does not expose timing, so `ft12` validates
structure and checksum and hunts for the next start character instead, and
discards an unfinished frame when the time between reads inside it exceeds
`link.Config.FrameGap` (`ft12.Decoder.Gap`); see
[LIMITATIONS.md](LIMITATIONS.md#iec-60870-5-101).

### Link layer

`link.Primary` (unbalanced) serializes exchanges: one message out, wait
`Config.Timeout` for the response, repeat the identical frame (same FCB)
up to `Config.Retries` times. It toggles FCB per secondary for each new
message with FCV set and clears it after reset of remote link. A response
whose function code cannot answer the message sent (an ACK while a class 2
poll is outstanding: a late repetition) is discarded and the real answer
awaited; up to 8 frames queue for the exchange, and those left over are
discarded before the next message. When only such responses arrive until
the last repetition times out, the error wraps both `ErrUnexpected` and
`ErrTimeout`, so the IEC 101, 102 and 103 clients take the link down and
initialize it again. `Init` repeats request status of link
while the secondary reports DFC (IEC 60870-5-101:2003 figure 5).

`link.Secondary` follows the states of IEC 60870-5-101:2003 figure 6:
until the first reset of remote link it answers only request status of
link and reset of remote link. It keeps its expected FCB; a message with
FCV set whose FCB equals the previous one, and whose function code and
user data equal the previous message's, is a repetition and is answered
with the stored previous response without calling the user again. This is
what keeps a lost ACK from executing a command twice (test
`TestRepetitionNotProcessedTwice`); comparing function and data keeps a
master that does not toggle the FCB from getting a stale answer
(`TestSameFCBOtherRequest`).

Optional secondary hooks a `SecondaryHandler` may implement:
`FCBResetHandler` (function 7, 103 cl. 6.3; without it function 7 is
answered 15, as IEC 60870-5-101 table 3 has no function 7),
`FlowControlHandler` (DFC on every response, user data refused with NACK:
"secondary link busy", 101 figures 6 and 9; a `BalancedHandler` may
implement it too) and `FunctionFilter` (function codes outside 103 cl.
6.2.3 answered with 15 in any state, FCB still advancing). The IEC 101
server and the balanced client report busy while their handler queue is
more than half full.

`link.Balanced` is a primary for its own direction and a secondary for the
peer's, over a full-duplex line, with the DIR bit set by station A. Its
secondary has the not-reset state of figure 9; its primary waits for DFC
to clear before sending user data again (figure 8). Its responses carry the
receiver's address (6.2.1.2) unless `Config.SenderAddr`, and it accepts
responses with either address.

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
while that queue is more than half full every response carries DFC and user
data is refused with NACK, as IEC 60870-5-101:2003 figures 6 and 9 provide.
A balanced server sends a refused ASDU again. With `Params.Strict` both
client and server apply `iec101.Validate`, the 101 profile checks the
shared codec leaves out. After the first reset of remote link it sends end of
initialization (M_EI_NA_1).

## Testing

See [TESTING.md](TESTING.md). The IEC 101 layers are tested bottom up:
`ft12` with known frames, every truncation and a fuzz target; `link` over
`net.Pipe` with lossy wrappers and scripted peers that send wrong or negative
answers; `iec101/e2e` runs client against server in both modes, including
link loss and recovery. Interoperability with independent implementations
runs from otfabric/iec60870-5-interop.
