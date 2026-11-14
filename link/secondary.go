// SPDX-License-Identifier: MIT

package link

import (
	"bytes"
	"io"
	"sync"

	"github.com/otfabric/go-iec60870-5/ft12"
)

// SecondaryHandler is the user of a secondary station: it receives the
// primary's user data and supplies the data the primary polls. Calls come
// from the station's reader goroutine, one at a time, and must return
// quickly: the primary is waiting for the response. They run with the
// station's state locked. The handler may call Close, which then does not
// wait for the reader goroutine: it returns once the station is stopped,
// and no frame is answered after the call. A handler that does not return
// normally stops the station (see [Config.Logger]).
//
// The station keeps what Class1 and Class2 return, to send it again when
// the primary repeats its request (FCB not toggled): the handler must not
// modify it afterwards. The data passed to UserData is a copy the
// handler may keep; the station also keeps it to recognise a repetition,
// so it must not be modified.
type SecondaryHandler interface {
	// UserData delivers user data from the primary (function 3, or
	// function 4 when broadcast or no reply is expected). Returning false
	// refuses it: the station answers NACK with DFC set.
	UserData(data []byte, broadcast bool) bool

	// Class1 returns the next class 1 user data, or nil.
	Class1() []byte
	// Class2 returns the next class 2 user data, or nil.
	Class2() []byte
	// HasClass1 reports whether class 1 data is waiting (ACD).
	HasClass1() bool

	// Reset is called for reset of remote link (user false) and reset of
	// user process (user true).
	Reset(user bool)
}

// FCBResetHandler is implemented by a [SecondaryHandler] that serves
// function 7 "reset of frame count bit", the IEC 60870-5-103 extension
// (6.3): ResetFCB is called after the station reset its expected FCB and
// before it answers ACK. Function 7 is not a function of IEC 60870-5-101
// (table 3): a secondary whose handler does not implement this interface
// answers it with function 15 "link service not implemented".
type FCBResetHandler interface {
	ResetFCB()
}

// FlowControlHandler is implemented by a [SecondaryHandler] or a
// [BalancedHandler] that reports data flow control: while Busy returns
// true every response carries DFC = 1 (IEC 60870-5-2 5.1; IEC 60870-5-103
// 6.3), and user data with confirmation (function 3) is answered NACK with
// DFC = 1 without being delivered ("secondary link busy",
// IEC 60870-5-101:2003 figures 6 and 9).
type FlowControlHandler interface {
	Busy() bool
}

// FunctionFilter is implemented by a [SecondaryHandler] that restricts the
// primary function codes it serves, as a companion standard does
// (IEC 60870-5-103 6.2.3): a message with a function code for which
// Implemented returns false is answered with function 15 "link service
// not implemented" and not processed (IEC 60870-5-103 6.3). The FCB
// bookkeeping is the same as for an accepted message.
type FunctionFilter interface {
	Implemented(fn byte) bool
}

// Secondary is the secondary station of unbalanced transmission: it
// answers the messages addressed to it and ignores the others. Until the
// primary resets the link (function 0) it answers only request status of
// link and reset of remote link and ignores every other message
// (IEC 60870-5-101:2003 6.2.1.1, figure 6).
type Secondary struct {
	p    *port
	addr uint16
	h    SecondaryHandler

	mu    sync.Mutex
	reset bool // the primary reset the link since the station started
	fcb   fcbState
}

// beforeReset reports whether a secondary that has not been reset yet
// answers function fn: request status of link and reset of remote link
// (IEC 60870-5-101:2003 figures 6 and 9), and reset of frame count bit for
// a handler that implements the IEC 60870-5-103 extension, which resets
// the link like function 0 (IEC 60870-5-103 6.3).
func beforeReset(fn byte, h any) bool {
	switch fn {
	case FuncRequestStatus, FuncResetLink:
		return true
	case FuncResetFCB:
		_, ok := h.(FCBResetHandler)
		return ok
	}
	return false
}

// NewSecondary starts a secondary station with link address addr on rw.
// Close stops it; rw is closed when it is an io.Closer.
func NewSecondary(rw io.ReadWriter, cfg Config, addr uint16, h SecondaryHandler) (*Secondary, error) {
	cfg, err := cfg.withDefaults()
	if err != nil {
		return nil, err
	}
	if addr > cfg.FT12.MaxAddr() {
		return nil, ErrInvalidConfig
	}
	s := &Secondary{p: newPort(rw, cfg, "link.secondary"), addr: addr, h: h}
	go s.p.run("SecondaryHandler", s.receive)
	return s, nil
}

// Close stops the station and waits for its goroutines, as
// [Primary.Close] does. Called from the handler or another callback, Close
// does not wait for the goroutine the call runs on: it returns once the
// station is stopped, and that goroutine ends when the call has returned.
func (s *Secondary) Close() error { return s.p.close() }

// Done is closed when the station stops (see [Primary.Done]).
func (s *Secondary) Done() <-chan struct{} { return s.p.readDone }

// Err returns the error that stopped the station, if any (see
// [Primary.Err]).
func (s *Secondary) Err() error { return s.p.err() }

func (s *Secondary) receive(f ft12.Frame) {
	if f.Kind == ft12.KindSingle {
		return
	}
	c := Control(f.Control)
	if !c.PRM() {
		return // another secondary's response on a shared line
	}
	broadcast := f.Addr == s.p.cfg.Broadcast() && s.p.cfg.FT12.AddrSize > 0
	if f.Addr != s.addr && !broadcast {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.reset && !beforeReset(c.Func(), s.h) && s.implemented(c.Func()) {
		// "Secondary link not reset": only request status of link and
		// reset of remote link are answered (IEC 60870-5-101:2003
		// 6.2.1.1, figure 6). A function a FunctionFilter refuses is
		// answered with 15 in any state, as the companion standard of
		// the filter asks (IEC 60870-5-103 6.3).
		return
	}
	if broadcast {
		// Only send/no reply is allowed to the broadcast address.
		if c.Func() == FuncUserDataNoRepl {
			s.h.UserData(f.Data, true)
		}
		return
	}
	if r := s.fcb.repeated(c, f); r != nil {
		// Repetition: the primary did not get our response.
		_ = s.p.write(*r)
		return
	}
	r := s.respond(c, f)
	if r == nil {
		return
	}
	s.fcb.remember(c, f, r)
	_ = s.p.write(*r)
}

// fcbState is the frame count bit bookkeeping of a secondary link
// (IEC 60870-5-101:2003 figures 6 and 9: "FCB not changed" resends the
// previous response).
type fcbState struct {
	known    bool // an FCV message was accepted since start or reset
	expected bool // FCB the next new FCV message carries
	last     *ft12.Frame
	lastFn   byte
	lastData []byte
}

// repeated returns the previous response when c and f repeat the previous
// message: FCV set, the FCB not toggled, and the same function code and
// user data. A message with the old FCB but another function or other data
// is new: several masters do not toggle the FCB as IEC 60870-5-2 asks
// (start with FCB 0 after reset, reuse it after a time-out or a NACK, never
// toggle it, toggle it with function 9), and replaying the stored response
// would answer, say, a class 2 poll with an ACK. Otherwise repeated returns
// nil and, for an FCV message, records the FCB the next one carries.
func (st *fcbState) repeated(c Control, f ft12.Frame) *ft12.Frame {
	if !c.FCV() || !fcvRequired(c.Func()) {
		return nil
	}
	if st.known && c.FCB() != st.expected && st.last != nil &&
		c.Func() == st.lastFn && bytes.Equal(f.Data, st.lastData) {
		return st.last
	}
	st.known, st.expected = true, !c.FCB()
	return nil
}

// remember stores the response r to the message c, f.
func (st *fcbState) remember(c Control, f ft12.Frame, r *ft12.Frame) {
	st.last, st.lastFn, st.lastData = r, c.Func(), f.Data
}

// resetFCB is the effect of reset of remote link and reset of FCB: the
// next FCV message carries FCB = 1.
func (st *fcbState) resetFCB() { st.known, st.expected = true, true }

// implemented reports false for a function the handler's FunctionFilter
// refuses.
func (s *Secondary) implemented(fn byte) bool {
	ff, ok := s.h.(FunctionFilter)
	return !ok || ff.Implemented(fn)
}

func (s *Secondary) busy() bool {
	b, ok := s.h.(FlowControlHandler)
	return ok && b.Busy()
}

func (s *Secondary) reply(fn byte, dfc bool) *ft12.Frame {
	acd := s.h.HasClass1()
	dfc = dfc || s.busy()
	if s.p.cfg.SingleCharAck && !acd && !dfc && (fn == FuncAck || fn == FuncNoData) {
		return &ft12.Frame{Kind: ft12.KindSingle}
	}
	return &ft12.Frame{Kind: ft12.KindFixed, Addr: s.addr, Control: byte(SecondaryControl(fn, acd, dfc, false))}
}

func (s *Secondary) respond(c Control, f ft12.Frame) *ft12.Frame {
	if ff, ok := s.h.(FunctionFilter); ok && !ff.Implemented(c.Func()) {
		if c.Func() == FuncUserDataNoRepl {
			return nil
		}
		return s.reply(FuncNotImplemented, false)
	}
	switch c.Func() {
	case FuncResetLink:
		s.fcb.resetFCB()
		s.reset = true
		s.h.Reset(false)
		return s.reply(FuncAck, false)
	case FuncResetFCB:
		// Not a function of IEC 60870-5-101 (table 3): only a handler
		// that implements the IEC 60870-5-103 extension opts in.
		r, ok := s.h.(FCBResetHandler)
		if !ok {
			return s.reply(FuncNotImplemented, false)
		}
		s.fcb.resetFCB()
		s.reset = true
		r.ResetFCB()
		return s.reply(FuncAck, false)
	case FuncResetUser:
		s.h.Reset(true)
		return s.reply(FuncAck, false)
	case FuncRequestStatus, FuncAccessDemand:
		// Both are answered with status of link (IEC 60870-5-101:2003
		// table 3).
		return s.reply(FuncStatus, false)
	case FuncUserDataConf:
		if f.Kind != ft12.KindVariable {
			return s.reply(FuncNotImplemented, false)
		}
		if s.busy() {
			// "Secondary link busy": user data is not accepted
			// (IEC 60870-5-101:2003 figure 6, Rx[FC3]/Tx (FC1, DFC = 1)).
			return s.reply(FuncNack, true)
		}
		if !s.h.UserData(f.Data, false) {
			return s.reply(FuncNack, true)
		}
		return s.reply(FuncAck, false)
	case FuncUserDataNoRepl:
		if f.Kind == ft12.KindVariable {
			s.h.UserData(f.Data, false)
		}
		return nil
	case FuncRequestClass1, FuncRequestClass2:
		var data []byte
		if c.Func() == FuncRequestClass1 {
			data = s.h.Class1()
		} else {
			data = s.h.Class2()
		}
		if data == nil {
			return s.reply(FuncNoData, false)
		}
		return &ft12.Frame{Kind: ft12.KindVariable, Addr: s.addr, Data: data,
			Control: byte(SecondaryControl(FuncUserData, s.h.HasClass1(), s.busy(), false))}
	}
	return s.reply(FuncNotImplemented, false)
}
