// SPDX-License-Identifier: MIT

package link

import (
	"context"
	"fmt"
	"io"
	"sync"

	"github.com/otfabric/go-iec60870-5/ft12"
)

// BalancedHandler is the user of a balanced station: it receives the
// peer's user data. Calls come from the station's reader goroutine, one at
// a time, and must return quickly. They run with the station's secondary
// state locked. The handler may call Close, which then does not wait for
// the reader goroutine (see [SecondaryHandler]); a handler that does not
// return normally stops the station. The handler may call Send and the other
// primary methods only from another goroutine: their answers arrive on
// the goroutine the handler blocks. The data passed to UserData is a copy
// the handler may keep; the station also keeps it to recognise a
// repetition, so it must not be modified.
type BalancedHandler interface {
	// UserData delivers user data from the peer. Returning false refuses
	// it: the station answers NACK with DFC set.
	UserData(data []byte) bool
	// Reset is called for reset of remote link (user false) and reset of
	// user process (user true) from the peer.
	Reset(user bool)
}

// BalancedConfig identifies the two ends of a balanced link.
type BalancedConfig struct {
	// Addr is this station's link address: the peer's primary messages
	// carry it, and this station's responses too. With a 0-octet address
	// field it is ignored.
	Addr uint16
	// Remote is the peer's link address, carried by this station's
	// primary messages.
	Remote uint16
	// DIR is the direction bit of every frame this station sends:
	// IEC 60870-5-2 sets it in frames from station A (by convention the
	// controlling station) and clears it in frames from station B.
	DIR bool
}

// Balanced is a combined station of balanced transmission: it is primary
// for the messages it initiates and secondary for its peer's. Both
// directions run at the same time over a full-duplex line.
//
// Its secondary answers only request status of link and reset of remote
// link until the peer reset the link (IEC 60870-5-101:2003 figure 9). Its
// primary waits for the peer to clear DFC before it sends user data again
// (figure 8).
type Balanced struct {
	p    *port
	x    *exchanger
	bc   BalancedConfig
	h    BalancedHandler
	fcb  bool // last FCB sent
	full bool // the peer's last response had DFC set

	mu    sync.Mutex // secondary state
	reset bool
	rx    fcbState
}

// NewBalanced starts a balanced station on rw. Close stops it; rw is
// closed when it is an io.Closer.
func NewBalanced(rw io.ReadWriter, cfg Config, bc BalancedConfig, h BalancedHandler) (*Balanced, error) {
	cfg, err := cfg.withDefaults()
	if err != nil {
		return nil, err
	}
	if bc.Addr > cfg.FT12.MaxAddr() || bc.Remote > cfg.FT12.MaxAddr() {
		return nil, fmt.Errorf("%w: address does not fit %d octet(s)", ErrInvalidConfig, cfg.FT12.AddrSize)
	}
	pt := newPort(rw, cfg, "link.balanced")
	b := &Balanced{p: pt, x: newExchanger(pt, bc.DIR), bc: bc, h: h}
	b.x.alt, b.x.altOK = bc.Addr, true
	go pt.run("BalancedHandler", b.receive)
	return b, nil
}

// Close stops the station and waits for its goroutines, as
// [Primary.Close] does. Called from the handler or another callback, Close
// does not wait for the goroutine the call runs on: it returns once the
// station is stopped, and that goroutine ends when the call has returned.
func (b *Balanced) Close() error { return b.p.close() }

// Done is closed when the station stops (see [Primary.Done]).
func (b *Balanced) Done() <-chan struct{} { return b.p.readDone }

// Err returns the error that stopped the station, if any (see
// [Primary.Err]).
func (b *Balanced) Err() error { return b.p.err() }

func (b *Balanced) receive(f ft12.Frame) {
	if f.Kind == ft12.KindSingle || !Control(f.Control).PRM() {
		b.x.offer(f)
		return
	}
	c := Control(f.Control)
	if b.p.cfg.FT12.AddrSize > 0 && f.Addr != b.bc.Addr {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.reset && c.Func() != FuncRequestStatus && c.Func() != FuncResetLink {
		// "Secondary link not reset" (IEC 60870-5-101:2003 figure 9).
		return
	}
	if r := b.rx.repeated(c, f); r != nil {
		_ = b.p.write(*r)
		return
	}
	var r *ft12.Frame
	switch c.Func() {
	case FuncResetLink:
		b.rx.resetFCB()
		b.reset = true
		b.h.Reset(false)
		r = b.reply(FuncAck, false)
	case FuncResetUser:
		b.h.Reset(true)
		r = b.reply(FuncAck, false)
	case FuncTestLink:
		r = b.reply(FuncAck, false)
	case FuncRequestStatus:
		r = b.reply(FuncStatus, false)
	case FuncUserDataConf:
		switch {
		case f.Kind != ft12.KindVariable:
			r = b.reply(FuncNotImplemented, false)
		case b.busy():
			// "Secondary link busy" (figure 9): Rx[FC3]/Tx (FC1, DFC = 1).
			r = b.reply(FuncNack, true)
		case b.h.UserData(f.Data):
			r = b.reply(FuncAck, false)
		default:
			r = b.reply(FuncNack, true)
		}
	case FuncUserDataNoRepl:
		if f.Kind == ft12.KindVariable {
			b.h.UserData(f.Data)
		}
		return
	default:
		r = b.reply(FuncNotImplemented, false)
	}
	b.rx.remember(c, f, r)
	_ = b.p.write(*r)
}

func (b *Balanced) busy() bool {
	f, ok := b.h.(FlowControlHandler)
	return ok && f.Busy()
}

func (b *Balanced) reply(fn byte, dfc bool) *ft12.Frame {
	dfc = dfc || b.busy()
	if b.p.cfg.SingleCharAck && fn == FuncAck && !dfc {
		return &ft12.Frame{Kind: ft12.KindSingle}
	}
	addr := b.bc.Remote
	if b.p.cfg.SenderAddr {
		addr = b.bc.Addr
	}
	return &ft12.Frame{Kind: ft12.KindFixed, Addr: addr, Control: byte(SecondaryControl(fn, false, dfc, b.bc.DIR))}
}

func (b *Balanced) exchange(ctx context.Context, fn byte, data []byte) (ft12.Frame, error) {
	b.x.mu.Lock()
	defer b.x.mu.Unlock()
	if fcvRequired(fn) {
		b.fcb = !b.fcb
	}
	r, err := b.x.exchange(ctx, b.bc.Remote, fn, b.fcb, data)
	if err == nil && r.Kind != ft12.KindSingle {
		b.full = Control(r.Control).DFC()
	} else if err == nil {
		b.full = false
	}
	return r, err
}

func (b *Balanced) confirm(ctx context.Context, fn byte, data []byte) (Status, error) {
	r, err := b.exchange(ctx, fn, data)
	if err != nil {
		return Status{}, err
	}
	if err := classify(r); err != nil {
		return statusOf(r), err
	}
	if r.Kind != ft12.KindSingle && Control(r.Control).Func() != FuncAck {
		return statusOf(r), fmt.Errorf("%w: %v to function %d", ErrUnexpected, r, fn)
	}
	return statusOf(r), nil
}

// RequestStatus sends request status of link and awaits status of link.
func (b *Balanced) RequestStatus(ctx context.Context) (Status, error) {
	r, err := b.exchange(ctx, FuncRequestStatus, nil)
	if err != nil {
		return Status{}, err
	}
	if err := classify(r); err != nil {
		return statusOf(r), err
	}
	if r.Kind == ft12.KindSingle || Control(r.Control).Func() != FuncStatus {
		return statusOf(r), fmt.Errorf("%w: %v to request status of link", ErrUnexpected, r)
	}
	return statusOf(r), nil
}

// ResetLink sends reset of remote link; the next FCV message carries
// FCB = 1.
func (b *Balanced) ResetLink(ctx context.Context) error {
	_, err := b.confirm(ctx, FuncResetLink, nil)
	if err == nil {
		b.x.mu.Lock()
		b.fcb = false
		b.x.mu.Unlock()
	}
	return err
}

// Init brings up this station's direction of the link: request status of
// link until the peer answers with DFC = 0, then reset of remote link
// (IEC 60870-5-101:2003 figure 8). See [Primary.Init] for the repetition.
func (b *Balanced) Init(ctx context.Context) error {
	if _, err := untilFree(ctx, b.p.cfg, b.RequestStatus); err != nil {
		return err
	}
	return b.ResetLink(ctx)
}

// Test sends the test function for link and awaits the ACK.
func (b *Balanced) Test(ctx context.Context) error {
	_, err := b.confirm(ctx, FuncTestLink, nil)
	return err
}

// Send sends user data with confirmation. When the peer's last response
// had DFC set it first requests status of link until DFC is clear
// ("secondary link busy", IEC 60870-5-101:2003 figure 8) and fails with
// [ErrDataFlow] when it stays set. Empty data fails with [ErrNoData].
//
// The frame refers to data without copying it: data must not be modified
// after the call, since a frame queued before ctx ended is still sent.
// When ctx ends during the exchange the frame count bit of the message is
// spent although no confirmation came: if the message did not reach the
// peer, the peer takes the next message with the same user data for a
// repetition of the last one it accepted and does not deliver it. Run an
// exchange that must not lose its message under a context that outlives
// it, as the iec101 client does.
func (b *Balanced) Send(ctx context.Context, data []byte) error {
	if len(data) == 0 {
		return ErrNoData
	}
	if b.peerFull() {
		if _, err := untilFree(ctx, b.p.cfg, b.RequestStatus); err != nil {
			return err
		}
	}
	_, err := b.confirm(ctx, FuncUserDataConf, data)
	return err
}

func (b *Balanced) peerFull() bool {
	b.x.mu.Lock()
	defer b.x.mu.Unlock()
	return b.full
}
