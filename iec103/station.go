// SPDX-License-Identifier: MIT

package iec103

import "errors"

// Sentinel errors of the controlling and the controlled station; test with
// errors.Is.
var (
	// ErrClosed reports an operation on a closed client or server.
	ErrClosed = errors.New("iec103: closed")
	// ErrLinkDown reports an operation while the link is not initialized
	// (not yet, or no longer after the protection equipment stopped
	// answering).
	ErrLinkDown = errors.New("iec103: link not initialized")
	// ErrInvalidConfig reports a configuration a client or server cannot
	// be built with.
	ErrInvalidConfig = errors.New("iec103: invalid configuration")
	// ErrBusy reports a request while another of the same kind is pending:
	// clause 7.4.4 allows one command procedure at a time, clause 7.4.8.4
	// one generic read and one generic write, clause 7.4.7 one disturbance
	// data transfer.
	ErrBusy = errors.New("iec103: request of the same kind pending")
	// ErrNegative reports a negative answer: a negative acknowledgement of
	// a command (cause 21), of a generic write (cause 41), or a negative
	// end of a disturbance data transfer.
	ErrNegative = errors.New("iec103: negative answer")
	// ErrAborted reports a disturbance data transfer the protection
	// equipment aborted (clause 7.4.7, TOO 34, 37 or 40).
	ErrAborted = errors.New("iec103: transfer aborted by the protection equipment")
)

// State is the state of the link as seen by one station.
type State uint8

// Link states.
const (
	// StateDown: the link is not initialized.
	StateDown State = iota
	// StateUp: the link is initialized and user data flows.
	StateUp
	// StateClosed: the station was closed.
	StateClosed
)

// String returns "down", "up" or "closed".
func (s State) String() string {
	switch s {
	case StateDown:
		return "down"
	case StateUp:
		return "up"
	case StateClosed:
		return "closed"
	}
	return "unknown"
}
