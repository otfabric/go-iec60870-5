// SPDX-License-Identifier: MIT

package capture

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/otfabric/go-iec60870-5/ft12"
	"github.com/otfabric/go-iec60870-5/link"
)

// Step is one ASDU of a recorded exchange: a request the controlling
// station sent (user data, function 3 or 4) or user data the controlled
// station sent (function 8).
type Step struct {
	Request bool
	Decoded
}

// Script returns the ASDUs of a recording that a replay plays: the
// requests of the controlling station and the user data of the controlled
// station at link address addr, in recorded order. Frames of other link
// addresses (a shared line) are left out; requests to the broadcast
// address are kept.
func Script(ds []Decoded, p Params, addr uint16) []Step {
	var out []Step
	for _, d := range ds {
		if d.Kind != ft12.KindVariable || d.ASDU == nil {
			continue
		}
		c := d.Control()
		switch {
		case c.PRM() && (c.Func() == link.FuncUserDataConf || c.Func() == link.FuncUserDataNoRepl) && (d.Addr == addr || d.Addr == p.FT12().MaxAddr()):
			out = append(out, Step{Request: true, Decoded: d})
		case !c.PRM() && c.Func() == link.FuncUserData && d.Addr == addr:
			out = append(out, Step{Decoded: d})
		}
	}
	return out
}

// Station plays a recorded controlled station against a controlling
// station over rw (unbalanced transmission). It answers the link functions
// as IEC 60870-5-2 asks, hands out the recorded user data in order to the
// class 1 and class 2 polls (ACD set while more is queued), and records
// the user data the controlling station sends. A recorded request is a
// barrier: the user data recorded after it is handed out only once the
// controlling station sent a request. Frames to other link addresses are
// ignored; a repeated message (same FCB) gets the previous response again.
type Station struct {
	RW     io.ReadWriter
	Params Params
	Addr   uint16
	Steps  []Step

	mu       sync.Mutex
	next     int           // index of the next step
	received [][]byte      // ASDUs received
	changed  chan struct{} // closed at the next step played or request received
}

// Changed returns a channel that is closed when the station next plays a
// step or receives a request: a test waits on it instead of polling Next
// and Done.
func (s *Station) Changed() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.changed == nil {
		s.changed = make(chan struct{})
	}
	return s.changed
}

// notify wakes the waiters of Changed. The caller holds mu.
func (s *Station) notify() {
	if s.changed != nil {
		close(s.changed)
		s.changed = nil
	}
}

// Received returns the ASDUs the controlling station sent.
func (s *Station) Received() [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([][]byte(nil), s.received...)
}

// Done reports whether every step was played.
func (s *Station) Done() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.next == len(s.Steps)
}

// Next returns the index of the next step to play.
func (s *Station) Next() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.next
}

// Run answers frames until rw fails.
func (s *Station) Run() error {
	fp := s.Params.FT12()
	dec := ft12.NewDecoder(s.RW, fp)
	var (
		fcbKnown bool
		fcb      bool
		last     []byte // the last response
		lastReq  []byte
	)
	for {
		f, err := dec.Next()
		if err != nil {
			if errors.Is(err, ft12.ErrChecksum) || errors.Is(err, ft12.ErrMalformed) {
				continue
			}
			return err
		}
		if f.Kind == ft12.KindSingle {
			continue
		}
		c := link.Control(f.Control)
		if !c.PRM() || (f.Addr != s.Addr && f.Addr != fp.MaxAddr()) {
			continue
		}
		raw, _ := f.Encode(fp)
		if c.FCV() && fcbKnown && c.FCB() == fcb && bytes.Equal(raw, lastReq) && last != nil {
			if _, err := s.RW.Write(last); err != nil {
				return err
			}
			continue
		}
		if c.FCV() {
			fcbKnown, fcb, lastReq = true, c.FCB(), raw
		}
		var resp ft12.Frame
		switch c.Func() {
		case link.FuncResetLink, link.FuncResetFCB:
			fcbKnown = false
			resp = s.fixed(link.FuncAck)
		case link.FuncResetUser:
			resp = s.fixed(link.FuncAck)
		case link.FuncRequestStatus:
			resp = s.fixed(link.FuncStatus)
		case link.FuncUserDataConf, link.FuncUserDataNoRepl:
			s.request(f.Data)
			if c.Func() == link.FuncUserDataNoRepl {
				continue
			}
			resp = s.fixed(link.FuncAck)
		case link.FuncRequestClass1, link.FuncRequestClass2:
			resp = s.data()
		default:
			resp = s.fixed(link.FuncNotImplemented)
		}
		out, err := resp.Encode(fp)
		if err != nil {
			return fmt.Errorf("encode response: %w", err)
		}
		last = out
		if _, err := s.RW.Write(out); err != nil {
			return err
		}
	}
}

// ready reports whether the next step is user data to hand out.
func (s *Station) ready() bool { return s.next < len(s.Steps) && !s.Steps[s.next].Request }

func (s *Station) fixed(fn byte) ft12.Frame {
	s.mu.Lock()
	defer s.mu.Unlock()
	return ft12.Frame{Kind: ft12.KindFixed, Control: byte(link.SecondaryControl(fn, s.ready(), false, false)), Addr: s.Addr}
}

func (s *Station) request(data []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.received = append(s.received, append([]byte(nil), data...))
	if s.next < len(s.Steps) && s.Steps[s.next].Request {
		s.next++
	}
	s.notify()
}

func (s *Station) data() ft12.Frame {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.ready() {
		return ft12.Frame{Kind: ft12.KindFixed, Control: byte(link.SecondaryControl(link.FuncNoData, false, false, false)), Addr: s.Addr}
	}
	d := s.Steps[s.next].Decoded
	s.next++
	s.notify()
	return ft12.Frame{Kind: ft12.KindVariable, Control: byte(link.SecondaryControl(link.FuncUserData, s.ready(), false, false)), Addr: s.Addr, Data: d.Data}
}
