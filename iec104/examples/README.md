# Examples

Runnable programs for the IEC 60870-5-104 packages. Each is one `main.go`
that uses only the public API. The commands below run from the module root.

The first eight are self-contained: they start a station and a controlling
station in one process, connected over TCP on loopback, so `go run` shows
the whole exchange and there is nothing to set up.

| Example | Shows | Run |
|---------|-------|-----|
| [quickstart](quickstart/main.go) | The smallest complete station and client: a `Mux`, an interrogation handler, `Dial`, `Interrogate` | `go run ./iec104/examples/quickstart` |
| [commands](commands/main.go) | Direct execution, select-before-operate, cancelling a selection, a set point with time tag, refusals and how they surface as `NegativeError` | `go run ./iec104/examples/commands` |
| [files](files/main.go) | File transfer in both directions and the directory: `FileServer` with a source, a sink and a directory; `GetFile`, `PutFile`, `ListFiles` | `go run ./iec104/examples/files` |
| [events](events/main.go) | Reporting on the station's own initiative: the event queue (`Enqueue`, `Pending`), `Broadcast` for cyclic data, end of initialization from a state handler | `go run ./iec104/examples/events` |
| [redundancy](redundancy/main.go) | A redundancy group over two paths: one connection started, `Switchover`, automatic failover, the switch handler | `go run ./iec104/examples/redundancy` |
| [tls](tls/main.go) | TLS with certificates on both sides (IEC 62351-3): a station that only accepts its authority's control centres and reads who is calling | `go run ./iec104/examples/tls` |
| [secure](secure/main.go) | Application-layer security (IEC 62351-5): a Station Association with certificates of a plant authority, AES-256-GCM Secure Data, a command refused by role-based access control | `go run ./iec104/examples/secure` |
| [observability](observability/main.go) | `slog` logging, state handlers and metrics on both sides; request timeout, retries, reconnect and the start handler | `go run ./iec104/examples/observability` |

The last two are a station and a controlling station as separate programs,
for trying the library against something else: another implementation, a
simulator, a device.

| Example | Shows | Run |
|---------|-------|-----|
| [server](server/main.go) | A station with single and double points, measurements, counters, a commandable switch and spontaneous updates | `go run ./iec104/examples/server -addr :2404` |
| [client](client/main.go) | Connect, interrogate, synchronize the clock, optionally operate a switch, then print what the station reports | `go run ./iec104/examples/client -addr 127.0.0.1:2404 -operate 6001` |

Both take `-debug` to log every APDU.

`make build` compiles every example of the module to `bin/example-<name>`
(these two become `bin/example-server` and `bin/example-client`);
`make examples` runs the self-contained ones, which is also how CI keeps
them working.

## Where to go from each

- A station of your own: start from **quickstart**, add handlers per type
  identification as in **commands**, report changes as in **events**.
- A control centre of your own: **client** for the shape of the program,
  **observability** for the options a long-running one needs,
  **redundancy** when the station has two links.
- The API in full: [API.md](../../docs/iec104/API.md). What the library does and does not
  implement of the standard: [INTEROPERABILITY.md](../../docs/iec104/INTEROPERABILITY.md).
