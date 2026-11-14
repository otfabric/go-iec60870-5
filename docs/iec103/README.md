# IEC 60870-5-103

The companion standard for the informative interface of protection
equipment: a controlling system polls protection relays over a serial line
(FT 1.2, unbalanced IEC 60870-5-2 link) and exchanges ASDUs addressed by
function type (FUN) and information number (INF).

| Package | What it is |
|---|---|
| [`iec103`](../api/iec103.md) | ASDU model and codec, information elements, FUN/INF tables, shared errors and link state |
| [`iec103/client`](../api/iec103-client.md) | the controlling system (link primary) |
| [`iec103/server`](../api/iec103-server.md) | the protection equipment (link secondary) |
| `iec103/e2e` | client against server over in-memory and lossy lines, hostile-peer and chaos tests (tests only) |
| `iec103/interop` | the reference stacks of iec60870-5-interop against this module (build tag `interop`, `make interop-iec103`) |
| `iec103/examples/quickstart` | both sides in one program; `make examples` runs it |

What is implemented, clause by clause, and how it is verified:
[INTEROPERABILITY.md](INTEROPERABILITY.md). The server is run against the
clients of four independent stacks and the client against one server whose
answers deviate from the standard (`make interop-iec103`, `iec103/interop`).

The text we hold is IEC 60870-5-103:1997 in its identical Russian adoption
GOST R IEC 60870-5-103-2005, which keeps the IEC clause numbering. Clause
numbers in code and documents refer to it. Where the text is ambiguous the
code says "our reading" or "interpretation" in the doc comment and lists the
point in INTEROPERABILITY.md.

## The ASDU model

IEC 60870-5-103 does not use the telecontrol ASDU of 101/104 (package
`asdu`): every field of the data unit identifier is one octet, and the
object is addressed by FUN and INF (clause 7.1, figure 2).

```go
a := iec103.New(iec103.CauseSpontaneous, 3, iec103.FunDistance, iec103.InfGeneralTrip,
	iec103.RelTimeTagged{DPI: iec103.DPIOn, RET: 12, FAN: 4, Time: cp.Now()})
b, err := a.Encode()        // time tags in UTC; AppendIn(b, loc) for another zone
d, err := iec103.Decode(b)  // DecodeIn(b, loc)
switch body := d.Body.(type) {
case iec103.RelTimeTagged:
	...
case iec103.Raw: // private type or private cause: every octet kept
	...
}
```

- Every type identification of tables 3 and 4 has a body type: 1
  `TimeTagged`, 2 `RelTimeTagged`, 3 and 9 `Measurands`, 4
  `RelTimeMeasurand`, 5 `Identification`, 6 `TimeSync`, 7 `GIInit`, 8
  `GITermination`, 10 `GenericData`, 11 `GenericIdent`, 20
  `GeneralCommand`, 21 `GenericCommand`, 23 `DisturbanceList`, 24 and 25
  `DisturbanceOrder`, 26 `DisturbanceReady`, 27 `ChannelReady`, 28
  `TagsReady`, 29 `Tags`, 30 `DisturbanceValues`, 31 `EndOfTransmission`.
- A private type identification (32..255), a reserved one, or a private
  cause of transmission (64..255) decodes as `Raw` and encodes back octet
  for octet. Clause 7.2 lets a private cause change the structure (type 7
  with cause above 63 for a vendor GI, for example); `DecodeTyped`
  interprets such an ASDU with the compatible structure when a station
  keeps it.
- Encode writes the VSQ the body defines (0x81, or the number of
  measurands or disturbances); decode checks it (clause 7.2.2: 0x81 for a
  single information object, SQ = 0 for ASDUs 3, 9 and 23) and keeps it in
  the VSQ field. The "not used" octets of ASDUs 23 to 31 are written as 0 and
  ignored on receive, except the INF position, which `ASDU.Inf` keeps.
- `MEA` holds MVAL as the integer in -4096..4095 with `OV` and `ER`;
  `MEAOf` and `Value` convert from and to the fraction of full scale (full
  scale is 1.2 or 2.4 times the rated value, clause 7.2.6.8).
- Time tags: the bit after the minutes is RES1 in IEC 103 (clauses
  7.2.6.28, 7.2.6.29), not the GEN flag of 101/104: ignored on decode
  (`cp.Timestamp.Substituted` stays false) and written as 0.
- Generic data: `Value` is a GDD with its GID. `ASCII`, `Uints`, `Ints`,
  `Float32s`, `Float64s`, `Reply` and `Structure` build values; `Text`,
  `Uint`, `Int`, `Float` and `Fields` read them. `PackGenericData` and
  `PackGenericIdent` split long answers over ASDUs with COUNT and CONT.
  A GDD needs DATASIZE 1..255 and NUMBER 1..127 (clause 7.2.6.32) and an
  ASDU 11 at least one description element (NDE 1..63, clause 7.2.6.35);
  encode and decode refuse others with `ErrInvalid`.
- Function types and information numbers: `FunctionType.Compatible`
  implements the ranges of clause 7.2.5.1; `StandardInfo` returns the
  entries of tables 8 to 18; a `Catalog` adds private FUN/INF semantics and
  looks up both. INF 73 (fault location) is listed with types 2 and 4
  (interpretation 7). `CommandAllowed` checks a general command against
  clause 7.2.6.4 (DCO 1 or 2) and table 17: for a compatible function type
  only its INF (16 to 19, 23 to 26; clause 7.2.5.2) and, by the COM
  column, only ON for INF 19 and 23 to 26.

## Controlling system

```go
import "github.com/otfabric/go-iec60870-5/iec103/client"

c, err := client.New(port, client.Config{LinkAddr: 1, Handler: func(a *iec103.ASDU) { ... }})
err = c.WaitUp(ctx)                          // request status, reset CU, identification
data, err := c.Interrogate(ctx)              // ASDU 7 ... ASDU 8
t, err := c.ClockSync(ctx, time.Now())       // addressed ASDU 6; BroadcastClockSync for 255
err = c.Command(ctx, iec103.FunDistance, iec103.InfLEDReset, iec103.DPIOn)
res, err := c.ReadEntry(ctx, iec103.GIN{Group: 2, Entry: 1}, iec103.KODActualValue)
res, err = c.GenericWrite(ctx, iec103.InfWriteExecute, sets...)
w, err := c.BeginWrite()                     // staged: 248 ... 250 with one RII
err = w.Stage(ctx, part1...)
res, err = w.Execute(ctx, part2...)          // or w.Abort(ctx): INF 251
rec, err := c.Disturbance(ctx, iec103.FunDistance, fan)
```

- The link: one-octet address, request status of link then reset CU
  (function 0) or, with `Config.ResetFCB`, reset FCB (function 7); class 1
  is polled while the equipment sets ACD, class 2 otherwise. The link is
  initialized again after `RetryInterval` (2 s) when the equipment stops
  answering, or sends only frames that answer something else until the
  last time-out (`link.ErrUnexpected` wrapping `link.ErrTimeout`). After a
  failed poll the client waits `PollInterval` (100 ms) before the next.
  `WaitUp` returns only after `Config.OnState` for the change to
  `StateUp` has returned. While the
  equipment sets DFC, control-direction ASDUs stay queued (clause 6.3);
  broadcasts are sent anyway. Broadcasts go through the same loop as
  every other message, between two link exchanges; `Broadcast` and
  `BroadcastClockSync` return `iec103.ErrLinkDown` when the link goes down
  before the loop sent them.
- Time synchronization (clause 7.4.2): `ClockSync` and
  `BroadcastClockSync` encode the ASDU 6 when it is transmitted, with the
  given time advanced by the time it waited for the link, so the command
  carries the time of its transmission.
- `Command` refuses a DCO or INF that `iec103.CommandAllowed` refuses with
  `ErrInvalid`.
- Generic writes (clause 7.4.8.6.3, figure 17): `GenericWrite` with INF
  250 or 251 sends every ASDU but the last with INF 248 and CONT = 1, the
  last with INF 250/251 and CONT = 0, one RII, COUNT toggling.
  `BeginWrite` returns a `StagedWrite` that builds the same sequence over
  several calls (`Stage`, then `Execute` or `Abort`); it holds the one
  generic write of clause 7.4.8.4 until it ends.
- Disturbance data (clause 7.4.7, figure 10): every ASDU 31 is
  acknowledged with an ASDU 25. Tags or a channel the equipment aborted
  (TOO 40, 37) are acknowledged negatively (TOO 69, 67) and the transfer
  goes on; the end of the disturbance data is then acknowledged negatively
  (TOO 65) and the record returned with `ErrNegative`. The tags are also
  acknowledged negatively when the initial states are not at position 0,
  the end when fewer channels came than NOC announced. An abortion of the
  whole transfer (TOO 34) is acknowledged with TOO 65 and returns
  `ErrAborted`. The record holds at most the NOC channels of NOE values
  the ASDU 26 announced (clauses 7.2.6.10, 7.2.6.11): a value or a
  channel beyond them ends the transfer at once with `ErrInvalid`, and no
  further order is sent (`TestDisturbanceWithinAnnounced`).
- After every initialization the client synchronizes the time and runs a
  general interrogation (clause 7.4.1, figure 3) unless `NoInitTimeSync` /
  `NoInitGI` are set. The identification (ASDU 5) is kept and available
  through `Identification`.
- Every received ASDU reaches `Handler`, the answers to requests included.
  A request, `Send`, `Broadcast` or `BroadcastClockSync` called from
  `Handler` or `OnState` on one of the client's goroutines returns
  `iec103.ErrInHandler` at once and is not sent; both may call `Close`,
  which then returns at once. A callback that does not return normally is
  logged with its stack and stops the client; `Err` reports it.
- One request of each kind at a time (`ErrBusy`), as clauses 7.4.4, 7.4.7
  and 7.4.8.4 require. `RequestTimeout` (10 s) applies to a context without
  deadline; give a disturbance transfer a longer one. A request returns at
  its deadline even while the link layer is still repeating its frame. A
  request collects at most `Config.MaxResult` ASDUs (65536 by default),
  then ends with `iec103.ErrResultLimit` and the answers so far; one that
  missed answers returns `iec103.ErrOverflow`. A request in flight at
  Close, and every request after it, returns `iec103.ErrClosed`.

## Protection equipment

```go
import "github.com/otfabric/go-iec60870-5/iec103/server"

s, err := server.New(port, server.Config{
	LinkAddr: 1, Fun: iec103.FunDistance, // Fun is required
	Identification: iec103.IdentificationOf(iec103.COLWithGeneric, "VENDOR", [4]byte{}),
	Handler: server.HandlerFunc(func(s *server.Server, a *iec103.ASDU) { ... }),
	Disturbances: recorder, // optional DisturbanceSource
})
s.Event(iec103.CauseSpontaneous, iec103.FunDistance, iec103.InfGeneralTrip, iec103.DPIOn)
s.Send(measurands, server.Class2)
```

- The server answers the link itself: functions 0, 3, 4, 7, 9, 10 and 11;
  any other is answered with function 15 (clauses 6.2.3 and 6.3). DFC is
  set while the queue of received ASDUs is more than half full; user data
  that arrives while DFC is set is not processed and is answered NACK
  (clause 6.3), a broadcast is taken while there is room.
- Reset CU clears both queues; reset CU and reset FCB queue the
  identification (cause 4, INF 3, or cause 3, INF 2) ahead of everything
  else; after the first reset since start the identification with cause 6
  (power on) or 5 (`StartCause`) follows (clause 7.4.1, table 8).
- Time synchronization is handled by the server: it keeps the offset,
  answers an addressed synchronization with ASDU 6, and `Now` returns time
  tags with IV set until the first synchronization and after `SyncTimeout`
  (23 h) without one (clause 7.4.2). The received time is corrected by
  `SyncDelay`, by default the transmission time of the ASDU 6 frame (21
  characters of 11 bits) at `BitRate` (default 9600 bit/s, clause 6.2.4);
  a negative `SyncDelay` turns the correction off. A broadcast
  synchronization is answered only with `AnswerBroadcastSync` (figure 5:
  broadcast messages need no answer). A synchronization whose time is flagged
  invalid, or lies outside the years 1970 to 2069 that CP56Time2a carries,
  is not applied: the clock stays as it was, and an addressed one is
  answered with ASDU 6 carrying the station's time with IV set
  ([interpretation 13](INTEROPERABILITY.md#interpretations)).
- `AnswerGI` needs the general interrogation (ASDU 7) and `AckCommand` the
  general command (ASDU 20) the handler received; for any other ASDU they
  return `iec103.ErrInvalid` and send nothing.
- The handler answers with `AnswerGI` (cause 9, SCN as SIN, ASDU 8),
  `AckCommand` (ASDU 1, cause 20/21, RII as SIN), `GenericAnswer`,
  `AnswerGenericGI` and `GenericIdentAnswer`. A new ASDU 7 (or ASDU 21 with
  INF 245) drops the queued rest of the previous cycle, its termination
  included (clauses 7.4.3 and 7.4.8.7). A command received before the
  previous one was acknowledged is refused by the server (clause 7.4.4),
  and so is one with a DCO or INF that `iec103.CommandAllowed` refuses.
- With a `DisturbanceSource` the server runs the disturbance data transfer
  of clause 7.4.7 itself, including the list (ASDU 23 with the function
  type of the equipment, cause 31) after a GI and after a completed or
  aborted transfer. An implausible order ends the transfer in progress
  with TOO 34, then the list follows. The selection of a record with more
  than 255 channels or more than 65535 values per channel, which NOC and
  NOE cannot announce, is answered with TOO 34 and no transfer starts
  (`TestDisturbanceTooLarge`).

## Changes between releases

API and behaviour changes, including those of the v0.2.0 audit against
IEC 60870-5-103:1997, are listed per release in
[RELEASE.md](../../RELEASE.md).

## Not implemented

See [INTEROPERABILITY.md](INTEROPERABILITY.md#not-implemented): one
primary polling several items of equipment on a shared line, the test mode
and the monitor-direction blocking as server features (the application
sends the causes and indications, see `TestTestModeAndBlocking`),
splitting one generic data element over several ASDUs, and abortion of a
disturbance transfer by the client.
