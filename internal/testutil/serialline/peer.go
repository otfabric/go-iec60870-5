// SPDX-License-Identifier: MIT

package serialline

import (
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/otfabric/go-iec60870-5/ft12"
)

// Control field bits and function codes, as IEC 60870-5-2 defines them. They
// are repeated here, not imported from package link, so that the tests of
// package link can use the peer.
const (
	DIR byte = 0x80
	PRM byte = 0x40
	FCB byte = 0x20 // ACD in a secondary frame
	FCV byte = 0x10 // DFC in a secondary frame
)

// ErrNoFrame is returned by Next when no frame came in time.
var ErrNoFrame = errors.New("serialline: no frame")

// Peer is the far end of a line, speaking raw FT 1.2: it writes octets
// straight onto the line and reads the frames the station under test
// sends. Frames are decoded on a goroutine of its own, which ends with the
// line.
type Peer struct {
	T     testing.TB
	End   *End
	P     ft12.Params
	frame chan ft12.Frame

	mu      sync.Mutex
	discard int // octets the decoder skipped
	serve   func(f ft12.Frame) [][]byte
}

// NewPeer starts a peer on e with address size p. The line is closed at the
// end of the test.
func NewPeer(t testing.TB, e *End, p ft12.Params) *Peer {
	pr := &Peer{T: t, End: e, P: p, frame: make(chan ft12.Frame, 1024)}
	go func() {
		d := ft12.NewDecoder(e, p)
		d.OnDiscard = func(b []byte, _ error) { pr.mu.Lock(); pr.discard += len(b); pr.mu.Unlock() }
		for {
			f, err := d.Next()
			if err != nil {
				close(pr.frame)
				return
			}
			f.Data = append([]byte(nil), f.Data...)
			pr.mu.Lock()
			serve := pr.serve
			pr.mu.Unlock()
			if serve != nil && f.Kind != ft12.KindSingle && f.Control&PRM != 0 {
				for _, b := range serve(f) {
					_ = pr.Raw(b)
				}
				continue
			}
			select {
			case pr.frame <- f:
			default: // nobody reads: the station floods, keep the newest out
			}
		}
	}()
	t.Cleanup(func() { _ = e.Close() })
	return pr
}

// Raw writes octets onto the line.
func (p *Peer) Raw(b []byte) error {
	_, err := p.End.Write(b)
	return err
}

// MustRaw writes octets and fails the test on error.
func (p *Peer) MustRaw(b []byte) {
	p.T.Helper()
	if err := p.Raw(b); err != nil {
		p.T.Fatalf("raw write: %v", err)
	}
}

// Encode returns the octets of f.
func (p *Peer) Encode(f ft12.Frame) []byte {
	p.T.Helper()
	b, err := f.Encode(p.P)
	if err != nil {
		p.T.Fatalf("encode %v: %v", f, err)
	}
	return b
}

// Send writes frame f.
func (p *Peer) Send(f ft12.Frame) {
	p.T.Helper()
	p.MustRaw(p.Encode(f))
}

// Next returns the next frame the station sends, waiting at most d.
func (p *Peer) Next(d time.Duration) (ft12.Frame, error) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case f, ok := <-p.frame:
		if !ok {
			return ft12.Frame{}, io.EOF
		}
		return f, nil
	case <-t.C:
		return ft12.Frame{}, ErrNoFrame
	}
}

// Drain discards the frames received so far.
func (p *Peer) Drain() {
	for {
		select {
		case _, ok := <-p.frame:
			if !ok {
				return
			}
		default:
			return
		}
	}
}

// Silent fails the test when the station sends a frame within d.
func (p *Peer) Silent(d time.Duration, what string) {
	p.T.Helper()
	if f, err := p.Next(d); err == nil {
		p.T.Fatalf("%s: station answered %v", what, f)
	}
}

// Fixed returns a fixed-length frame.
func Fixed(c byte, addr uint16) ft12.Frame {
	return ft12.Frame{Kind: ft12.KindFixed, Control: c, Addr: addr}
}

// Variable returns a variable-length frame.
func Variable(c byte, addr uint16, data []byte) ft12.Frame {
	return ft12.Frame{Kind: ft12.KindVariable, Control: c, Addr: addr, Data: data}
}

// Primary returns the control field of a primary message.
func Primary(fn byte, fcb, fcv bool) byte {
	c := PRM | fn&0x0F
	if fcb {
		c |= FCB
	}
	if fcv {
		c |= FCV
	}
	return c
}

// Secondary returns the control field of a secondary message.
func Secondary(fn byte, acd, dfc bool) byte {
	c := fn & 0x0F
	if acd {
		c |= FCB
	}
	if dfc {
		c |= FCV
	}
	return c
}

// Func returns the function code of a frame; -1 for the single character.
func Func(f ft12.Frame) int {
	if f.Kind == ft12.KindSingle {
		return -1
	}
	return int(f.Control & 0x0F)
}

// RawPrimary plays a primary station that behaves, on a peer: it toggles
// the FCB, repeats a message the station did not answer, and checks the
// function code of the answer.
type RawPrimary struct {
	*Peer
	Addr    uint16
	Timeout time.Duration // per attempt
	Tries   int
	// AnyAddr accepts answers with any address: a balanced station
	// answers with the receiver's (IEC 60870-5-101:2003 6.2.1.2).
	AnyAddr bool
	fcb     bool
}

// NewRawPrimary returns a primary addressing addr.
func NewRawPrimary(p *Peer, addr uint16) *RawPrimary {
	return &RawPrimary{Peer: p, Addr: addr, Timeout: 300 * time.Millisecond, Tries: 5}
}

// fcv reports the functions sent with FCV set.
func fcv(fn byte) bool { return fn == 2 || fn == 3 || fn == 10 || fn == 11 }

// Exchange sends a primary message with function fn (and data for a
// variable frame) and returns the answer. It repeats the same frame up to
// Tries times when no answer comes. A message with FCV toggles the FCB
// first.
func (r *RawPrimary) Exchange(fn byte, data []byte) (ft12.Frame, error) {
	if fcv(fn) {
		r.fcb = !r.fcb
	}
	c := Primary(fn, fcv(fn) && r.fcb, fcv(fn))
	f := Fixed(c, r.Addr)
	if data != nil {
		f = Variable(c, r.Addr, data)
	}
	b := r.Encode(f)
	for try := 0; try < r.Tries; try++ {
		r.Drain()
		if err := r.Raw(b); err != nil {
			return ft12.Frame{}, err
		}
		deadline := time.Now().Add(r.Timeout)
		for {
			a, err := r.Next(time.Until(deadline))
			if errors.Is(err, io.EOF) {
				return ft12.Frame{}, err
			}
			if err != nil {
				break
			}
			if a.Kind != ft12.KindSingle && (a.Control&PRM != 0 || a.Addr != r.Addr && !r.AnyAddr) {
				continue
			}
			return a, nil
		}
	}
	return ft12.Frame{}, fmt.Errorf("function %d to %d: %w after %d tries", fn, r.Addr, ErrNoFrame, r.Tries)
}

// Expect runs Exchange and fails the test unless the answer has function
// want (-1: the single character).
func (r *RawPrimary) Expect(fn byte, data []byte, want ...int) ft12.Frame {
	r.T.Helper()
	a, err := r.Exchange(fn, data)
	if err != nil {
		r.T.Fatalf("function %d: %v", fn, err)
	}
	for _, w := range want {
		if Func(a) == w {
			return a
		}
	}
	r.T.Fatalf("function %d answered %v, want function %v", fn, a, want)
	return a
}

// Init requests the status of link and resets the link; the next FCV
// message carries FCB 1.
func (r *RawPrimary) Init() {
	r.T.Helper()
	r.Expect(9, nil, 11)
	r.Expect(0, nil, 0, -1)
	r.fcb = false
}

// SetFCB sets the FCB the last FCV message carried.
func (r *RawPrimary) SetFCB(v bool) { r.fcb = v }

// Poll requests class 1 or 2 data and returns the user data, nil for none.
func (r *RawPrimary) Poll(class int) ([]byte, ft12.Frame) {
	r.T.Helper()
	fn := byte(11)
	if class == 1 {
		fn = 10
	}
	a := r.Expect(fn, nil, 8, 9, -1)
	if Func(a) == 8 {
		return a.Data, a
	}
	return nil, a
}

// PollAll polls (class 1 while ACD is set, then class 2) until the station
// has nothing, at most limit times, and returns the user data.
func (r *RawPrimary) PollAll(limit int) [][]byte {
	r.T.Helper()
	var out [][]byte
	class := 1
	for range limit {
		d, a := r.Poll(class)
		acd := a.Kind != ft12.KindSingle && a.Control&FCB != 0
		if d != nil {
			out = append(out, d)
		}
		if d == nil && !acd && class == 2 {
			return out
		}
		class = 2
		if acd {
			class = 1
		}
	}
	return out
}

// Serve answers each primary message the station sends with the octets
// answer returns, on the peer's reading goroutine; the other frames still
// reach Next.
func (p *Peer) Serve(answer func(f ft12.Frame) [][]byte) {
	p.mu.Lock()
	p.serve = answer
	p.mu.Unlock()
}

// RawSecondary plays a secondary station that behaves, on a peer: it
// answers request status of link, the resets, user data (handing it to
// OnUserData) and the polls (from its class 1 queue), repeats its previous
// answer for a repeated FCB, and lets Hook rewrite or replace every answer.
type RawSecondary struct {
	*Peer
	Addr uint16

	// OnUserData, when set, receives user data of function 3 and 4; its
	// result is queued as class 1 data.
	OnUserData func(data []byte) [][]byte
	// Hook, when set, sees each received frame and the octets the station
	// would answer with, and returns what it does answer with.
	Hook func(f ft12.Frame, answer [][]byte) [][]byte

	mu      sync.Mutex
	queue   [][]byte
	last    []byte
	lastFCB int // -1: none
	polls   atomic.Int64
}

// Polls returns the number of requests for class 1 or 2 data received.
func (s *RawSecondary) Polls() int64 { return s.polls.Load() }

// NewRawSecondary returns a secondary with link address addr.
func NewRawSecondary(p *Peer, addr uint16) *RawSecondary {
	return &RawSecondary{Peer: p, Addr: addr, lastFCB: -1}
}

// Queue adds class 1 user data.
func (s *RawSecondary) Queue(data ...[]byte) {
	s.mu.Lock()
	s.queue = append(s.queue, data...)
	s.mu.Unlock()
}

// Pending returns the queued user data.
func (s *RawSecondary) Pending() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.queue)
}

// Start serves the station's frames until the line closes.
func (s *RawSecondary) Start() {
	s.Serve(func(f ft12.Frame) [][]byte {
		if fn := Func(f); fn == 10 || fn == 11 {
			s.polls.Add(1)
		}
		a := s.answer(f)
		if s.Hook != nil {
			return s.Hook(f, a)
		}
		return a
	})
}

func (s *RawSecondary) answer(f ft12.Frame) [][]byte {
	if f.Kind == ft12.KindSingle || f.Control&PRM == 0 || f.Addr != s.Addr {
		return nil
	}
	fn := f.Control & 0x0F
	s.mu.Lock()
	if f.Control&FCV != 0 {
		fcb := 0
		if f.Control&FCB != 0 {
			fcb = 1
		}
		if fcb == s.lastFCB && s.last != nil {
			r := s.last
			s.mu.Unlock()
			return [][]byte{r}
		}
		s.lastFCB = fcb
	}
	s.mu.Unlock()
	var r ft12.Frame
	switch fn {
	case 9:
		r = Fixed(Secondary(11, s.Pending() > 0, false), s.Addr)
	case 0, 7:
		s.mu.Lock()
		s.lastFCB, s.last = 0, nil
		s.mu.Unlock()
		r = Fixed(Secondary(0, s.Pending() > 0, false), s.Addr)
	case 1, 2:
		r = Fixed(Secondary(0, s.Pending() > 0, false), s.Addr)
	case 3, 4:
		if s.OnUserData != nil {
			s.Queue(s.OnUserData(f.Data)...)
		}
		if fn == 4 {
			return nil
		}
		r = Fixed(Secondary(0, s.Pending() > 0, false), s.Addr)
	case 10, 11:
		s.mu.Lock()
		if len(s.queue) == 0 {
			s.mu.Unlock()
			r = Fixed(Secondary(9, false, false), s.Addr)
			break
		}
		d := s.queue[0]
		s.queue = s.queue[1:]
		acd := len(s.queue) > 0
		s.mu.Unlock()
		r = Variable(Secondary(8, acd, false), s.Addr, d)
	default:
		r = Fixed(Secondary(15, false, false), s.Addr)
	}
	b := s.Encode(r)
	if f.Control&FCV != 0 {
		s.mu.Lock()
		s.last = b
		s.mu.Unlock()
	}
	return [][]byte{b}
}
