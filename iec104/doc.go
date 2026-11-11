// SPDX-License-Identifier: MIT

// Package iec104 holds what the controlling station
// ([github.com/otfabric/go-iec60870-5/iec104/client]) and the controlled
// station ([github.com/otfabric/go-iec60870-5/iec104/server]) of
// IEC 60870-5-104 share: the sentinel errors, [NegativeError], the
// connection [State] and the [Metrics] callbacks with their optional
// extensions [RequestMetrics] and [HandlerMetrics].
//
// The APDU codec and the protocol parameters are in
// [github.com/otfabric/go-iec60870-5/iec104/apci], the ASDU model in
// [github.com/otfabric/go-iec60870-5/asdu] and the application-layer
// security in [github.com/otfabric/go-iec60870-5/sec]. The Logger interfaces
// are in the module root package, [github.com/otfabric/go-iec60870-5].
package iec104
