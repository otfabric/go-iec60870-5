# Errors: otfabric/go-iec60870-5

How go-iec60870-5 reports failures. For signatures see [API.md](API.md) and
[docs/api](docs/api/README.md).

The IEC 60870-5-104 packages (`iec104`, `iec104/client`, `iec104/server`,
`sec`, `asdu`) keep the error model of go-iec104 v0.3.0, described in the
sections below up to [Detection patterns](#detection-patterns). The serial
companion standards add their own layers, described in
[Serial companion standards](#serial-companion-standards).

## Table of contents

- [Quick reference](#quick-reference)
- [Sentinels](#sentinels)
- [Negative answers (`NegativeError`)](#negative-answers-negativeerror)
- [Connection-fatal errors](#connection-fatal-errors)
- [Codec errors](#codec-errors)
- [Application-layer security errors](#application-layer-security-errors)
- [File transfer errors](#file-transfer-errors)
- [Result bounds](#result-bounds)
- [Context and timeouts](#context-and-timeouts)
- [Retries](#retries)
- [What is not an error](#what-is-not-an-error)
- [Detection patterns](#detection-patterns)
- [Serial companion standards](#serial-companion-standards)
- [IEC 60870-5-103](#iec-60870-5-103)
- [IEC 60870-5-102](#iec-60870-5-102)

## Quick reference

| Situation | Error | Connection |
|-----------|-------|------------|
| Station answers with P/N = 1 or cause 44..47 | `*iec104.NegativeError` | stays up |
| Answer does not arrive before the deadline | wraps `context.DeadlineExceeded` | stays up |
| Request while the same one is pending | `iec104.ErrBusy` | stays up |
| `Interrogate`, `CounterInterrogate` (`asdu.FreezeRead`) or `ListFiles` answered with more than `WithMaxResult` ASDUs or entries | wraps `telecontrol.ErrResultLimit`, with the results so far | stays up |
| `GetFile` from a station that announces more than 255 sections | `iec104.ErrProtocol` | stays up |
| Request method called from the client's `Handler` or state handler | `iec104.ErrInHandler` | stays up |
| `Connect`/`Dial` could not establish the connection | `iec104.ErrConnectFailed`, wrapping the cause | - |
| Client has no connection | `iec104.ErrNotConnected` | - |
| Data transfer stopped (before STARTDT, after STOPDT) | `iec104.ErrNotStarted` | stays up |
| Operation after `Close` | `iec104.ErrClosed` | - |
| Peer closed, write or read failed | `iec104.ErrConnectionLost` | closed |
| I frame not acknowledged or U frame not confirmed within `t1` | `iec104.ErrTimeout` | closed |
| Malformed APDU, sequence error, I frame in STOPDT | `iec104.ErrProtocol` | closed |
| With IEC 62351-5: more than 1 MiB of security output waits for the peer's acknowledgements | `iec104.ErrProtocol` | closed |
| ASDU cannot be encoded | `asdu.Err…` | stays up |
| Invalid `apci.Params` / `asdu.Params` at construction | `apci.ErrInvalidParams` / `asdu.ErrInvalidParams` | - |
| Any other invalid option at construction | `iec104.ErrInvalidOption` | - |

All sentinels are wrapped with detail. Always test with `errors.Is` and
`errors.As`, never with `==`.

## Sentinels

Defined in package `iec104` and shared by `iec104/client` and `iec104/server`:

| Sentinel | Meaning |
|----------|---------|
| `ErrClosed` | The client, server or session was closed by the application |
| `ErrNotConnected` | The client has no established connection (never connected, or reconnecting) |
| `ErrConnectFailed` | `Connect` or `Dial` failed: TCP dial (also a `Dialer` that returns neither a connection nor an error), TLS handshake or STARTDT. The cause is wrapped too, so `errors.Is(err, context.DeadlineExceeded)` and `errors.As(err, &netErr)` keep working |
| `ErrNotStarted` | An ASDU was to be sent while data transfer is stopped |
| `ErrConnectionLost` | The peer closed the connection or the transport failed |
| `ErrTimeout` | `t1` expired; the connection was closed |
| `ErrProtocol` | The peer violated the protocol; the connection was closed |
| `ErrBusy` | An identical request, or a conflicting STARTDT/STOPDT, is in progress |
| `ErrInHandler` | A request method was called from inside the client's `Handler` or state handler, where its answer cannot be delivered |
| `ErrInvalidOption` | An option value a client or server cannot be constructed with |

`server.ErrServerClosed` is what `Serve` and `ListenAndServe` return after
`Server.Close`. It is the normal way for them to end.

## Negative answers (`NegativeError`)

A controlled station refuses a request by mirroring it. The client request
methods turn both forms of refusal into `*iec104.NegativeError`:

| The station sends | `Cause()` | Typical reason |
|-------------------|-----------|----------------|
| Activation confirmation with P/N = 1 | `asdu.CauseActivationCon` | Interlocked, execute without select, value out of range |
| Deactivation confirmation with P/N = 1 | `asdu.CauseDeactivationCon` | Nothing to deactivate |
| Cause 44 | `asdu.CauseUnknownType` | Type identification not supported |
| Cause 45 | `asdu.CauseUnknownCause` | Cause of transmission not valid for the type |
| Cause 46 | `asdu.CauseUnknownCommonAddr` | No such station |
| Cause 47 | `asdu.CauseUnknownIOA` | No such information object |

`NegativeError.ASDU` is the station's answer in full. `Interrogate` and
`CounterInterrogate` with `asdu.FreezeRead` return the ASDUs collected so
far together with the error. `CounterInterrogate` with a freeze or reset
qualifier returns at the confirmation and always with nil ASDUs.

## Result bounds

`client.WithMaxResult` (default `client.DefaultMaxResult`, 65536; a
negative value is no bound) bounds what one collecting request returns:
the ASDUs of `Interrogate` and of `CounterInterrogate` with
`asdu.FreezeRead`, the directory entries of `ListFiles`. The answer that
would exceed it ends the request with the results so far and an error
wrapping `telecontrol.ErrResultLimit`; such a request is not retried. A
request that waits for one answer (a command, `Read`, `ClockSync`, ...)
queues at most 16 mirrors ahead of its answer and otherwise ends with an
error wrapping `telecontrol.ErrResultLimit` too. `GetFile` ends with
`iec104.ErrProtocol` when the station announces more than 255 sections,
the most a file can have (one per name of section, IEC 60870-5-101
7.2.6.33).

## Connection-fatal errors

`ErrConnectionLost`, `ErrTimeout` and `ErrProtocol` end the connection. They
surface in three places:

- as the return value of a call that was in flight (`Send`, `StartDT`, a
  pending request)
- in the state handler: `client.WithStateHandler` receives
  `StateDisconnected` (or `StateConnecting` with `WithReconnect`) and the
  error; `server.WithStateHandler` receives `StateDisconnected` and the error
- from `Session.Err` on the server

With `client.WithReconnect` the client re-dials by itself; requests made in
the meantime fail with `ErrNotConnected`. A failed attempt is reported to the
state handler as `StateConnecting` with the dial error.

On the server, a session closed by `Session.Close` or `Server.Close` ends
with a nil error.

## Codec errors

Returned by `asdu.Encode`/`Decode` and by every method that encodes an ASDU
(`Client.Send`, `Client.Command`, `Session.Send`, `Server.Broadcast`):

| Sentinel | Meaning |
|----------|---------|
| `asdu.ErrInvalidValue` | A field does not fit: qualifier out of range, address wider than the field, more than 127 objects; also a nil ASDU or command |
| `asdu.ErrTypeMismatch` | An object does not belong to the ASDU's type, or is not a command where one is required |
| `asdu.ErrUnsupportedType` | Typed objects under a type identification without an object model |
| `asdu.ErrNotSequential` | `Sequence` is set and the addresses are not consecutive |
| `asdu.ErrTooLong` | The encoded ASDU exceeds 249 octets (`Params.MaxSize`) |
| `asdu.ErrMalformed` | Decode: truncated header or payload size not matching the object count |
| `asdu.ErrInvalidParams` | The `Params` field sizes are not allowed |

`apci` has the framing counterparts: `ErrInvalidStart`, `ErrInvalidLength`
(also an I frame without ASDU, on receipt and on send),
`ErrInvalidControl`, `ErrASDUTooLong`. Received over a connection they are
wrapped in `iec104.ErrProtocol` and close it.

An encode error never touches the connection: nothing was sent.

## Application-layer security errors

Package `sec` (IEC 62351-5) has its own sentinels. They are usually wrapped with detail.

| Sentinel | Meaning |
|----------|---------|
| `sec.ErrMessage` | A security message cannot be encoded or decoded: a field out of range, a wrong length, a type or cause not permitted in its direction |
| `sec.ErrNotReady` | Application data cannot be protected yet: no session keys, or the controlling station holds its data back while it changes them. Wait for `Endpoint.Changed` and try again |
| `sec.ErrUnsecured` | An ordinary ASDU arrived on a connection the A-Profile applies to. It is discarded |
| `sec.ErrAuthentication` | A MAC or AEAD tag does not verify, or wrapped keys do not unwrap. The message is discarded |
| `sec.ErrReplay` | Secure Data with a sequence number not above the last one accepted. It is discarded |
| `sec.ErrUnexpected` | A security message the state machine does not expect now, or with association IDs that do not match. It is discarded |
| `sec.ErrCertificate` | A remote certificate that is invalid (wrong curve, outside its validity period, bad signature) or not authorized by `Trust`, or a local one that cannot be used |
| `sec.ErrAlgorithm` | The remote station selected an algorithm this station does not support or accept |
| `sec.ErrProcedure` | A Station Association or Session Key Change failed: the remote station repeatedly did not answer within the expected reply time |
| `sec.ErrConfig` | A `Config` an `Endpoint` cannot run with |
| `sec.ErrNotPermitted` | `Endpoint.Protect` at a controlled station refused monitor-direction data (types 1 to 44) because the roles of the controlling station lack MONITOR DATA (IEC TS 60870-5-7:2025 8.2). `Session.Send` returns it; `Server.Broadcast` skips the session, `Server.Enqueue` drops the event for it |

## File transfer errors

`server.ErrFileNotAcknowledged` is reported to `FileServer.OnDone` when the controlling station answers a section or the file with a negative acknowledgement, or abandons the transfer.

## Context and timeouts

- **Connect** is bounded by the context and by `t0`, whichever ends first.
  Its error always matches `ErrConnectFailed` and, when a deadline was the
  reason, `context.DeadlineExceeded` as well.
- **Request methods** (`Command`, `Interrogate`, `Read`, …) wait for the
  station's answer. A context without deadline is bounded by
  `WithRequestTimeout` (default 10s); a context with a deadline is used as
  is. On expiry the error wraps `context.DeadlineExceeded`, the request slot
  is released and the connection stays up: IEC 60870-5-104 has no way to
  cancel a request, so a late answer still reaches the `Handler`.
- **Send** blocks only while the send window is full (`k` unacknowledged I
  frames). If the peer never acknowledges, `t1` closes the connection and
  `Send` returns that error.
- **StartDT/StopDT/TestLink** wait for the confirmation. If the context ends
  first the call returns, but the U frame stays outstanding and `t1` still
  applies to it.

## Retries

Off by default. `client.WithRetry` repeats a request when all of these hold:

1. **The request only reads**: `Interrogate`, `CounterInterrogate` with
   `asdu.FreezeRead`, `Read`, `TestCommand`, `ListFiles`.
2. **The failure is transient**: the connection is not there
   (`ErrNotConnected`, `ErrNotStarted`), was lost during the attempt
   (`ErrConnectionLost`, `ErrTimeout`, `ErrProtocol`), or the station did not
   answer within the request timeout.
3. **The caller still wants it**: its context has not ended and the client is
   not closed.

A `*NegativeError` is never retried: the station answered. `ErrBusy` is not
retried either.

`Command`, `Deactivate`, `ClockSync`, `ResetProcess`, a counter freeze or
reset, `GetFile`, `PutFile` and `Send` are **never** repeated. If the confirmation of a command is
lost, the command may have been executed; IEC 60870-5-104 offers no way to
find out, and sending it again could operate the process twice. Decide in the
application, typically by reading the state back.

With retries on, the request timeout applies to every attempt even when the
context has a deadline; the context bounds the call as a whole. After the
last attempt the error of that attempt is returned. Retries are reported
through `RequestMetrics.OnRetry` and a warning in the log.

A `Group` answers `ErrNotConnected` while it has no started connection;
`ErrInvalidOption` when created without addresses.

## What is not an error

- **An unknown type identification** on receive decodes into `ASDU.Raw`.
- **A received ASDU that does not decode** is dropped and reported through
  `Metrics.OnDecodeError` and a warning; the connection stays up. The peer is
  not answered, because a mirror cannot be built from bytes that do not parse.
- **An invalid time tag** (a field out of range or a date that does not
  exist) decodes to the zero time with `Timestamp.Invalid` set.
- **Reserved bits** in quality descriptors and qualifiers are ignored on
  receive and sent as zero.
- **Unsolicited U-frame confirmations** are ignored.

## Detection patterns

```go
err := c.Command(ctx, 1, asdu.SingleCommand{IOA: 6001, Value: true})

var neg *iec104.NegativeError
switch {
case err == nil:
	// confirmed
case errors.As(err, &neg):
	if neg.Cause() == asdu.CauseUnknownIOA {
		// no such object
	}
case errors.Is(err, context.DeadlineExceeded):
	// the station did not answer; the connection is still up
case errors.Is(err, iec104.ErrNotConnected), errors.Is(err, iec104.ErrConnectionLost):
	// no connection: retry after the state handler reports StateStarted
case errors.Is(err, asdu.ErrInvalidValue):
	// programming error: the command cannot be encoded
}
```

## Serial companion standards

IEC 60870-5-101 (and the -102/-103 stations built on the same layers)
report failures layer by layer. Every sentinel is wrapped with detail: test
with `errors.Is`.

### Time tags (`cp`)

| Sentinel | Meaning |
|---|---|
| `cp.ErrShort` | the buffer is shorter than the time tag. Out-of-range fields are not errors: they decode with `Invalid` set. |

### FT 1.2 frames (`ft12`)

| Sentinel | Meaning |
|---|---|
| `ft12.ErrInvalidParams` | link address size other than 0, 1 or 2 |
| `ft12.ErrMalformed` | wrong start or stop character, the two length octets differ, a length below control and address size |
| `ft12.ErrChecksum` | the checksum does not match |
| `ft12.ErrTooLong` | user data does not fit one frame (L at most 255) |
| `ft12.ErrInvalidAddress` | link address wider than the address field |
| `ft12.ErrShort` | the buffer ends before the frame: more octets may complete it |

`ft12.Decoder` never returns `ErrMalformed` or `ErrChecksum`: it skips the
octets and resynchronizes, reporting them to `OnDiscard`. With `Gap` set,
the octets of a frame whose next octet comes later than `Gap` are skipped
the same way and reported with `ErrShort` (the stations of `link` set it
from `link.Config.FrameGap`). It returns the reader's error, or
`io.ErrUnexpectedEOF` when the stream ends inside a frame.

### Link procedures (`link`)

| Sentinel | Meaning | Link |
|---|---|---|
| `link.ErrTimeout` | no response after `Config.Retries` repetitions | the caller re-initializes it |
| `link.ErrNack` | the secondary answered NACK (function 1): message not accepted | stays up |
| `link.ErrDataFlow` | DFC is set: the secondary cannot take more user data now, and kept it set while `Init` or a balanced `Send` requested the status of link `Config.Retries` + 1 times | stays up |
| `link.ErrNotFunctioning` | function 14: link service not functioning | stays up |
| `link.ErrNotImplemented` | function 15: link service not implemented | stays up |
| `link.ErrUnexpected` | a response that does not answer the request (its function code cannot answer the function sent). When only such responses came until the last time-out the error also wraps `link.ErrTimeout`: the message went unanswered | stays up; with `link.ErrTimeout` the caller re-initializes it |
| `link.ErrNoData` | `SendConfirm`, `SendNoReply` or a balanced `Send` with empty user data: without an ASDU a frame is fixed-length (IEC 60870-5-101:2003 6.1) | stays up |
| `link.ErrClosed` | the station was closed, or the transport failed (wrapped with the transport error), or a callback (a handler, `OnFrame`, `OnDiscard`) did not return normally: `Err` then wraps it with the callback's name | gone |
| `link.ErrInvalidConfig` | a `Config` a station cannot be built with | - |

### IEC 60870-5-101 stations (`iec101`, `iec101/client`, `iec101/server`)

| Sentinel | Meaning |
|---|---|
| `iec101.ErrLinkDown` | a request while the link is not initialized; outstanding requests fail with it (wrapping the link error) when the link is lost |
| `iec101.ErrClosed` | an operation on a closed client or server: a request in flight at Close and every request after it, in both modes; `Err` wraps it when a Handler or OnState did not return normally |
| `iec101.ErrInHandler` | a client request that waits for an answer, called from `Config.Handler` or `Config.OnState` on one of the client's goroutines (and `Send` or `SetDelay` from OnState there): it is not sent |
| `iec101.ErrInvalidConfig` | invalid parameters (wraps the `ft12`, `asdu` or `link` error) |
| `server.ErrQueueFull` | a class 1 or class 2 queue (or the balanced send queue) is full |
| `iec101.ErrProfile` | with `Params.Strict`: an ASDU the IEC 60870-5-101 profile does not allow (`*iec101.ProfileError` says why); it was not sent |

Requests of `iec101/client` return `*telecontrol.NegativeError` for a
negative confirmation or an "unknown ..." cause, `telecontrol.ErrBusy` for a
request whose answers could not be told apart from a pending identical one,
an error wrapping `telecontrol.ErrResultLimit` for an interrogation whose
answer has more ASDUs than `Config.MaxResult` (with the ASDUs so far), and wrap `context.DeadlineExceeded` when no answer arrives within
`Config.RequestTimeout`. A request ends at its deadline also while the link
layer is still repeating its user data; the exchange itself runs to its
end, so the station may still receive it.

A `link` exchange (`Primary`, `Balanced`) returns its context's error also
while its frame waits for room in a full send queue, which a peer that
stops reading leaves full.

## IEC 60870-5-103

### Codec (`iec103`)

| Sentinel | Meaning |
|---|---|
| `iec103.ErrShort` | the ASDU ends before its last field, or a GID is shorter than its GDD announces |
| `iec103.ErrTrailing` | octets after the information object of a compatible type identification |
| `iec103.ErrInvalid` | a field out of range: more elements than the ASDU may carry (ASDU 3: 4, ASDU 9: 9, ASDU 23: 8, ASDU 29 and 30: 25, NGD/NDE: 63), GID length different from its GDD, a GDD with DATASIZE 0 or NUMBER outside 1..127 (7.2.6.32), an ASDU 11 with NDE 0 (7.2.6.35), a VSQ other than 81H for a single information object or with SQ = 1 for ASDUs 3, 9 and 23 (7.2.2), an accessor of `iec103.Value` used on the wrong data type; also a client request with an information number that is not a generic read or write, or a general command with a DCO that `iec103.CommandAllowed` refuses; also a disturbance transfer with a value or a channel beyond the NOC channels of NOE values its ASDU 26 announced, or a channel (ASDU 27) offered for another fault (FAN) than the one selected (`Disturbance` returns the record so far), and `AnswerGI` / `AckCommand` of `iec103/server` with an ASDU that is not a general interrogation (ASDU 7) / general command (ASDU 20) |
| `iec103.ErrTooLong` | an ASDU longer than 253 octets (one FT 1.2 frame with a one-octet link address), or a data set or description element too large for one ASDU |

A type identification outside tables 3 and 4, or a private cause of
transmission, is not an error: it decodes as `iec103.Raw`.

### Stations (`iec103/client`, `iec103/server`)

| Sentinel | Meaning |
|---|---|
| `iec103.ErrLinkDown` | a request while the link is not initialized; a pending request fails with it when the link is lost, and so does a broadcast (`Broadcast`, `BroadcastClockSync`) the link loop had not taken when the link went down |
| `iec103.ErrClosed` | an operation on a closed client or server, a request in flight at Close, or an operation on a `client.StagedWrite` that was already executed or aborted; `Err` wraps it when a callback did not return normally |
| `iec103.ErrInHandler` | a client request (also `Send`, `Broadcast`, `BroadcastClockSync`) called from `Config.Handler` or `Config.OnState` on one of the client's goroutines: it is not sent |
| `iec103.ErrResultLimit` | a request that collects more ASDUs than `Config.MaxResult` (`Interrogate`, the generic services, the tags of a disturbance transfer); also a disturbance channel answered with more than `Config.MaxResult` ASDUs 30 that add nothing to the record (no values, or another FAN). The answers so far are returned with it |
| `iec103.ErrOverflow` | a request missed answers: they arrived faster than it took them, beyond the 1024 ASDUs a request buffers |
| `iec103.ErrInvalidConfig` | invalid configuration: link address 255, a server without function type (`Config.Fun` 0), a negative `Config.BitRate`, a start cause other than 5 or 6, an invalid `link.Config` |
| `iec103.ErrBusy` | a request while another of the same kind is pending (command, GI, generic read, generic write, generic GI, disturbance transfer) |
| `iec103.ErrNegative` | negative acknowledgement of a command (cause 21), of a generic write (cause 41; the result is returned too), or a disturbance transfer the client acknowledged negatively: tags or a channel aborted by the equipment (TOO 40, 37), incomplete, initial tag states not at position 0, fewer channels than NOC (the record is returned too) |
| `iec103.ErrAborted` | the protection equipment ended a disturbance transfer with TOO 34 (acknowledged with TOO 65 first) |
| `server.ErrQueueFull` (`iec103/server`) | a class 1 or class 2 queue is full |

Requests wrap `context.DeadlineExceeded` when no answer arrives within the
context's deadline or `Config.RequestTimeout`, also while the link layer is
still repeating their user data (the exchange runs to its end). A generic
read answered with cause 43 is not an error: `GenericResult.Cause` reports it and the data sets
carry the generic reply codes. Link errors (`link.ErrNack` while the
equipment holds back commands with DFC, `link.ErrTimeout`) are returned
wrapped.

## IEC 60870-5-102

### Codec (`iec102`, `iec102/ree`)

| Sentinel | Meaning |
|---|---|
| `iec102.ErrShort` | the ASDU ends before its last field, or a time a / time b has fewer than 5 / 7 octets |
| `iec102.ErrTrailing` | octets after the information objects of a type the codec interprets |
| `iec102.ErrInvalid` | a field out of range: a common address size other than 1 or 2, a length that fits no layout of the type (integrated totals with or without signature, REE tariff records of 63 octets), more than 127 objects, a counter reading wider than its type, a body whose type it cannot encode; also a client helper answered by an ASDU of an unexpected type |
| `iec102.ErrTooLong` | an ASDU longer than 253 octets, or longer than the server's link carries |

A type identification without a known layout (outside the standard range
without a profile, M_EI_NA_2, a list with SQ set) is not an error: it
decodes as `iec102.Raw`. Neither is a refusal whose objects do not fit its
type.

### Stations (`iec102/client`, `iec102/server`)

| Sentinel | Meaning |
|---|---|
| `iec102.ErrLinkDown` | the link could not be initialized, or the station stopped answering during a request (wraps `link.ErrTimeout` or `link.ErrClosed`) |
| `iec102.ErrClosed` | an operation on a closed client or server, or a request in progress when the client was closed; `Err` wraps it when a callback did not return normally |
| `iec102.ErrResultLimit` | a request whose answer has more ASDUs than the client's `Config.MaxResult`; the ASDUs so far are returned with it |
| `iec102.ErrInvalidConfig` | invalid configuration: a link address size other than 1, 2 or `iec102.NoLinkAddr`, a link address that does not fit it, invalid codec parameters or `link.Config` |
| `iec102.ErrNegative` | the station refused the request: it mirrored it with the P/N bit or a cause 13..18; the error is an `*iec102.RefusedError` with the refusal ASDU and its cause |
| `server.ErrQueueFull` (`iec102/server`) | the class 2 queue is full |

Requests wrap `context.DeadlineExceeded` when the answer is not complete
within the context's deadline or `Config.RequestTimeout`. A NACK of the
request (the station's input is full) is not returned: the client tries
again after `PollInterval` until the deadline.

An ASDU that matches the request's type, addresses and record but not its
information object is not taken as its answer. A request after one that
ended before its answer was complete first polls the station until it has
no more data; an error of that polling is the request's (wrapping
`iec102.ErrLinkDown`, `iec102.ErrClosed` or the context's error).
