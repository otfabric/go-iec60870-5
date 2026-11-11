// SPDX-License-Identifier: MIT

package link

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/otfabric/go-iec60870-5/ft12"
)

// Errors. They are wrapped with detail; test with errors.Is.
var (
	// ErrClosed reports an operation on a closed station.
	ErrClosed = errors.New("link: closed")
	// ErrTimeout reports that the secondary did not answer within
	// Config.Timeout after Config.Retries repetitions.
	ErrTimeout = errors.New("link: no response")
	// ErrNack reports a secondary that answered NACK (function code 1):
	// the message was not accepted, the link is busy.
	ErrNack = errors.New("link: message not accepted (NACK)")
	// ErrNotFunctioning reports function code 14: link service not
	// functioning.
	ErrNotFunctioning = errors.New("link: link service not functioning")
	// ErrNotImplemented reports function code 15: link service not
	// implemented.
	ErrNotImplemented = errors.New("link: link service not implemented")
	// ErrUnexpected reports a response that does not answer the request:
	// a wrong function code or the address of another station.
	ErrUnexpected = errors.New("link: unexpected response")
	// ErrInvalidConfig reports a Config a station cannot be built with.
	ErrInvalidConfig = errors.New("link: invalid configuration")
	// ErrDataFlow reports a secondary with DFC set: it cannot accept
	// further user data now, and did not clear DFC while the primary
	// requested the status of link (IEC 60870-5-101:2003 6.2.1, figures 5
	// and 8).
	ErrDataFlow = errors.New("link: data flow control (DFC) set")
	// ErrNoData reports user data without octets: a frame without an ASDU
	// is a fixed-length frame (IEC 60870-5-101:2003 6.1), so send/confirm
	// and send/no reply of user data need at least one octet.
	ErrNoData = errors.New("link: empty user data")
)

// Defaults of [Config].
const (
	DefaultTimeout = time.Second
	DefaultRetries = 3
)

// Config holds the link parameters.
type Config struct {
	// FT12 holds the address field size.
	FT12 ft12.Params

	// Timeout is how long a primary waits for the response to a message
	// before it repeats it. Zero means [DefaultTimeout]. It must exceed the
	// round trip plus the transmission time of the longest frame
	// (IEC 60870-5-101:2003 6.2.2, formulas (1) and (2)): about 2.25 s at
	// 1200 bit/s with 240-octet frames. The 1 s default suits 9600 bit/s
	// and faster, and transports such as TCP.
	Timeout time.Duration

	// Retries is how often a primary repeats a message that was not
	// answered. Zero means [DefaultRetries]; negative means none.
	Retries int

	// SingleCharAck makes a secondary answer with the single character
	// E5h instead of a fixed-length ACK, and instead of a fixed-length
	// "no data" NACK when neither ACD nor DFC is set (IEC 60870-5-2 5.1).
	// Primaries always accept E5h.
	SingleCharAck bool

	// SenderAddr makes a balanced station's responses (secondary
	// messages) carry its own link address instead of the peer's. By
	// default they carry the address of the receiver, the peer:
	// IEC 60870-5-101:2003 6.2.1.2 asks for the receiver's address "in
	// primary and secondary messages". lib60870-C v2.4.1 answers with its
	// own address and accepts either. Responses with either address are
	// accepted whatever this says. Unbalanced stations ignore it.
	SenderAddr bool

	// OnFrame, when set, is called for every frame sent (tx true) and
	// received. It runs on the station's goroutines and must not block.
	OnFrame func(tx bool, f ft12.Frame)

	// OnDiscard, when set, is called for octets that do not form a
	// valid frame (line noise, checksum errors).
	OnDiscard func(b []byte, err error)
}

func (c Config) withDefaults() (Config, error) {
	if err := c.FT12.Validate(); err != nil {
		return c, fmt.Errorf("%w: %w", ErrInvalidConfig, err)
	}
	if c.Timeout < 0 {
		return c, fmt.Errorf("%w: negative timeout", ErrInvalidConfig)
	}
	if c.Timeout == 0 {
		c.Timeout = DefaultTimeout
	}
	switch {
	case c.Retries == 0:
		c.Retries = DefaultRetries
	case c.Retries < 0:
		c.Retries = 0
	}
	return c, nil
}

// Broadcast returns the broadcast link address for the address size:
// all ones.
func (c Config) Broadcast() uint16 { return c.FT12.MaxAddr() }

// Status is the state a secondary reports in its responses.
type Status struct {
	// ACD (access demand): the secondary has class 1 data.
	ACD bool
	// DFC (data flow control): the secondary cannot accept more user data.
	DFC bool
}

// port owns the transport: one goroutine reads frames, writes are
// serialized.
type port struct {
	cfg Config
	rw  io.ReadWriter

	tx chan ft12.Frame // frames for the writer goroutine

	done      chan struct{}
	closeOnce sync.Once
	errMu     sync.Mutex
	readErr   error // the transport error that stopped the station
	readDone  chan struct{}
}

func newPort(rw io.ReadWriter, cfg Config) *port {
	p := &port{cfg: cfg, rw: rw, tx: make(chan ft12.Frame, 16), done: make(chan struct{}), readDone: make(chan struct{})}
	go p.writer()
	return p
}

// writer sends queued frames. Writes are decoupled from the reader so a
// station never blocks its own receive path on a full transport (balanced
// stations answer from the reader while their peer does the same).
func (p *port) writer() {
	var buf []byte
	for {
		select {
		case <-p.done:
			return
		case f := <-p.tx:
			buf, _ = f.Append(buf[:0], p.cfg.FT12) // validated by write
			if p.cfg.OnFrame != nil {
				p.cfg.OnFrame(true, f)
			}
			if _, err := p.rw.Write(buf); err != nil {
				select {
				case <-p.done:
				default:
					p.setErr(fmt.Errorf("link: write: %w", err))
				}
				p.shutdown()
				if c, ok := p.rw.(io.Closer); ok {
					_ = c.Close() // unblock the reader
				}
				return
			}
		}
	}
}

// run reads frames until the transport fails or the port is closed, and
// hands each to deliver. Data is copied.
func (p *port) run(deliver func(ft12.Frame)) {
	defer close(p.readDone)
	d := ft12.NewDecoder(p.rw, p.cfg.FT12)
	d.OnDiscard = p.cfg.OnDiscard
	for {
		f, err := d.Next()
		if err != nil {
			select {
			case <-p.done:
			default:
				p.setErr(err)
			}
			p.shutdown()
			return
		}
		if f.Data != nil {
			f.Data = append([]byte(nil), f.Data...)
		}
		if p.cfg.OnFrame != nil {
			p.cfg.OnFrame(false, f)
		}
		deliver(f)
	}
}

// write queues f for the writer goroutine.
func (p *port) write(f ft12.Frame) error {
	if _, err := f.Encode(p.cfg.FT12); err != nil {
		return err
	}
	// Checked first: select picks at random among ready cases.
	select {
	case <-p.done:
		return ErrClosed
	default:
	}
	select {
	case <-p.done:
		return ErrClosed
	case p.tx <- f:
		return nil
	}
}

func (p *port) setErr(err error) {
	p.errMu.Lock()
	defer p.errMu.Unlock()
	if p.readErr == nil {
		p.readErr = err
	}
}

func (p *port) shutdown() { p.closeOnce.Do(func() { close(p.done) }) }

// close stops the station. The transport is closed when it is an
// io.Closer, which unblocks the reader.
func (p *port) close() error {
	p.shutdown()
	var err error
	if c, ok := p.rw.(io.Closer); ok {
		err = c.Close()
	}
	<-p.readDone
	return err
}

// Err returns the error that stopped the reader, nil while it runs or
// after Close.
func (p *port) err() error {
	select {
	case <-p.readDone:
		p.errMu.Lock()
		defer p.errMu.Unlock()
		return p.readErr
	default:
		return nil
	}
}

// exchanger sends primary messages and waits for their responses.
type exchanger struct {
	p    *port
	dir  bool
	mu   sync.Mutex // one exchange at a time
	resp chan ft12.Frame
	// alt, when altOK, is a second address a response may carry: a
	// balanced peer answers with its own address or, as
	// IEC 60870-5-101:2003 6.2.1.2 reads, with ours (the receiver's).
	alt   uint16
	altOK bool
}

func newExchanger(p *port, dir bool) *exchanger {
	return &exchanger{p: p, dir: dir, resp: make(chan ft12.Frame, respQueue)}
}

// respQueue is how many secondary frames wait for an exchange to look at
// them: a late answer followed by the real one must both get there.
const respQueue = 8

// offer hands a secondary frame to the exchange; when frames pile up
// beyond respQueue (nobody is waiting) it is dropped.
func (x *exchanger) offer(f ft12.Frame) {
	select {
	case x.resp <- f:
	default:
	}
}

// drain discards the frames that arrived while no message was
// outstanding, or answer an earlier one.
func (x *exchanger) drain() {
	for {
		select {
		case <-x.resp:
		default:
			return
		}
	}
}

// exchange sends a primary message and returns its response, repeating
// the identical frame after each time-out.
func (x *exchanger) exchange(ctx context.Context, addr uint16, fn byte, fcb bool, data []byte) (ft12.Frame, error) {
	f := ft12.Frame{Kind: ft12.KindFixed, Addr: addr}
	if data != nil {
		f.Kind, f.Data = ft12.KindVariable, data
	}
	f.Control = byte(PrimaryControl(fn, fcb && fcvRequired(fn), fcvRequired(fn), x.dir))
	timer := time.NewTimer(x.p.cfg.Timeout)
	defer timer.Stop()
	var stray *ft12.Frame // the last response that cannot answer fn
	for attempt := 0; ; attempt++ {
		x.drain() // late answers to earlier attempts or messages
		if err := x.p.write(f); err != nil {
			return ft12.Frame{}, err
		}
		timer.Reset(x.p.cfg.Timeout)
	wait:
		select {
		case r := <-x.resp:
			if r.Kind != ft12.KindSingle && r.Addr != addr && (!x.altOK || r.Addr != x.alt) {
				goto wait // another station's answer on a shared line
			}
			if !answers(fn, r) {
				// A late repetition of the answer to an earlier message,
				// say: the answer to this one may still come.
				stray = &r
				goto wait
			}
			return r, nil
		case <-timer.C:
			if attempt >= x.p.cfg.Retries {
				if stray != nil {
					return *stray, fmt.Errorf("%w: %v to function %d", ErrUnexpected, *stray, fn)
				}
				return ft12.Frame{}, fmt.Errorf("%w: function %d to address %d after %d attempt(s)", ErrTimeout, fn, addr, attempt+1)
			}
		case <-ctx.Done():
			return ft12.Frame{}, ctx.Err()
		case <-x.p.done:
			if err := x.p.err(); err != nil {
				return ft12.Frame{}, fmt.Errorf("%w: %w", ErrClosed, err)
			}
			return ft12.Frame{}, ErrClosed
		}
	}
}

// answers reports whether r can be the response to a primary message with
// function fn (IEC 60870-5-101:2003 tables 3 and 4, figures 5 and 8): ACK
// or NACK to send/confirm, status of link to request status of link and to
// request for access demand, user data or "no data" to a request for user
// data. Functions 14 and 15 answer anything; the single character E5h
// stands for ACK or for "no data".
func answers(fn byte, r ft12.Frame) bool {
	if r.Kind == ft12.KindSingle {
		return fn != FuncRequestStatus && fn != FuncAccessDemand
	}
	got := Control(r.Control).Func()
	if got == FuncNotFunctioning || got == FuncNotImplemented {
		return true
	}
	switch fn {
	case FuncRequestStatus, FuncAccessDemand:
		return got == FuncStatus
	case FuncRequestClass1, FuncRequestClass2:
		return got == FuncUserData || got == FuncNoData
	}
	return got == FuncAck || got == FuncNack
}

// classify turns a secondary's negative answer into an error.
func classify(r ft12.Frame) error {
	if r.Kind == ft12.KindSingle {
		return nil
	}
	c := Control(r.Control)
	switch c.Func() {
	case FuncNack:
		return ErrNack
	case FuncNotFunctioning:
		return ErrNotFunctioning
	case FuncNotImplemented:
		return ErrNotImplemented
	}
	return nil
}

func statusOf(r ft12.Frame) Status {
	if r.Kind == ft12.KindSingle {
		return Status{}
	}
	c := Control(r.Control)
	return Status{ACD: c.ACD(), DFC: c.DFC()}
}

// untilFree repeats request status of link while the secondary answers
// with DFC set (IEC 60870-5-101:2003 6.2.1, figure 5: Rx[FC11, DFC = 1]
// leads back to "request status of secondary link"; figure 8: "secondary
// link busy" sends FC9 until FC11 with DFC = 0). It waits Config.Timeout
// between requests and gives up with [ErrDataFlow] after Config.Retries
// repetitions.
func untilFree(ctx context.Context, cfg Config, request func(context.Context) (Status, error)) (Status, error) {
	for attempt := 0; ; attempt++ {
		st, err := request(ctx)
		if err != nil || !st.DFC {
			return st, err
		}
		if attempt >= cfg.Retries {
			return st, fmt.Errorf("%w: still set after %d request(s) of status of link", ErrDataFlow, attempt+1)
		}
		t := time.NewTimer(cfg.Timeout)
		select {
		case <-t.C:
		case <-ctx.Done():
			t.Stop()
			return st, ctx.Err()
		}
	}
}
