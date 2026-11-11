# IEC 60870-5-102

The companion standard for the transmission of integrated totals (energy
meter readings) in electric power systems: a controlling station reads
integration periods of counters from a meter or data concentrator over a
serial line or modem (FT 1.2, unbalanced IEC 60870-5-2 link).

| Package | What it is |
|---|---|
| [`iec102`](../api/iec102.md) | ASDU model and codec, time a and time b, shared errors and link state |
| [`iec102/ree`](../api/iec102-ree.md) | the Spanish REE profile: its type identifications, quality bits, session helpers; applied only when asked for |
| [`iec102/client`](../api/iec102-client.md) | the controlling station (link primary) |
| [`iec102/server`](../api/iec102-server.md) | the integrated total station (link secondary) with a store of records |
| `iec102/e2e` | client against server over an in-memory line (tests only) |
| `iec102/examples/quickstart` | both sides in one program; `make examples` runs it |

**We do not hold the text of IEC 60870-5-102.** Every layout and procedure
is traced to the Beckhoff TS650x manual (vendor documentation of a
commercial IEC 102 master), to recorded meter traffic and to independent
implementations read for comparison. [INTEROPERABILITY.md](INTEROPERABILITY.md)
classifies every type identification, cause, field and procedure as
standard, REE profile, vendor-specific or unresolved, with its source and
confidence, and lists where the sources disagree. Independent evidence:
the controlling station of gisce/iec870ree (REE profile) against
`iec102/server` (`make interop-iec102`); the client has no independent
peer.

## The ASDU model

IEC 60870-5-102 does not use the telecontrol ASDU of 101/104 (package
`asdu`). After the data unit identifier comes a record address, then the
information objects; lists of integrated totals end with one common time
tag:

```text
type | VSQ | cause | common address (1 or 2) | record address | objects ... | [time a]
```

`iec102.Decode` returns an `*iec102.ASDU` whose `Body` is one of
`IntegratedTotals` (types 2..13), `SinglePoints` (1), `ProductSpec` (71),
`SystemTime` (72), `Read` (100..123), or `Raw` for everything else. A raw
body keeps every octet, and every body this package decodes encodes back to
the same octets: the 749 frames of the recorded meter traffic do, and the
fuzz target checks it for arbitrary input.

```go
a, err := iec102.Decode(frame.Data, iec102.Params{})
if b, ok := a.Body.(iec102.IntegratedTotals); ok {
	for _, t := range b.Totals {
		fmt.Println(t.Addr, t.Value, t.Quality.IV(), b.Time)
	}
}
```

`iec102.Params` sets the common address size (default two octets, as in
all sources), the time zone of conversions and an optional `Profile`.

The time formats are 102's own, not IEC 60870-5-4 time tags: **time a**
(`iec102.TimeA`, CP40Time2a: minute to year with the tariff flags TIS, ETI,
PTI) and **time b** (`iec102.TimeB`, CP56Time2b: milliseconds and seconds
before a time a). Their fields hold the octets as received; `Time(loc)`
converts and uses SU to resolve the repeated hour at the end of summer
time.

## The REE profile

Spanish fiscal metering uses IEC 60870-5-102 with a session (access key),
clock setting, tariff and configuration requests in the private range of
type identifications, and other meanings for the low bits of the quality
descriptor. None of it is IEC 102. Package `iec102/ree` provides it, and it
applies only where the caller asks:

```go
import "github.com/otfabric/go-iec60870-5/iec102/client"

p := iec102.Params{Profile: ree.Profile} // decode REE bodies
c, _ := client.New(port, client.Config{LinkAddr: 1, CommonAddr: 1, Params: p})
_ = ree.Login(ctx, c, key)
periods, _ := c.ReadTotals(ctx, ree.RecordLoadProfile, iec102.Read{
	T: iec102.C_CI_NU_2, First: 1, Last: 6, From: from, To: to,
})
_ = ree.Logout(ctx, c)
```

On the server side `ree.Session` answers the session and clock requests and
passes everything else on.

## Controlling station

`client.New(rw, cfg)` takes the transport (serial port, modem or terminal
server stream) and a `Config`: the link address and its size (two octets
by default, `iec102.NoLinkAddr` for none), the common address, timing,
codec parameters, a `Handler` for ASDUs of another common address, and a
logger. The link is initialized by `Init` or the first request.

IEC 102 is request driven: the client polls only while a request is open.
`Request(ctx, a, until)` sends an ASDU and polls until the answer is
complete: the first reply (`UntilReply`), the activation confirmation
(`UntilConfirmation`) or the activation termination (`UntilTermination`).
A refusal (cause 13..18 or P/N) returns `*iec102.RefusedError`. The typed
helpers are `ReadProductSpec`, `ReadTime`, `ReadTotals` (any of the 20 read
commands C_CI_NA_2 .. C_CI_NU_2) and `ReadSinglePoints`.

## Integrated total station

`server.New(rw, cfg)` answers the link and serves from a `server.Store`:
`AddPeriod(record, IntegratedTotals)` adds one integration period (oldest
first), `AddSinglePoints(record, ...)` adds events. It answers C_RD_NA_2
and C_TI_NA_2 itself and runs C_CI_* and C_SP_* reads with confirmation,
data and termination, splitting periods over several frames where needed.
Other requests go to the `Handler`, which answers with `Send` and `Mirror`.
All answers are class 2 data.

## Not implemented

See the last section of [INTEROPERABILITY.md](INTEROPERABILITY.md):
M_EI_NA_2 and many REE types stay raw, signatures are not checked, no
class 1 or spontaneous data, no shared-line polling of several stations.
