// SPDX-License-Identifier: MIT

// Package iec101 holds what the controlling and the controlled station of
// IEC 60870-5-101 share: the system parameters, the link [State] and the
// sentinel errors.
//
// IEC 60870-5-101 carries the telecontrol ASDUs of package asdu in FT 1.2
// frames (package ft12) over a serial line, with the link procedures of
// IEC 60870-5-2 (package link):
//
//   - unbalanced transmission: the controlling station is the primary
//     station and polls the controlled station for class 1 data (events,
//     confirmations) and class 2 data (cyclic data); see [ModeUnbalanced]
//   - balanced transmission: each station initiates its own messages over a
//     full-duplex point-to-point line; see [ModeBalanced]
//
// Applications import [github.com/otfabric/go-iec60870-5/iec101/client]
// and [github.com/otfabric/go-iec60870-5/iec101/server]. The transport is
// any io.ReadWriter: a serial port (for example github.com/otfabric/go-serial),
// a TCP connection to a terminal server that carries the serial byte
// stream unchanged, or an in-memory pipe for tests. IEC 60870-5-101 over
// such a TCP stream is still IEC 60870-5-101 (FT 1.2 framing and link
// procedures), not IEC 60870-5-104.
package iec101

import (
	"errors"
	"fmt"

	"github.com/otfabric/go-iec60870-5/asdu"
	"github.com/otfabric/go-iec60870-5/ft12"
	"github.com/otfabric/go-iec60870-5/link"
)

// Sentinel errors shared by the client and the server; test with
// errors.Is.
var (
	// ErrClosed reports an operation on a closed client or server.
	ErrClosed = errors.New("iec101: closed")
	// ErrLinkDown reports an operation while the link is not initialized
	// (not yet, or no longer after the peer stopped answering).
	ErrLinkDown = errors.New("iec101: link not initialized")
	// ErrInvalidConfig reports a configuration a client or server cannot
	// be built with.
	ErrInvalidConfig = errors.New("iec101: invalid configuration")
)

// Mode is the link transmission mode.
type Mode uint8

// Transmission modes.
const (
	// ModeUnbalanced: the controlling station polls (IEC 60870-5-2 clause 5).
	ModeUnbalanced Mode = iota
	// ModeBalanced: both stations initiate messages (IEC 60870-5-2
	// clause 6). Requires a full-duplex point-to-point line.
	ModeBalanced
)

// String returns "unbalanced" or "balanced".
func (m Mode) String() string {
	if m == ModeBalanced {
		return "balanced"
	}
	return "unbalanced"
}

// Params are the IEC 60870-5-101 system parameters both stations must
// agree on.
type Params struct {
	// Mode is unbalanced or balanced transmission.
	Mode Mode
	// Link holds the link address size and the link timing.
	Link link.Config
	// ASDU holds the ASDU field sizes. A zero MaxSize is set to the largest
	// ASDU one frame carries.
	ASDU asdu.Params
}

// DefaultParams returns a common profile: unbalanced, 1-octet link
// address, 1-octet cause of transmission, 1-octet common address and
// 2-octet information object address (IEC 60870-5-101 8, the
// interoperability list leaves each choice to the system).
func DefaultParams() Params {
	return Params{
		Link: link.Config{FT12: ft12.Params{AddrSize: 1}},
		ASDU: asdu.Params{CauseSize: 1, CommonAddrSize: 1, IOASize: 2},
	}
}

// Normalize validates p and fills in defaults.
func (p Params) Normalize() (Params, error) {
	if err := p.Link.FT12.Validate(); err != nil {
		return p, fmt.Errorf("%w: %w", ErrInvalidConfig, err)
	}
	if p.Mode == ModeUnbalanced && p.Link.FT12.AddrSize == 0 {
		return p, fmt.Errorf("%w: unbalanced transmission needs a link address", ErrInvalidConfig)
	}
	if p.ASDU.MaxSize == 0 {
		p.ASDU.MaxSize = p.Link.FT12.MaxData()
	}
	if p.ASDU.MaxSize > p.Link.FT12.MaxData() {
		return p, fmt.Errorf("%w: ASDU max size %d exceeds the %d octets of a frame", ErrInvalidConfig, p.ASDU.MaxSize, p.Link.FT12.MaxData())
	}
	if err := p.ASDU.Validate(); err != nil {
		return p, fmt.Errorf("%w: %w", ErrInvalidConfig, err)
	}
	return p, nil
}

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
