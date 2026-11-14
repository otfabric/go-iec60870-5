// SPDX-License-Identifier: MIT

// Package secbind connects iec104/server to the part of package sec that
// binds a connection to an association its controlling station shares
// with other connections. It is not part of the API of sec: sec sets the
// function below when it is initialized.
package secbind

import "time"

// ReceiveAs hands a security ASDU that connection owner received to the
// shared *sec.Endpoint ep, for a connection that does not carry the
// association. The Endpoint runs the key management of that connection on
// its own, without changing the association, until a message authenticates
// the connection. Then the connection carries the association from that
// message on, and bound is true. data and out are what
// sec.Endpoint.Receive returns; out goes to owner's connection.
var ReceiveAs func(ep any, owner any, now time.Time, raw []byte) (data []byte, out [][]byte, bound bool, err error)
