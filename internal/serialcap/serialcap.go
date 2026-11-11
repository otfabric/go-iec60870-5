// SPDX-License-Identifier: MIT

// Package serialcap splits the recorded octets of an FT 1.2 line
// (IEC 60870-5-1 6.2.4.2) into frames and checks the link procedures of
// IEC 60870-5-2 in them: the control field, the frame count bit of each
// primary direction and link address, and the pairing of every primary
// message with the secondary's response. The octets come from package
// pcapio: an RTAC serial-line capture or a TCP stream of a serial port
// server, one run of contiguous octets per direction and gap.
//
// The rules applied, with the text they rest on (IEC 60870-5-2 is not held;
// IEC 60870-5-101:2003 clause 6.2 and IEC 60870-5-103:1997 clause 6.2-6.3
// profile it, see docs/STANDARDS.md):
//
//   - FCV is set in the primary functions 2, 3, 10 and 11 and clear in the
//     others (IEC 60870-5-2 tables 1 and 3, as package link sends them).
//   - A primary toggles FCB for every new message with FCV set, and repeats
//     a message that was not answered with the same FCB and the same
//     octets; after reset of remote link (function 0) or of the frame
//     count bit (function 7) the next message with FCV set carries FCB 1.
//   - Every primary message except send/no reply (function 4) is answered
//     by the secondary before the primary sends its next message to the
//     same direction, or it is repeated: a primary that sends something
//     else is counted as giving up on the message.
//   - The response matches the request: ACK, NACK or E5h to send/confirm
//     (0, 1, 2, 3, 7), status of link to 8 and 9, user data, "no data" or
//     E5h to 10 and 11; 14 and 15 to anything.
//
// It is test infrastructure for the captures in the testdata/captures
// directories and is not part of the public API.
package serialcap

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/otfabric/go-iec60870-5/ft12"
	"github.com/otfabric/go-iec60870-5/internal/pcapio"
	"github.com/otfabric/go-iec60870-5/link"
)

// Recording is one line of a capture file.
type Recording struct {
	// Name is "rtac" for an RTAC serial line and "client -> server" for a
	// TCP connection.
	Name string
	// Dirs names the two directions: "rx" and "tx" of the RTAC, or the
	// client and the server.
	Dirs [2]string
	Line
	// Conn holds the TCP counters of a TCP line.
	Conn *pcapio.Conn
}

// ReadFile reads a capture and splits its lines into FT 1.2 frames under
// p: the RTAC serial line when the file has one, and the TCP connections
// to ports otherwise.
func ReadFile(path string, p ft12.Params, ports ...uint16) ([]Recording, error) {
	b, err := pcapio.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Read(b, p, ports...)
}

// Read is [ReadFile] on a capture held in memory.
func Read(b []byte, p ft12.Params, ports ...uint16) ([]Recording, error) {
	pkts, err := pcapio.Packets(b)
	if err != nil {
		return nil, err
	}
	if len(pkts) > 0 && pkts[0].Link == pcapio.LinkRTACSerial {
		l, err := pcapio.RTAC(pkts)
		if err != nil {
			return nil, err
		}
		return []Recording{{Name: "rtac", Dirs: [2]string{"rx", "tx"},
			Line: Split([2][]pcapio.Run{{l.RX}, {l.TX}}, p)}}, nil
	}
	conns, err := pcapio.TCP(pkts, ports...)
	if err != nil {
		return nil, err
	}
	out := make([]Recording, 0, len(conns))
	for i := range conns {
		c := &conns[i]
		out = append(out, Recording{Name: c.Client + " -> " + c.Server, Dirs: [2]string{c.Client, c.Server},
			Line: Split([2][]pcapio.Run{c.Up.Runs, c.Down.Runs}, p), Conn: c})
	}
	return out, nil
}

// Errors of octets that are not a frame.
var (
	// ErrNoise reports octets that are not a start character where a
	// frame should begin.
	ErrNoise = errors.New("serialcap: octets outside any frame")
	// ErrCut reports a frame cut by a gap in the recording or by its end.
	ErrCut = errors.New("serialcap: frame cut by the end of the recording or a gap")
)

// Frame is one FT 1.2 frame of a line.
type Frame struct {
	// Dir is the direction it was sent in: 0 or 1, as given to [Split].
	Dir int
	// First and Last are the 1-based numbers of the capture frames that
	// carried its first and last octet; Time is the time of Last.
	First, Last int
	Time        time.Time
	// Raw are the octets as recorded.
	Raw []byte
	ft12.Frame
}

// Control returns the link control field (not used by E5h).
func (f Frame) Control() link.Control { return link.Control(f.Frame.Control) }

// Bad is a stretch of recorded octets that is not a frame.
type Bad struct {
	Dir         int
	First, Last int
	Raw         []byte
	Err         error
}

func (b Bad) String() string {
	return fmt.Sprintf("dir %d frames %d..%d: %d octets: %v", b.Dir, b.First, b.Last, len(b.Raw), b.Err)
}

// Line is the frames of both directions, merged by completing frame.
type Line struct {
	Frames []Frame
	Bad    []Bad
}

// Split splits the runs of both directions into FT 1.2 frames under p.
func Split(dirs [2][]pcapio.Run, p ft12.Params) Line {
	var sides [2][]Frame
	var l Line
	for d, runs := range dirs {
		for _, r := range runs {
			fs, bad := split(r, d, p)
			sides[d] = append(sides[d], fs...)
			l.Bad = append(l.Bad, bad...)
		}
	}
	l.Frames = merge(sides[0], sides[1])
	return l
}

func isStart(c byte) bool {
	return c == ft12.StartFixed || c == ft12.StartVariable || c == ft12.SingleChar
}

// split splits one run. Octets that do not start a valid frame are
// collected into a [Bad] up to the next start character that does.
func split(r pcapio.Run, dir int, p ft12.Params) (out []Frame, bad []Bad) {
	d := r.Data
	var cur *Bad
	flush := func() {
		if cur != nil {
			bad = append(bad, *cur)
			cur = nil
		}
	}
	addBad := func(from, end int, err error) {
		first, last := r.Span(from, end)
		if cur == nil {
			cur = &Bad{Dir: dir, First: first.Frame, Err: err}
		}
		cur.Last = last.Frame
		cur.Raw = append(cur.Raw, d[from:end]...)
	}
	for off := 0; off < len(d); {
		if !isStart(d[off]) {
			addBad(off, off+1, ErrNoise)
			off++
			continue
		}
		f, n, err := ft12.Decode(d[off:], p)
		switch {
		case errors.Is(err, ft12.ErrShort):
			flush()
			addBad(off, len(d), ErrCut)
			off = len(d)
			continue
		case err != nil:
			addBad(off, off+1, err)
			off++
			continue
		}
		flush()
		first, last := r.Span(off, off+n)
		raw := append([]byte(nil), d[off:off+n]...)
		if f.Kind == ft12.KindVariable {
			f.Data = raw[len(raw)-2-len(f.Data) : len(raw)-2]
		}
		out = append(out, Frame{Dir: dir, First: first.Frame, Last: last.Frame, Time: last.Time, Raw: raw, Frame: f})
		off += n
	}
	flush()
	return out, bad
}

// merge merges the frames of the two directions by completing frame,
// keeping each direction in order.
func merge(a, b []Frame) []Frame {
	out := make([]Frame, 0, len(a)+len(b))
	for len(a) > 0 && len(b) > 0 {
		if a[0].Last <= b[0].Last {
			out, a = append(out, a[0]), a[1:]
		} else {
			out, b = append(out, b[0]), b[1:]
		}
	}
	return append(append(out, a...), b...)
}

// Exact reports whether f encodes back to the recorded octets under p.
func (f Frame) Exact(p ft12.Params) bool {
	b, err := f.Encode(p)
	return err == nil && bytes.Equal(b, f.Raw)
}

// Finding is one observation of the link analysis.
type Finding struct {
	Frame int // capture frame number of the frame it is about
	What  string
}

func (f Finding) String() string { return fmt.Sprintf("frame %d: %s", f.Frame, f.What) }

// Link is what the link analysis derives from a line.
type Link struct {
	// Primary, Secondary and Single count the frames per direction: with
	// PRM set, with PRM clear, and the single character E5h.
	Primary, Secondary, Single [2]int
	// Balanced reports primary messages in both directions.
	Balanced bool
	// PrimaryFunc and SecondaryFunc count the function codes.
	PrimaryFunc, SecondaryFunc map[byte]int
	// Exchanges counts primary messages answered by a matching response;
	// NoReply the send/no reply messages; Leading the responses before
	// the first primary message (the recording starts after the
	// request); Overheard the responses that came in the direction of
	// their request (a shared line on which the recording station hears
	// other stations).
	Exchanges, NoReply, Leading, Overheard int
	// Repeated counts primary messages sent again with the same FCB and
	// octets, RepeatedAnswered those of them whose first transmission was
	// answered (the primary did not receive the response).
	Repeated, RepeatedAnswered int
	// Abandoned lists the capture frames of the messages neither answered
	// nor repeated before the primary sent another.
	Abandoned []int
	// FCBToggled counts the messages with FCV set whose FCB was checked
	// against the previous one of the direction and address.
	FCBToggled int
	// Violations are departures from the rules in the package
	// documentation: they are the peer's (or the recording's) and are
	// recorded, not fixed.
	Violations []Finding
}

// fcvRequired reports whether a primary function is sent with FCV set
// (IEC 60870-5-2 tables 1 and 3).
func fcvRequired(fn byte) bool {
	switch fn {
	case link.FuncTestLink, link.FuncUserDataConf, link.FuncRequestClass1, link.FuncRequestClass2:
		return true
	}
	return false
}

// answers reports whether a secondary response with function rfn (or E5h
// when single) answers a primary function fn.
func answers(fn byte, single bool, rfn byte) bool {
	if !single && (rfn == link.FuncNotFunctioning || rfn == link.FuncNotImplemented) {
		return true
	}
	switch fn {
	case link.FuncResetLink, link.FuncResetUser, link.FuncTestLink, link.FuncUserDataConf, link.FuncResetFCB:
		return single || rfn == link.FuncAck || rfn == link.FuncNack
	case link.FuncAccessDemand, link.FuncRequestStatus:
		return !single && rfn == link.FuncStatus
	case link.FuncRequestClass1, link.FuncRequestClass2:
		return single || rfn == link.FuncUserData || rfn == link.FuncNoData
	}
	return false
}

// fcbKey is one primary direction and link address.
type fcbKey struct {
	dir  int
	addr uint16
}

type fcbState struct {
	known    bool // a message with FCV set was seen since the start or a reset
	fcb      bool
	reset    bool // a reset was sent: the next FCV message carries FCB 1
	raw      []byte
	answered bool
}

// Analyze checks the link procedures of a line.
func Analyze(l Line) *Link {
	a := &Link{PrimaryFunc: map[byte]int{}, SecondaryFunc: map[byte]int{}}
	fcb := map[fcbKey]*fcbState{}
	// outstanding[d] is the index of the primary message of direction d
	// that awaits its response, or -1.
	outstanding := [2]int{-1, -1}
	violate := func(f Frame, format string, args ...any) {
		a.Violations = append(a.Violations, Finding{Frame: f.Last, What: fmt.Sprintf(format, args...)})
	}
	for i, f := range l.Frames {
		o := 1 - f.Dir
		if f.Kind == ft12.KindSingle || !f.Control().PRM() {
			// A response: it answers the other direction's message.
			single := f.Kind == ft12.KindSingle
			var rfn byte
			if single {
				a.Single[f.Dir]++
			} else {
				a.Secondary[f.Dir]++
				rfn = f.Control().Func()
				a.SecondaryFunc[rfn]++
			}
			// The response answers the other direction's request. On a
			// shared (multi-drop) line the recording station also hears
			// the requests to and the responses of other stations: those
			// come in the same direction as the request they answer.
			from := o
			if outstanding[o] < 0 && outstanding[f.Dir] >= 0 {
				from = f.Dir
				a.Overheard++
			}
			j := outstanding[from]
			if j < 0 {
				if a.Primary[0]+a.Primary[1] == 0 {
					a.Leading++ // the recording starts after the request
					continue
				}
				violate(f, "response %s without an outstanding request", describe(f))
				continue
			}
			req := l.Frames[j]
			outstanding[from] = -1
			o = from
			if !single && req.Addr != f.Addr {
				violate(f, "response from link address %d to a request to %d", f.Addr, req.Addr)
			}
			if !answers(req.Control().Func(), single, rfn) {
				violate(f, "response %s does not answer request %s (frame %d)", describe(f), describe(req), req.Last)
				continue
			}
			a.Exchanges++
			if s := fcb[fcbKey{o, req.Addr}]; s != nil && bytes.Equal(s.raw, req.Raw) {
				s.answered = true
			}
			continue
		}
		c := f.Control()
		fn := c.Func()
		a.Primary[f.Dir]++
		a.PrimaryFunc[fn]++
		if a.Primary[o] > 0 {
			a.Balanced = true
		}
		if j := outstanding[f.Dir]; j >= 0 && !bytes.Equal(l.Frames[j].Raw, f.Raw) {
			a.Abandoned = append(a.Abandoned, l.Frames[j].Last)
		}
		outstanding[f.Dir] = -1
		if fn == link.FuncUserDataNoRepl {
			a.NoReply++
		} else {
			outstanding[f.Dir] = i
		}
		if c.FCV() != fcvRequired(fn) {
			violate(f, "FCV %v in primary function %d", c.FCV(), fn)
		}
		k := fcbKey{f.Dir, f.Addr}
		s := fcb[k]
		if s == nil {
			s = &fcbState{}
			fcb[k] = s
		}
		if fn == link.FuncResetLink || fn == link.FuncResetFCB {
			s.known, s.reset, s.raw = false, true, nil
			continue
		}
		if !c.FCV() {
			continue
		}
		switch {
		case s.reset && !c.FCB():
			violate(f, "FCB 0 in the first message with FCV set after a reset")
		case s.known && c.FCB() == s.fcb && bytes.Equal(s.raw, f.Raw):
			a.Repeated++
			if s.answered {
				a.RepeatedAnswered++
			}
		case s.known && c.FCB() == s.fcb:
			violate(f, "FCB not toggled for a new message to link address %d (previous %v)", f.Addr, s.fcb)
		case s.known:
			a.FCBToggled++
		}
		s.known, s.reset, s.fcb, s.raw, s.answered = true, false, c.FCB(), f.Raw, false
	}
	return a
}

// describe names a frame's link function for findings.
func describe(f Frame) string {
	if f.Kind == ft12.KindSingle {
		return "E5h"
	}
	c := f.Control()
	if c.PRM() {
		return fmt.Sprintf("PRM fn=%d", c.Func())
	}
	return fmt.Sprintf("fn=%d", c.Func())
}

// Controlling returns the direction that sends the primary messages of an
// unbalanced line: the one with more of them.
func (a *Link) Controlling() int {
	if a.Primary[1] > a.Primary[0] {
		return 1
	}
	return 0
}

// Funcs lists the keys of a function count in ascending order.
func Funcs(m map[byte]int) []byte {
	out := make([]byte, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
