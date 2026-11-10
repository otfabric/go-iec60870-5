// SPDX-License-Identifier: MIT

package link

import (
	"io"
	"sync"

	"github.com/otfabric/go-iec60870-5/ft12"
)

// SecondaryHandler is the user of a secondary station: it receives the
// primary's user data and supplies the data the primary polls. Calls come
// from the station's reader goroutine, one at a time, and must return
// quickly: the primary is waiting for the response.
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

// Secondary is the secondary station of unbalanced transmission: it
// answers the messages addressed to it and ignores the others.
type Secondary struct {
	p    *port
	addr uint16
	h    SecondaryHandler

	mu       sync.Mutex
	known    bool // an FCV message was accepted since start or reset
	expected bool // FCB the next new FCV message carries
	last     *ft12.Frame
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
	s := &Secondary{p: newPort(rw, cfg), addr: addr, h: h}
	go s.p.run(s.receive)
	return s, nil
}

// Close stops the station.
func (s *Secondary) Close() error { return s.p.close() }

// Done is closed when the station stops.
func (s *Secondary) Done() <-chan struct{} { return s.p.readDone }

// Err returns the transport error that stopped the station, if any.
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
	if broadcast {
		// Only send/no reply is allowed to the broadcast address.
		if c.Func() == FuncUserDataNoRepl {
			s.h.UserData(f.Data, true)
		}
		return
	}
	if c.FCV() && fcvRequired(c.Func()) {
		if s.known && c.FCB() != s.expected && s.last != nil {
			// Repetition: the primary did not get our response.
			_ = s.p.write(*s.last)
			return
		}
		s.known, s.expected = true, !c.FCB()
	}
	r := s.respond(c, f)
	if r == nil {
		return
	}
	s.last = r
	_ = s.p.write(*r)
}

func (s *Secondary) reply(fn byte, dfc bool) *ft12.Frame {
	acd := s.h.HasClass1()
	if s.p.cfg.SingleCharAck && !acd && !dfc && (fn == FuncAck || fn == FuncNoData) {
		return &ft12.Frame{Kind: ft12.KindSingle}
	}
	return &ft12.Frame{Kind: ft12.KindFixed, Addr: s.addr, Control: byte(SecondaryControl(fn, acd, dfc, false))}
}

func (s *Secondary) respond(c Control, f ft12.Frame) *ft12.Frame {
	switch c.Func() {
	case FuncResetLink:
		s.known, s.expected = true, true
		s.h.Reset(false)
		return s.reply(FuncAck, false)
	case FuncResetFCB:
		s.known, s.expected = true, true
		return s.reply(FuncAck, false)
	case FuncResetUser:
		s.h.Reset(true)
		return s.reply(FuncAck, false)
	case FuncRequestStatus:
		return s.reply(FuncStatus, false)
	case FuncUserDataConf:
		if f.Kind != ft12.KindVariable {
			return s.reply(FuncNotImplemented, false)
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
			Control: byte(SecondaryControl(FuncUserData, s.h.HasClass1(), false, false))}
	}
	return s.reply(FuncNotImplemented, false)
}
