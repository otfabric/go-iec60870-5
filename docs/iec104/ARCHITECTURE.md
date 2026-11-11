# Architecture: IEC 60870-5-104 (iec104 packages)

How the IEC 60870-5-104 packages are divided, why, and how a connection
works inside. The layering of the whole module, shared with the serial
companion standards, is in the root [ARCHITECTURE.md](../../ARCHITECTURE.md).

## Layers

IEC 60870-5-104 is two things in one standard: a control protocol for
running a TCP connection (APCI) and the application data it carries (ASDU).

```text
                 IEC 60870-5-104
┌──────────────────────────────────────────────┐
│  ASDU   type identification, cause of        │  package asdu
│         transmission, common address,        │  (shared with IEC 60870-5-101)
│         information objects                  │
├──────────────────────────────────────────────┤
│  A-Profile (optional) association, session  │  package sec
│         keys, Secure Data: IEC 62351-5,      │
│         IEC TS 60870-5-7                     │
├──────────────────────────────────────────────┤
│  APCI   APDU framing, I/S/U frames,          │  package iec104/apci (codec)
│         sequence numbers, acknowledgements,  │  iec104/internal/link (state machine)
│         STARTDT / STOPDT / TESTFR            │
└──────────────────────────────────────────────┘
                      TCP (optionally TLS)
```

The packages follow the reuse boundaries rather than the picture:

- **APCI stays inside the `iec104` tree.** It has no life outside
  IEC 60870-5-104. It is split into a pure codec (`iec104/apci`, exported,
  useful to analysers and test tools) and the state machine
  (`iec104/internal/link`, private, because its API is shaped by the client
  and the server and nothing else).
- **ASDU is the reusable part.** IEC 60870-5-101 carries the same ASDUs over
  a serial link layer with different field widths, and this module's
  `iec101` uses `asdu` for it. `asdu` therefore knows nothing about TCP or
  APCI and takes the widths as `asdu.Params`.
- **TCP is the standard library.** The client accepts any `Dialer`, the
  server any `net.Listener`.
- **The application-layer security sits between them.** IEC TS 60870-5-7
  wraps every application ASDU in a Secure Data ASDU and adds its own key
  management ASDUs; the APCI carries both like any other. `sec` is
  sans-IO: an `Endpoint` takes received ASDUs and the time and returns the
  ASDUs to send, so the same code could serve an IEC 60870-5-101 link
  (not wired into `iec101`).

## Packages and import rules

Import paths are relative to the module, github.com/otfabric/go-iec60870-5.

```text
iec60870 (module root)  Logger interfaces                               imports: nothing
asdu                    ASDU model and codec                            imports: cp
sec                     IEC 62351-5 A-Profile, sans-IO                  imports: asdu
internal/logx           structured or formatted log entries             imports: iec60870
iec104                  errors, State, Metrics                          imports: apci, asdu
  ├── apci              frames, sequence arithmetic, Params             imports: nothing
  ├── internal/link     connection state machine                        imports: iec104, apci, logx
  ├── client            controlling station                             imports: iec60870, iec104, apci, asdu, sec, link, logx
  └── server            controlled station                              imports: iec60870, iec104, apci, asdu, sec, link, logx
```

- `iec104/apci` imports nothing from the module and nothing outside the
  standard library; `asdu` only `cp`. They do no I/O beyond
  `apci.ReadFrame(io.Reader)`.
- `iec104/internal/link` deals in raw ASDU bytes. It never imports `asdu`
  or `sec`: the protocol machine is independent of what the I frames carry.
  It sees the security through a small interface (`link.Security`) that
  `*sec.Endpoint` satisfies.
- `sec` imports `asdu` for the field sizes and type identifications only,
  and nothing outside the standard library: the cryptography is
  `crypto/...`.
- `iec104/client` and `iec104/server` never import each other (their tests
  do, to use the other side as a peer).
- `iec104` imports `iec104/apci` and `asdu` only for the types in its
  `Metrics` and `NegativeError` signatures.

## One connection

Both stations run the same machine, `link.Link`, with a role flag. A link
owns an established `net.Conn` and three goroutines:

```text
           ┌────────┐  frames   ┌──────────────────────────┐  events  ┌────────────┐
 net.Conn ─► reader ├──────────►│ loop                     ├─────────►│ dispatcher ├─► callbacks
           └────────┘           │  owns V(S) V(R) ack      │  (queue) └────────────┘   OnASDU
                                │  unacked[], timers,      │                           OnState
 Send/StartDT/StopDT ──────────►│  STARTDT/STOPDT state    │                           OnClose
   (channels)                   │  performs every write    ├─► net.Conn
                                └──────────────────────────┘
```

- **The loop owns all protocol state** and is the only writer. There are no
  locks around sequence numbers or timers because nothing else touches them.
  `Send` and the U-frame procedures are requests on channels; the loop stops
  receiving from the send channel while `k` I frames are unacknowledged,
  which is what makes `Send` block.
- **One timer** is armed for the earliest deadline among `t1` (oldest
  unacknowledged I frame, each pending U activation), `t2` (first
  unacknowledged received I frame) and `t3` (idle).
- **The dispatcher decouples callbacks from the protocol.** A handler may
  take as long as it likes, and may call `Send`, without stalling
  acknowledgements or test frames. Events are delivered in order: an ASDU,
  a start/stop notification and the final close arrive exactly as they
  happened on the wire.
- **Receive flow control uses the protocol's own window.** When 256 events
  wait for the dispatcher the loop stops acknowledging received I frames,
  so the peer stops after `k` more. Frames are still read: the peer's
  acknowledgements keep arriving, which matters when the slow handler is
  itself blocked in `Send`. Only a peer that ignores its window (or has an
  enormous `k`) reaches the second limit, where the loop stops reading and
  TCP pushes back. Memory per connection is bounded either way.

### Security in the loop

With an `Endpoint` the loop passes every received I frame's ASDU through
`Receive`, and every ASDU to send through `Protect`, on its own goroutine.
Both return ASDUs to send (key management, Secure Data, segments), which go
into one ordered transmit queue that the loop drains as the `k` window
allows: the receiver accepts Secure Data only with increasing sequence
numbers, and the segments of one message must not be interleaved. A send
request that finds no session keys (`ErrNotReady`) is held, and the send
channel is not read, until the endpoint announces a change; a held request
whose caller gives up is cancelled without being sent. The endpoint's
deadline joins the loop's one timer.

The connections that one controlling station keeps to a station (a client
`Group`) share one `Endpoint`, and with it the association: `Attach` when
data transfer starts hands it to that connection, `Detach` takes it away. A
connection that does not carry the association drops what it receives.

A station keeps one association per controlling station. Its sessions do
not know at first whose connection they are: a session's `link.Security`
is a router that holds back what the station sends until the first
security message arrives, takes the AIM from it (`sec.AssociationOf`),
fetches or makes that station's `Endpoint` in the redundancy group, attaches
to it and from then on hands everything to it.

### Conformance decisions

| Situation | Behaviour |
|-----------|-----------|
| APDU that does not parse, including an I frame without ASDU | Close (`ErrProtocol`, wrapping the `apci` error) |
| I frame with unexpected N(S) | Close (`ErrProtocol`) |
| N(R) acknowledging frames never sent | Close (`ErrProtocol`) |
| I frame received while stopped | Close (`ErrProtocol`); edition 2 behaviour, see [INTEROPERABILITY.md](INTEROPERABILITY.md#clauses-4-and-5-protocol-structure-and-apci) |
| STOPDT act sent (client) | Received I frames are acknowledged first, and at once until the con arrives |
| I frame received between STOPDT act and con | Accepted and acknowledged immediately |
| `t1` expires on an I frame or a U activation | Close (`ErrTimeout`) |
| `w` I frames received, or `t2` since the first | S frame |
| Own I frame sent | Counts as the acknowledgement; no S frame |
| `t3` without any received frame | TESTFR act, supervised by `t1` |
| STOPDT act received (server) | Acknowledge what was received; STOPDT con once the peer has acknowledged every I frame sent to it (`t1` applies) |
| STARTDT/STOPDT act received by a client | Ignored with a warning |
| U confirmation nobody asked for | Ignored with a warning |

## Client

`client.Client` wraps at most one link and adds:

- **Connection lifecycle**: `Connect` dials (bounded by `t0`), starts the
  link and sends STARTDT. With `WithReconnect` a loop re-dials with
  exponential backoff after a loss.
- **Request matching**: IEC 60870-5-104 has no transaction identifier. An
  answer is recognised by type identification, common address, information
  object address and cause of transmission. Each request method registers an
  *exchange* (a matcher plus a queue), sends, and consumes what the matcher
  selects. Two identical requests would be indistinguishable, so the second
  fails with `ErrBusy`.
- **Delivery**: every ASDU goes to the `Handler`, whether an exchange took
  it or not. Request methods are a convenience on top of the stream, never a
  filter in front of it.
- **Retries**: every request method runs through one function that bounds
  each attempt, repeats only requests marked idempotent and only for
  transport failures, and reports to metrics and log. Commands are not
  marked, by design.

`client.Group` composes several clients into a redundancy group. A supervisor
goroutine, woken by the clients' state changes, keeps every connection
established and exactly one started. It is the protocol's own notion of
multiple connections; a pool of interchangeable connections would contradict
how a station sends its data.

## Server

`server.Server` accepts connections and wraps each in a `Session`. A session
decodes received ASDUs, applies the common address filter and calls the
`Handler` on the session's dispatcher goroutine: one session is served
serially, sessions are served concurrently. `Mux` is a `Handler` that routes
by type identification and produces the negative answers of the standard for
what is not registered.

Events take a different path from replies. `Server.Enqueue` appends to one
queue per redundancy group; a pump goroutine per group sends the oldest
pending event on the group's active session and marks it in flight. The link
reports when the peer has acknowledged that I frame, and only then is the
event removed. A session that stops or ends returns its in-flight events to
pending, in their original order. That is what makes delivery "at least
once" across a switchover without the application tracking anything.

## Testing

The test tiers, what each suite covers and how to run them are in
[TESTING.md](../../TESTING.md). Per layer: `iec104/apci` and `asdu` with
byte vectors, round trips and fuzzing; `iec104/internal/link` against a
scripted peer over loopback TCP; `sec` with octet strings from
cB-Sigma/iec60870's encoder, RFC 3394 and RFC 5869 vectors and two
endpoints on a simulated clock; `iec104/client` and `iec104/server`
against each other; `iec104/e2e` end to end; `iec104/interop` against the
reference images; `iec104/interop/cbsigma` against cB-Sigma in process.
