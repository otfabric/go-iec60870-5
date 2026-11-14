// SPDX-License-Identifier: MIT

package link

import (
	"context"
	"fmt"
	"io"
	"sync"

	"github.com/otfabric/go-iec60870-5/ft12"
)

// Class is the class of user data a primary requests.
type Class uint8

// Data classes (IEC 60870-5-2 5.1).
const (
	Class1 Class = 1
	Class2 Class = 2
)

// Primary is the primary station of unbalanced transmission: it is the
// only station that initiates messages, and polls the secondaries
// addressed by link address. Methods are safe for concurrent use; the
// exchanges are serialized because the line carries one at a time.
//
// The frames refer to the caller's user data without copying it: data
// passed to SendConfirm or SendNoReply must not be modified after the
// call. SendNoReply returns once the frame is queued, and a frame queued
// before the context of SendConfirm ended is still sent. The user data
// RequestData returns is the caller's.
//
// When the context of SendConfirm ends during the exchange the frame
// count bit of the message is spent although no confirmation came: if the
// message did not reach the peer, the peer takes the next message with the
// same user data for a repetition of the last one it accepted and does not
// deliver it. Run an exchange that must not lose its message under a
// context that outlives it, as the iec101 client does.
type Primary struct {
	p *port
	x *exchanger

	mu  sync.Mutex
	fcb map[uint16]bool // last FCB sent per secondary
}

// NewPrimary starts a primary station on rw. Close stops it; rw is closed
// when it is an io.Closer.
func NewPrimary(rw io.ReadWriter, cfg Config) (*Primary, error) {
	cfg, err := cfg.withDefaults()
	if err != nil {
		return nil, err
	}
	pt := newPort(rw, cfg)
	pr := &Primary{p: pt, x: newExchanger(pt, false), fcb: map[uint16]bool{}}
	go pt.run(func(f ft12.Frame) {
		if f.Kind == ft12.KindSingle || !Control(f.Control).PRM() {
			pr.x.offer(f)
		}
	})
	return pr, nil
}

// Close stops the station and waits for its goroutines. It closes rw when
// rw is an io.Closer; the reader goroutine returns only when that Close
// unblocks Read, so a transport whose Close does not leaves Close waiting.
// Close is safe to call more than once and from several goroutines: the
// first call closes the transport and returns its error; later calls
// return nil.
func (pr *Primary) Close() error { return pr.p.close() }

// Done is closed when the station stops: after Close or a transport
// failure (see Err).
func (pr *Primary) Done() <-chan struct{} { return pr.p.readDone }

// Err returns the transport error that stopped the station, if any.
func (pr *Primary) Err() error { return pr.p.err() }

// nextFCB toggles and returns the FCB for a new message to addr.
func (pr *Primary) nextFCB(addr uint16) bool {
	pr.mu.Lock()
	defer pr.mu.Unlock()
	pr.fcb[addr] = !pr.fcb[addr]
	return pr.fcb[addr]
}

func (pr *Primary) exchange(ctx context.Context, addr uint16, fn byte, data []byte) (ft12.Frame, error) {
	pr.x.mu.Lock()
	defer pr.x.mu.Unlock()
	fcb := false
	if fcvRequired(fn) {
		fcb = pr.nextFCB(addr)
	}
	return pr.x.exchange(ctx, addr, fn, fcb, data)
}

// RequestStatus sends "request status of link" (function 9) and returns
// the status of link the secondary answers with (function 11).
func (pr *Primary) RequestStatus(ctx context.Context, addr uint16) (Status, error) {
	r, err := pr.exchange(ctx, addr, FuncRequestStatus, nil)
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

// confirm runs a send/confirm exchange that must be answered by ACK.
func (pr *Primary) confirm(ctx context.Context, addr uint16, fn byte, data []byte) (Status, error) {
	r, err := pr.exchange(ctx, addr, fn, data)
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

// ResetLink sends "reset of remote link" (function 0). On success the next
// message with FCV set carries FCB = 1.
func (pr *Primary) ResetLink(ctx context.Context, addr uint16) (Status, error) {
	st, err := pr.confirm(ctx, addr, FuncResetLink, nil)
	if err == nil {
		pr.mu.Lock()
		pr.fcb[addr] = false
		pr.mu.Unlock()
	}
	return st, err
}

// ResetUser sends "reset of user process" (function 1).
func (pr *Primary) ResetUser(ctx context.Context, addr uint16) (Status, error) {
	return pr.confirm(ctx, addr, FuncResetUser, nil)
}

// ResetFCB sends function 7 "reset of frame count bit", the
// IEC 60870-5-103 extension (6.3). Like ResetLink, the next message with
// FCV set carries FCB = 1.
func (pr *Primary) ResetFCB(ctx context.Context, addr uint16) (Status, error) {
	st, err := pr.confirm(ctx, addr, FuncResetFCB, nil)
	if err == nil {
		pr.mu.Lock()
		pr.fcb[addr] = false
		pr.mu.Unlock()
	}
	return st, err
}

// Init brings up the link to a secondary: request status of link until it
// answers with DFC = 0, then reset of remote link (IEC 60870-5-2 5.1, link
// initialization; IEC 60870-5-101:2003 6.2.1.1, figure 5). While the
// secondary answers with DFC = 1 the request is repeated every
// Config.Timeout, at most Config.Retries times, then Init fails with
// [ErrDataFlow].
func (pr *Primary) Init(ctx context.Context, addr uint16) (Status, error) {
	if _, err := untilFree(ctx, pr.p.cfg, func(ctx context.Context) (Status, error) {
		return pr.RequestStatus(ctx, addr)
	}); err != nil {
		return Status{}, err
	}
	return pr.ResetLink(ctx, addr)
}

// SendConfirm sends user data with confirmation (function 3). It returns
// [ErrNack] when the secondary did not accept it, and [ErrNoData] for
// empty data, which has no variable-length frame (IEC 60870-5-101:2003
// 6.1).
func (pr *Primary) SendConfirm(ctx context.Context, addr uint16, data []byte) (Status, error) {
	if len(data) == 0 {
		return Status{}, ErrNoData
	}
	return pr.confirm(ctx, addr, FuncUserDataConf, data)
}

// SendNoReply sends user data without reply (function 4), typically to
// the broadcast address. Nothing is awaited. Empty data fails with
// [ErrNoData].
func (pr *Primary) SendNoReply(addr uint16, data []byte) error {
	if len(data) == 0 {
		return ErrNoData
	}
	f := ft12.Frame{Kind: ft12.KindVariable, Control: byte(PrimaryControl(FuncUserDataNoRepl, false, false, false)), Addr: addr, Data: data}
	pr.x.mu.Lock()
	defer pr.x.mu.Unlock()
	return pr.p.write(f)
}

// RequestData polls a secondary for user data of class c (function 10 or
// 11). It returns the user data, or nil when the secondary has none
// (function 9 or E5h), with the secondary's status: ACD set means class 1
// data is waiting.
func (pr *Primary) RequestData(ctx context.Context, addr uint16, c Class) ([]byte, Status, error) {
	fn := FuncRequestClass2
	if c == Class1 {
		fn = FuncRequestClass1
	}
	r, err := pr.exchange(ctx, addr, fn, nil)
	if err != nil {
		return nil, Status{}, err
	}
	if err := classify(r); err != nil {
		return nil, statusOf(r), err
	}
	if r.Kind == ft12.KindSingle {
		return nil, Status{}, nil
	}
	switch Control(r.Control).Func() {
	case FuncUserData:
		if r.Data == nil {
			return nil, statusOf(r), fmt.Errorf("%w: user data in a fixed frame", ErrUnexpected)
		}
		return r.Data, statusOf(r), nil
	case FuncNoData:
		return nil, statusOf(r), nil
	}
	return nil, statusOf(r), fmt.Errorf("%w: %v to request class %d data", ErrUnexpected, r, c)
}
