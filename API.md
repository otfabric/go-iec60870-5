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
- [Packages `iec102` and `iec103`](#packages-iec102-and-iec103)
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
`MaxData`), `Decoder` (`Next`, `Buffered`, `Reset`, `OnDiscard`, `Gap`:
the octets of an unfinished frame followed by a longer pause are
discarded).

## Package `link`

`Config` holds the FT 1.2 parameters, `Timeout`, `Retries`,
`SingleCharAck`, `SenderAddr` (balanced responses with the own address
instead of the receiver's, IEC 60870-5-101:2003 6.2.1.2), `FrameGap` (an
unfinished frame followed by a longer pause is discarded; default
`Timeout`, negative disables) and the `OnFrame`/`OnDiscard` hooks.

| Type | Role | Methods |
|---|---|---|
| `Primary` | unbalanced primary (polling) | `Init`, `RequestStatus`, `ResetLink`, `ResetUser`, `ResetFCB`, `SendConfirm`, `SendNoReply`, `RequestData`, `Close`, `Done`, `Err` |
| `Secondary` | unbalanced secondary | `Close`, `Done`, `Err`; user data and polls go to a `SecondaryHandler` (`UserData`, `Class1`, `Class2`, `HasClass1`, `Reset`) |
| `Balanced` | balanced station | `Init`, `RequestStatus`, `ResetLink`, `Send`, `Test`, `Close`, `Done`, `Err`; peer data goes to a `BalancedHandler` |

`Control` builds and reads the control field (`PrimaryControl`,
`SecondaryControl`, `PRM`, `FCB`, `FCV`, `ACD`, `DFC`, `DIR`, `Func`).
`Status` reports a secondary's ACD and DFC. The function codes are the
`Func*` constants.

Optional handler interfaces: `FCBResetHandler` (serves function 7, the
IEC 60870-5-103 extension; without it function 7 is answered 15),
`FlowControlHandler` (`Busy`: DFC in every response and user data refused,
for a `SecondaryHandler` or a `BalancedHandler`), `FunctionFilter`.

Link states of IEC 60870-5-101:2003 figures 5, 6, 8 and 9: a secondary
answers only request status of link and reset of remote link until it is
reset; `Init` repeats request status of link while DFC is set and fails
with `ErrDataFlow` when it stays set; a balanced `Send` waits the same way
after a response with DFC set. A response whose function code cannot
answer the request is discarded; when only such responses came until the
last time-out, the error wraps both `ErrUnexpected` and `ErrTimeout`. `SendConfirm`, `SendNoReply` and `Send` refuse empty data with
`ErrNoData`.

## Package `telecontrol`

`Requester` runs controlling-station requests over a `Send` function the
companion standard supplies, and its `Dispatch` routes received ASDUs to
them: `Interrogate`, `CounterInterrogate`, `Read`, `Command`, `ClockSync`,
`TestCommand`, `ResetProcess`, `MeasureDelay`, `SetDelay`, and the
building blocks `Confirmed` and `Collect`. `FailAll` ends outstanding requests.
`NegativeError` reports a refusal; `Mirrored`, `FromStation`, `Rejected`
and `Confirmation` are the matching rules.

## Packages `iec101`, `iec101/client`, `iec101/server`

`iec101.Params` (`Mode`, `Link`, `ASDU`, `Strict`; `DefaultParams`,
`Normalize`), `iec101.State` (`StateDown`, `StateUp`, `StateClosed`).
`iec101.Validate` checks an ASDU against the IEC 60870-5-101 profile
(`Defined101`, `ErrProfile`, `*ProfileError` with the "unknown ..." cause to
mirror it with); `Params.Strict` applies it in the client and the server.

```go
c, err := client.New(port, client.Config{Params: p, LinkAddr: 3, Handler: h})
err = c.WaitUp(ctx)
data, err := c.Interrogate(ctx, ca, asdu.QOIStation)
err = c.Command(ctx, ca, asdu.SingleCommand{IOA: 500, Value: true, Select: true})
```

Client methods: `WaitUp`, `State`, `Close`, `Done`, `Err`, `Send`,
`Interrogate`, `CounterInterrogate`, `Read`, `Command`, `Deactivate`,
`ClockSync`, `TestCommand` (C_TS_NA_1), `ResetProcess`, `MeasureDelay`,
`SetDelay` (C_CD_NA_1). `WaitUp` returns after `Config.OnState` for the
change to `StateUp` has returned; a request returns at its context
deadline even while the link layer is still repeating it.

```go
s, err := server.New(port, server.Config{Params: p, LinkAddr: 3, CommonAddr: 1,
	Handler: server.HandlerFunc(func(s *server.Server, a *asdu.ASDU) { ... })})
err = s.Send(asdu.New(asdu.CauseSpontaneous, 1, asdu.SinglePoint{IOA: 100, Value: true}))
```

Server methods: `Send` (class 1), `SendClass`, `Pending`, `Reply`,
`Confirm`, `Negative`, `Terminate`, `Reject`, `State`, `Params`, `Close`,
`Done`, `Err`.

## Packages `iec102` and `iec103`

IEC 60870-5-102 (`iec102`, `iec102/client`, `iec102/server`,
`iec102/ree`) and IEC 60870-5-103 (`iec103`, `iec103/client`,
`iec103/server`) have their own ASDU models; their guides are
[docs/iec102/README.md](docs/iec102/README.md) and
[docs/iec103/README.md](docs/iec103/README.md).

## IEC 104 interop suite

`iec104/interop` (build tag `interop`) runs the IEC 104 packages against the
pinned reference images; `interop.TestReferences` logs which reference
versions a run was qualified against. See
[docs/iec104/INTEROPERABILITY.md](docs/iec104/INTEROPERABILITY.md).

## Concurrency

All methods of the stations are safe for concurrent use. Handlers run on a
goroutine of the station, one call at a time. `ft12.Decoder` is not safe for
concurrent use.
