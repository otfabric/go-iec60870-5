// SPDX-License-Identifier: MIT

package ft12

import (
	"errors"
	"io"
	"time"
)

// Decoder reads FT 1.2 frames from a byte stream. After octets that do not
// form a valid frame (line noise, a corrupted or truncated frame) it drops
// one octet and searches for the next start character, so a single error
// does not desynchronize the stream (IEC 60870-5-1 6.2.4.2, rule R4).
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
}

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

// skip drops the first octet and every following octet that cannot start
// a frame.
func (d *Decoder) skip(err error) {
	i := d.beg + 1
	for i < d.end {
		if c := d.buf[i]; c == SingleChar || c == StartFixed || c == StartVariable {
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
