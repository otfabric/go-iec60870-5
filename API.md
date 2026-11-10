# API Reference: otfabric/go-iec60870-5

A guide to the public API. The generated reference, one page per package,
is [docs/api](docs/api/README.md). The IEC 60870-5-104 packages are
described in [docs/iec104/API.md](docs/iec104/API.md).

## Table of contents

- [Package `iec60870`](#package-iec60870)
- [Package `cp`](#package-cp)
- [Package `ft12`](#package-ft12)
- [Package `link`](#package-link)
- [Package `telecontrol`](#package-telecontrol)
- [Packages `iec101`, `iec101/client`, `iec101/server`](#packages-iec101-iec101client-iec101server)
- [IEC 104 interop suite](#iec-104-interop-suite)
- [Concurrency](#concurrency)

## Package `iec60870`

The module root holds the logging interfaces every station accepts:
`Logger` (printf style), `FieldLogger` and `ContextLogger` (structured),
with `NopLogger`, `NewStdLogger`, `NewSlogLogger` and
`NewSlogFieldLogger`. See [OBSERVABILITY.md](OBSERVABILITY.md).

## Package `cp`

The time tags of IEC 60870-5-4: `Timestamp` (a `time.Time` with the IV,
GEN, SU and no-weekday flags), `AppendCP16`/`ParseCP16`,
`AppendCP24`/`ParseCP24`, `AppendCP32`/`ParseCP32`,
`AppendCP56`/`ParseCP56`, `At`, `Now` and the sizes `Size16` .. `Size56`.
`asdu.Timestamp` is an alias of `cp.Timestamp`.

## Package `ft12`

```go
p := ft12.Params{AddrSize: 1}
b, err := ft12.Frame{Kind: ft12.KindVariable, Control: 0x73, Addr: 1, Data: asduBytes}.Encode(p)
f, n, err := ft12.Decode(b, p)          // one frame from a buffer
d := ft12.NewDecoder(port, p)           // frames from a stream
f, err = d.Next()
```

`Frame` (`Kind`, `Control`, `Addr`, `Data`), `Kind` (`KindSingle`,
`KindFixed`, `KindVariable`), `Params` (`AddrSize`, `Validate`, `MaxAddr`,
`MaxData`), `Decoder` (`Next`, `Buffered`, `Reset`, `OnDiscard`).

## Package `link`

`Config` holds the FT 1.2 parameters, `Timeout`, `Retries`,
`SingleCharAck` and the `OnFrame`/`OnDiscard` hooks.

| Type | Role | Methods |
|---|---|---|
| `Primary` | unbalanced primary (polling) | `Init`, `RequestStatus`, `ResetLink`, `ResetUser`, `ResetFCB`, `SendConfirm`, `SendNoReply`, `RequestData`, `Close`, `Done`, `Err` |
| `Secondary` | unbalanced secondary | user data and polls go to a `SecondaryHandler` (`UserData`, `Class1`, `Class2`, `HasClass1`, `Reset`) |
| `Balanced` | balanced station | `Init`, `RequestStatus`, `ResetLink`, `Send`, `Test`, `Close`; peer data goes to a `BalancedHandler` |

`Control` builds and reads the control field (`PrimaryControl`,
`SecondaryControl`, `PRM`, `FCB`, `FCV`, `ACD`, `DFC`, `DIR`, `Func`).
`Status` reports a secondary's ACD and DFC. The function codes are the
`Func*` constants.

## Package `telecontrol`

`Requester` runs controlling-station requests over a `Send` function the
companion standard supplies, and `Dispatch` routes received ASDUs to them:
`Interrogate`, `CounterInterrogate`, `Read`, `Command`, `ClockSync`,
`TestCommand`, `ResetProcess`, `DelayAcquisition`, and the building blocks
`Confirmed` and `Collect`. `FailAll` ends outstanding requests.
`NegativeError` reports a refusal; `Mirrored`, `FromStation`, `Rejected`
and `Confirmation` are the matching rules.

## Packages `iec101`, `iec101/client`, `iec101/server`

`iec101.Params` (`Mode`, `Link`, `ASDU`; `DefaultParams`, `Normalize`),
`iec101.State` (`StateDown`, `StateUp`, `StateClosed`).

```go
c, err := client.New(port, client.Config{Params: p, LinkAddr: 3, Handler: h})
err = c.WaitUp(ctx)
data, err := c.Interrogate(ctx, ca, asdu.QOIStation)
err = c.Command(ctx, ca, asdu.SingleCommand{IOA: 500, Value: true, Select: true})
```

Client methods: `WaitUp`, `State`, `Close`, `Send`, `Interrogate`,
`CounterInterrogate`, `Read`, `Command`, `Deactivate`, `ClockSync`,
`TestCommand` (C_TS_NA_1), `ResetProcess`, `DelayAcquisition`.

```go
s, err := server.New(port, server.Config{Params: p, LinkAddr: 3, CommonAddr: 1,
	Handler: server.HandlerFunc(func(s *server.Server, a *asdu.ASDU) { ... })})
err = s.Send(asdu.New(asdu.CauseSpontaneous, 1, asdu.SinglePoint{IOA: 100, Value: true}))
```

Server methods: `Send` (class 1), `SendClass`, `Pending`, `Reply`,
`Confirm`, `Negative`, `Terminate`, `Reject`, `State`, `Params`, `Close`.

## IEC 104 interop suite

`iec104/interop` (build tag `interop`) runs the IEC 104 packages against the
pinned reference images; `interop.TestReferences` logs which reference
versions a run was qualified against. See
[docs/iec104/INTEROPERABILITY.md](docs/iec104/INTEROPERABILITY.md).

## Concurrency

All methods of the stations are safe for concurrent use. Handlers run on a
goroutine of the station, one call at a time. `ft12.Decoder` is not safe for
concurrent use.
