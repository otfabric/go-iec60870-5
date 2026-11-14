// SPDX-License-Identifier: MIT

package iec102

import (
	"errors"
	"fmt"

	"github.com/otfabric/go-iec60870-5/ft12"
)

// Sentinel errors of the controlling and the controlled station; test with
// errors.Is.
var (
	// ErrClosed reports an operation on a closed client or server.
	ErrClosed = errors.New("iec102: closed")
	// ErrLinkDown reports a request that failed because the link could
	// not be initialized or was lost.
	ErrLinkDown = errors.New("iec102: link not initialized")
	// ErrInvalidConfig reports a configuration a client or server cannot
	// be built with.
	ErrInvalidConfig = errors.New("iec102: invalid configuration")
	// ErrNegative reports a refusal of the integrated total station: the
	// request mirrored with the P/N bit or one of the causes 13..18. The
	// error is a [*RefusedError].
	ErrNegative = errors.New("iec102: request refused")
	// ErrResultLimit reports a request whose answer has more ASDUs than
	// the client's Config.MaxResult allows: the request ends there, with
	// the ASDUs so far.
	ErrResultLimit = errors.New("iec102: result limit reached")
)

// RefusedError is the refusal of a request; errors.Is(err, ErrNegative)
// holds.
type RefusedError struct {
	// ASDU is the refusal as received.
	ASDU *ASDU
}

// Error describes the refusal.
func (e *RefusedError) Error() string {
	return fmt.Sprintf("iec102: %s refused: %s", e.ASDU.Type, e.ASDU.Cause)
}

// Unwrap returns [ErrNegative].
func (e *RefusedError) Unwrap() error { return ErrNegative }

// Cause returns the cause of the refusal.
func (e *RefusedError) Cause() Cause { return e.ASDU.Cause }

// State is the state of the link as seen by one station.
type State uint8

// Link states.
const (
	// StateDown: the link is not initialized.
	StateDown State = iota
	// StateUp: the link is initialized.
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

// Link address field sizes.
const (
	// DefaultLinkAddrSize is the link address size when a station's
	// LinkAddrSize is zero: two octets, as in the Beckhoff example
	// configuration (TS650x manual, section 9.3), the REE profile and the
	// recorded traffic.
	DefaultLinkAddrSize = 2
	// NoLinkAddr as a station's LinkAddrSize selects frames without a link
	// address field (Beckhoff TS650x manual, section 3.2: "0, 1 or 2
	// octets"), for point-to-point lines.
	NoLinkAddr = -1
)

// LinkParams returns the FT 1.2 parameters for a link address size as the
// stations' LinkAddrSize takes it: 1, 2, [NoLinkAddr], or 0 for
// [DefaultLinkAddrSize].
func LinkParams(size int) (ft12.Params, error) {
	switch size {
	case 0:
		return ft12.Params{AddrSize: DefaultLinkAddrSize}, nil
	case NoLinkAddr:
		return ft12.Params{AddrSize: 0}, nil
	case 1, 2:
		return ft12.Params{AddrSize: size}, nil
	}
	return ft12.Params{}, fmt.Errorf("%w: link address size %d", ErrInvalidConfig, size)
}
