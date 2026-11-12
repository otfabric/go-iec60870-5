// SPDX-License-Identifier: MIT

package ft12

import (
	"errors"
	"fmt"
	"io"
	"time"
)

// Decoder reads FT 1.2 frames from a byte stream. After octets that do not
// form a valid frame (line noise, a corrupted or truncated frame) it
// searches for the next start character, so a single error does not
// desynchronize the stream (IEC 60870-5-1 6.2.4.2, rule R4).
//
// It does not resynchronize inside a frame it has rejected: a variable
// frame whose two length octets agree is dropped whole (4 + L + 2 octets,
// also when the rest arrives later), and after any other error a single
// character (E5), which has no checksum, is not accepted until octets
// arrive after a pause of at least ErrorIdle. IEC 60870-5-1 has the
// receiver wait for line idle after an error; a real answer follows a
// request the controlling station sends after the error, so it comes later.
// Resynchronizing on an E5 inside the user data of a corrupted frame took
// it for a valid answer, and the message that frame carried was lost
// (found by otfabric/iec60870ctl's chaos tests).
//
// A Decoder is not safe for concurrent use.
type Decoder struct {
	r   io.Reader
	p   Params
	buf []byte
	beg int // start of unconsumed octets in buf
	end int // end of valid octets in buf

	// OnDiscard, when set, is called with each run of octets the decoder
	// skips while resynchronizing and the error that caused it. The slice
	// is only valid during the call.
	OnDiscard func(skipped []byte, err error)

	// Gap, when positive, is the longest pause between two reads inside a
	// frame: octets of an unfinished frame read before a longer pause are
	// discarded, with [ErrShort], before the octets after it are looked
	// at. FT 1.2 sends a frame without idle line between its characters
	// (IEC 60870-5-1 6.2.4.2, rules R2 and R4); this is the closest a
	// reader without character timing comes to that check.
	Gap time.Duration

	last time.Time // when the last octets were read

	// drop is the number of octets of a rejected variable frame still to
	// be dropped as they arrive.
	drop int
	// noSingle is set by an error and cleared by a read after a pause of
	// ErrorIdle: until then a single character is not a frame start.
	noSingle bool
	// dropErr is the error reported for what drop discards later.
	dropErr error
	// lastRead is when the last octets arrived (also without Gap).
	lastRead time.Time
}

// ErrorIdle is the pause after which a single character is accepted again
// after an error: the line idle IEC 60870-5-1 asks for after an error, for
// a stream without character timing (TCP, a serial driver's buffers).
const ErrorIdle = 10 * time.Millisecond

// NewDecoder returns a decoder reading from r with parameters p. It panics
// if p is invalid; validate parameters supplied at run time first.
func NewDecoder(r io.Reader, p Params) *Decoder {
	if err := p.Validate(); err != nil {
		panic(err)
	}
	// The largest frame is 6 + 255 octets; twice that leaves room to
	// shift.
	return &Decoder{r: r, p: p, buf: make([]byte, 2*(6+MaxLength))}
}

// Buffered returns the number of octets read from the stream but not yet
// returned as part of a frame.
func (d *Decoder) Buffered() int { return d.end - d.beg }

// Reset discards buffered octets, for example after a link timeout when a
// partial frame must not be completed by later octets.
func (d *Decoder) Reset() { d.beg, d.end = 0, 0 }

// Next returns the next valid frame. The frame's Data aliases the
// decoder's buffer and is valid until the next call. Read errors of the
// underlying reader are returned as they are; io.EOF in the middle of a
// frame is returned as io.ErrUnexpectedEOF.
func (d *Decoder) Next() (Frame, error) {
	for {
		if d.drop > 0 && d.end > d.beg {
			n := min(d.drop, d.end-d.beg)
			if d.OnDiscard != nil {
				d.OnDiscard(d.buf[d.beg:d.beg+n], d.dropErr)
			}
			d.beg += n
			d.drop -= n
			continue
		}
		if d.noSingle && d.end > d.beg && d.buf[d.beg] == SingleChar {
			d.skip(errSingleAfterError)
			continue
		}
		if d.end > d.beg {
			f, n, err := Decode(d.buf[d.beg:d.end], d.p)
			switch {
			case err == nil:
				d.beg += n
				return f, nil
			case errors.Is(err, ErrShort):
				// need more octets
			default:
				d.skip(err)
				continue
			}
		}
		if err := d.fill(); err != nil {
			if errors.Is(err, io.EOF) && d.end > d.beg {
				return Frame{}, io.ErrUnexpectedEOF
			}
			return Frame{}, err
		}
	}
}

// errSingleAfterError is the reason a single character read with the
// octets of a rejected frame is dropped.
var errSingleAfterError = fmt.Errorf("%w: single character in the octets of a rejected frame", ErrMalformed)

// skip drops the octets that cannot start a frame: a whole rejected variable
// frame when its length octets agree, else the first octet and every
// following octet that cannot start one. A single character is not a start
// until the next read.
func (d *Decoder) skip(err error) {
	d.noSingle = true
	b := d.buf[d.beg:d.end]
	if len(b) >= 3 && b[0] == StartVariable && b[1] == b[2] && int(b[1]) >= 1+d.p.AddrSize {
		n := 6 + int(b[1])
		if n > len(b) {
			d.drop, d.dropErr = n-len(b), err
			n = len(b)
		}
		if d.OnDiscard != nil {
			d.OnDiscard(b[:n], err)
		}
		d.beg += n
		return
	}
	i := d.beg + 1
	for i < d.end {
		if c := d.buf[i]; c == StartFixed || c == StartVariable {
			break
		}
		i++
	}
	if d.OnDiscard != nil {
		d.OnDiscard(d.buf[d.beg:i], err)
	}
	d.beg = i
}

func (d *Decoder) fill() error {
	if d.beg > 0 {
		d.end = copy(d.buf, d.buf[d.beg:d.end])
		d.beg = 0
	}
	// Like bufio, tolerate a few empty reads before giving up.
	for range 100 {
		n, err := d.r.Read(d.buf[d.end:])
		if n > 0 && d.Gap > 0 {
			now := time.Now()
			if d.end > 0 && now.Sub(d.last) > d.Gap {
				if d.OnDiscard != nil {
					d.OnDiscard(d.buf[:d.end], ErrShort)
				}
				d.end = copy(d.buf, d.buf[d.end:d.end+n]) - n
			}
			d.last = now
		}
		if n > 0 {
			now := time.Now()
			if d.noSingle && !d.lastRead.IsZero() && now.Sub(d.lastRead) >= ErrorIdle {
				d.noSingle = false
			}
			d.lastRead = now
		}
		d.end += n
		if n > 0 {
			return nil
		}
		if err != nil {
			return err
		}
	}
	return io.ErrNoProgress
}
