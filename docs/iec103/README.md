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
| `iec103/e2e` | client against server over in-memory and lossy lines (tests only) |
| `iec103/examples/quickstart` | both sides in one program; `make examples` runs it |

What is implemented, clause by clause, and how it is verified:
[INTEROPERABILITY.md](INTEROPERABILITY.md). There is no interoperability
evidence against an independent implementation yet.

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
  measurands or disturbances); the VSQ field of a decoded ASDU is what was
  received. The "not used" octets of ASDUs 23 to 31 are written as 0 and
  ignored on receive, except the INF position, which `ASDU.Inf` keeps.
- `MEA` holds MVAL as the integer in -4096..4095 with `OV` and `ER`;
  `MEAOf` and `Value` convert from and to the fraction of full scale.
- Generic data: `Value` is a GDD with its GID. `ASCII`, `Uints`, `Ints`,
  `Float32s`, `Float64s`, `Reply` and `Structure` build values; `Text`,
  `Uint`, `Int`, `Float` and `Fields` read them. `PackGenericData` and
  `PackGenericIdent` split long answers over ASDUs with COUNT and CONT.
- Function types and information numbers: `FunctionType.Compatible`
  implements the ranges of clause 7.2.5.1; `StandardInfo` returns the
  entries of tables 8 to 18; a `Catalog` adds private FUN/INF semantics and
  looks up both.

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
rec, err := c.Disturbance(ctx, iec103.FunDistance, fan)
```

- The link: one-octet address, request status of link then reset CU
  (function 0) or, with `Config.ResetFCB`, reset FCB (function 7); class 1
  is polled while the equipment sets ACD, class 2 otherwise. While the
  equipment sets DFC, control-direction ASDUs stay queued (clause 6.3);
  broadcasts are sent anyway.
- After every initialization the client synchronizes the time and runs a
  general interrogation (clause 7.4.1, figure 3) unless `NoInitTimeSync` /
  `NoInitGI` are set. The identification (ASDU 5) is kept and available
  through `Identification`.
- Every received ASDU reaches `Handler`, the answers to requests included.
- One request of each kind at a time (`ErrBusy`), as clauses 7.4.4, 7.4.7
  and 7.4.8.4 require. `RequestTimeout` (10 s) applies to a context without
  deadline; give a disturbance transfer a longer one.

## Protection equipment

```go
import "github.com/otfabric/go-iec60870-5/iec103/server"

s, err := server.New(port, server.Config{
	LinkAddr: 1, Fun: iec103.FunDistance,
	Identification: iec103.IdentificationOf(iec103.COLWithGeneric, "VENDOR", [4]byte{}),
	Handler: server.HandlerFunc(func(s *server.Server, a *iec103.ASDU) { ... }),
	Disturbances: recorder, // optional DisturbanceSource
})
s.Event(iec103.CauseSpontaneous, iec103.FunDistance, iec103.InfGeneralTrip, iec103.DPIOn)
s.Send(measurands, server.Class2)
```

- The server answers the link itself: functions 0, 3, 4, 7, 9, 10 and 11;
  any other is answered with function 15 (clauses 6.2.3 and 6.3). DFC is
  set while the queue of received ASDUs is more than half full, and a
  message it cannot take is answered NACK.
- Reset CU clears both queues; reset CU and reset FCB queue the
  identification (cause 4, INF 3, or cause 3, INF 2) ahead of everything
  else; after the first reset since start the identification with cause 6
  (power on) or 5 (`StartCause`) follows (clause 7.4.1, table 8).
- Time synchronization is handled by the server: it keeps the offset,
  answers with ASDU 6, and `Now` returns time tags with IV set until the
  first synchronization and after `SyncTimeout` (23 h) without one (clause
  7.4.2).
- The handler answers with `AnswerGI` (cause 9, SCN as SIN, ASDU 8),
  `AckCommand` (ASDU 1, cause 20/21, RII as SIN), `GenericAnswer`,
  `AnswerGenericGI` and `GenericIdentAnswer`. A command received before
  the previous one was acknowledged is refused by the server (clause
  7.4.4).
- With a `DisturbanceSource` the server runs the disturbance data transfer
  of clause 7.4.7 itself, including the list after a GI and after a
  transfer.

## Not implemented

See [INTEROPERABILITY.md](INTEROPERABILITY.md#not-implemented): one
primary polling several items of equipment on a shared line, the test mode
and the monitor-direction blocking as server features, splitting one
generic data element over several ASDUs, and abortion of a disturbance
transfer by the client.
