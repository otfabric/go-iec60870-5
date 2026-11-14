# Observability: otfabric/go-iec60870-5

Logging and metrics for the client and the server. Both are off by default
and cost nothing when off. The sections up to [Connection state](#connection-state)
describe the IEC 60870-5-104 packages; the stations of IEC 101, 102 and 103
are in [Serial companion standards](#serial-companion-standards).

## Contents

- [Logging](#logging)
- [Metrics](#metrics)
- [Connection state](#connection-state)
- [Serial companion standards](#serial-companion-standards)

## Logging

Silent by default. `client.WithLogger` and `server.WithLogger` accept an
`iec60870.Logger`:

```go
type Logger interface {
	Debugf(format string, args ...any)
	Infof(format string, args ...any)
	Warnf(format string, args ...any)
	Errorf(format string, args ...any)
}
```

### Structured logging

The library writes every entry as a constant message plus key-value fields.
When the logger also implements `iec60870.FieldLogger` the fields are passed on
as such; otherwise they are appended to the message as `key=value`.

```go
type FieldLogger interface {
	Logger
	With(keysAndValues ...any) FieldLogger
	DebugKV(msg string, keysAndValues ...any)
	InfoKV(msg string, keysAndValues ...any)
	WarnKV(msg string, keysAndValues ...any)
	ErrorKV(msg string, keysAndValues ...any)
}
```

`iec60870.NewSlogLogger(handler)` wraps a `log/slog` handler and implements
`FieldLogger`, so `slog` users get structured output without doing anything:

```go
logger := iec60870.NewSlogLogger(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
	Level: slog.LevelDebug,
}))
c, err := client.Dial(ctx, "10.0.0.5:2404", client.WithLogger(logger))
```

```json
{"level":"INFO","msg":"connection state","component":"iec104.client","remote":"10.0.0.5:2404","state":"started"}
{"level":"DEBUG","msg":"frame sent","component":"iec104.client","remote":"10.0.0.5:2404","format":"I","ns":0,"nr":0,"len":10}
{"level":"DEBUG","msg":"request","component":"iec104.client","remote":"10.0.0.5:2404","type":"C_IC_NA_1","ca":1,"duration":"3.1ms"}
```

To add your own fields to everything the library logs, use
`NewSlogFieldLogger` and `With`:

```go
logger := iec60870.NewSlogFieldLogger(handler).With("site", "north", "station", 7)
srv, err := server.New(mux, server.WithLogger(logger))
```

A printf-style logger (`iec60870.NewStdLogger`, or your own four methods)
receives the same entries as one line each:

```text
INFO  iec104 client remote=10.0.0.5:2404: connection state state=started
DEBUG iec104 client remote=10.0.0.5:2404: frame sent format=I ns=0 nr=0 len=10
```

### Context

When the logger implements `iec60870.ContextLogger`, entries that belong to a
request (`request`, `request failed, retrying`) are logged with the context
the request method was called with, so a handler can add trace and span
identifiers. `NewSlogLogger` implements it through
`slog.Logger.DebugContext` and friends.

### Entries

| Level | Message | Fields |
|-------|---------|--------|
| Info | `connection state` (client) | `state`, `error` when there is a reason |
| Info | `listening`, `session connected`, `session disconnected`, `session closed` (server) | `address`; `session`, `remote`, `error` |
| Debug | `frame sent`, `frame received` | `format` (`I`, `S`, `U`), and `ns`, `nr`, `len` or `function` |
| Debug | `request` (client) | `type`, `ca`, `duration`, `error` on failure |
| Debug | `connection closed`, `connection terminated` | `error` |
| Warn | `request failed, retrying` (client) | `type`, `ca`, `attempt`, `retry_in`, `error` |
| Warn | `reconnect failed` (client) | `retry_in`, `error` |
| Warn | `connection terminated` (`t1` or a protocol violation) | `error` |
| Warn | `dropping undecodable ASDU` | `error` |
| Warn | `connection rejected` (server) | `remote`, `reason` |
| Warn | `accept failed` (server; Serve retries after `retry_in`) | `retry_in`, `error` |
| Warn | `event queue full, dropping the oldest events` (server; once per overflow) | `group`, `size` |
| Warn | `file transfer refused: the session has the most transfers open` (server, `FileServer.MaxTransfers`) | `open`, `max` |
| Info | `redundancy group switched over` (server) | `group`, `session`, `previous` |
| Warn | `ignoring unsolicited confirmation`, `ignoring U frame from controlled station` | `function` |
| Error | `handler panic` (server) | `asdu`, `panic`, `stack` |
| Error | `callback did not return normally` (client: `Handler`, `state handler`, `start handler`, `switch handler`; the client is closed) | `callback`, `value`, `stack` |

Every entry carries `component` (`iec104.client` or `iec104.server`). Client
entries carry `remote`; entries of a server session carry `session`,
`remote` and `group`.

Messages are constants: match on `msg` and the fields, not on formatted
text. Log calls are made synchronously on the protocol path; keep
implementations non-blocking. Per-frame debug entries are not built at all
when a `NewSlogLogger` handler is not enabled for debug.

Other adapters: `iec60870.NewStdLogger(logger)` for the standard `log` package
and `iec60870.NopLogger()`.

## Metrics

`client.WithMetrics` and `server.WithMetrics` accept an `iec104.Metrics`:

```go
type Metrics interface {
	OnConnect(remote net.Addr)
	OnDisconnect(remote net.Addr, err error)
	OnFrameSent(remote net.Addr, f apci.Frame, size int)
	OnFrameReceived(remote net.Addr, f apci.Frame, size int)
	OnDecodeError(remote net.Addr, err error)
}
```

| Callback | When | Useful for |
|----------|------|------------|
| `OnConnect` | A transport connection is established (client: each successful dial; server: each accepted session) | Connection counts, reconnect rate |
| `OnDisconnect` | A connection ends; `err` is nil when the local application closed it | Classifying losses with `errors.Is` (`ErrTimeout`, `ErrProtocol`, `ErrConnectionLost`) |
| `OnFrameSent` / `OnFrameReceived` | Every APDU; `f.Format` distinguishes I, S and U, `f.Function` the U function | Throughput, TESTFR activity, bytes on the wire |
| `OnDecodeError` | A received ASDU did not decode and was dropped | Detecting a peer with a different ASDU layout |

### Request and handler metrics

Two optional extensions, detected on the same value:

```go
// Client: every request method call.
type RequestMetrics interface {
	OnRequest(remote net.Addr, t asdu.TypeID, ca asdu.CommonAddr)
	OnRequestDone(remote net.Addr, t asdu.TypeID, ca asdu.CommonAddr, duration time.Duration, err error)
	OnRetry(remote net.Addr, t asdu.TypeID, ca asdu.CommonAddr, attempt int, err error)
}

// Server: every ASDU passed to the Handler.
type HandlerMetrics interface {
	OnHandled(remote net.Addr, t asdu.TypeID, cause asdu.Cause, duration time.Duration)
}
```

| Callback | When | Useful for |
|----------|------|------------|
| `OnRequest` | Once, before a request method sends | In-flight gauge, request rate by type |
| `OnRequestDone` | Once, with the final outcome; the duration includes retries and their delays | Latency histograms; error rate split by `*NegativeError`, `context.DeadlineExceeded`, connection errors |
| `OnRetry` | For each attempt that failed and will be repeated | Retry rate: a rising value points at a slow station or a flapping link |
| `OnHandled` | After the server's handler returned or panicked | Handler latency by type identification |

Request metrics are request-level, like the log: one `OnRequest` and one
`OnRequestDone` per call, whatever happened in between.

Embed `iec104.NopMetrics` to implement only what you need:

```go
type metrics struct {
	iec104.NopMetrics
	iFrames  atomic.Int64
	requests *prometheus.HistogramVec
}

func (m *metrics) OnFrameReceived(_ net.Addr, f apci.Frame, _ int) {
	if f.Format == apci.FormatI {
		m.iFrames.Add(1)
	}
}

func (m *metrics) OnRequest(net.Addr, asdu.TypeID, asdu.CommonAddr)             {}
func (m *metrics) OnRetry(net.Addr, asdu.TypeID, asdu.CommonAddr, int, error)   {}
func (m *metrics) OnRequestDone(_ net.Addr, t asdu.TypeID, _ asdu.CommonAddr, d time.Duration, err error) {
	outcome := "ok"
	var neg *iec104.NegativeError
	switch {
	case errors.As(err, &neg):
		outcome = "refused"
	case err != nil:
		outcome = "failed"
	}
	m.requests.WithLabelValues(t.String(), outcome).Observe(d.Seconds())
}
```

The callbacks run synchronously on the protocol path, from several goroutines
on a server. They must be safe for concurrent use and must not block.

`f.ASDU` in the frame callbacks is memory the library still uses: do not
modify it.

## Connection state

State is observable without logs or metrics:

- `Client.State()` and `client.WithStateHandler` on the client
- `Group.Active()`, `Group.Clients()` and `client.WithSwitchHandler` on a redundancy group
- `Server.Sessions()`, `Session.Started()`, `Session.Group()`,
  `Session.Err()` and `server.WithStateHandler` on the server
- `Server.Pending(group)` and `Server.Dropped(group)` for the event queues:
  export them as gauges to see a control centre falling behind

See [ERRORS.md](ERRORS.md#connection-fatal-errors) for the errors the state
handlers receive.

## Serial companion standards

The clients and servers of `iec101`, `iec102` and `iec103` take the same
`iec60870.Logger` (and use `FieldLogger` and `ContextLogger` the same way)
through `Config.Logger`; nil is silent. They have no metrics interface.

| Level | Message | Fields |
|-------|---------|--------|
| Info | `link state` | `state`, `error` |
| Debug | `link init failed` (IEC 101 and 103 clients, IEC 101 server) | `error` |
| Warn | `undecodable ASDU dropped` | `error` |
| Warn | `ASDU dropped by the IEC 60870-5-101 profile` (client), `ASDU refused by the IEC 60870-5-101 profile` (server); only with `Params.Strict` | `error` |
| Debug | `controlling station busy, sending again` (IEC 101 balanced server) | - |
| Warn | `send failed` (IEC 101 server) | `error` |
| Warn | `handler queue full, ASDU dropped` (IEC 102 client), `answer not queued` (IEC 102 server) | - |
| Warn | `identification not queued`, `disturbance data not queued` (IEC 103 server) | - |
| Debug | `interrogation cycle aborted`, `ASDU for another common address dropped` (IEC 103 server) | - |
| Debug | `unexpected ASDU in disturbance data transfer` (IEC 103 client) | - |
| Warn | `time synchronization after initialization failed`, `general interrogation after initialization failed` (IEC 103 client) | `error` |
| Error | `callback did not return normally` (every client and server, and the `link` stations with `link.Config.Logger`; the station stops and `Err` reports it) | `callback`, `value`, `stack` |

Every entry carries `component` and `link` (the link address). The
component is `<standard>.<role>`: `iec101.client`, `iec101.server`,
`iec102.client`, `iec102.server`, `iec103.client`, `iec103.server` (and
`iec104.client`, `iec104.server` for IEC 104). The `link` stations log as
`link.primary`, `link.secondary` and `link.balanced`, without the `link`
field; a station whose `Config.Link.Logger` is nil gives its link its own
`Config.Logger`. The error field is `error` in every package.

State without logs:

- `State()` on every client and server (`Done()` and `Err()` as well on
  every serial client and server: why it stopped, a callback that did not
  return normally included), and
  `Config.OnState`, called for every change of the link state. The
  `WaitUp` of the IEC 101 and IEC 103 clients returns after `OnState` for
  the change to `StateUp` has returned.
- `link.Config.OnFrame` (every frame sent and received) and
  `link.Config.OnDiscard` (octets the FT 1.2 decoder skipped, with the
  reason: noise, a bad checksum, a frame cut off by `FrameGap`), passed in
  `Config.Link` of the IEC 102 and 103 stations and in the `link.Config`
  the IEC 101 stations build from `iec101.Params.Link`.
- `Server.Pending` (IEC 101) for the class 1 and class 2 queues.
