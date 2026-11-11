// SPDX-License-Identifier: MIT

package pcapio

import (
	"encoding/binary"
	"errors"
	"fmt"
	"slices"
	"sort"
	"time"
)

// TCP reassembly. Each direction of a connection is ordered by sequence
// number, from the SYN when the capture holds it and from the lowest
// sequence number otherwise. A segment whose octets were all delivered
// already (a retransmission) is dropped; a segment that overlaps delivered
// octets contributes only its new octets, and differing overlapping octets
// are counted as conflicts. Segments that arrive out of order are put back
// in order. A sequence gap (octets missing from the capture) ends a run of
// contiguous octets.

const (
	etherIPv4 = 0x0800
	etherVLAN = 0x8100
	protoTCP  = 6
)

// Mark is the frame that delivered the octets of a run from Off onwards.
type Mark struct {
	Off   int
	Frame int
	Time  time.Time
}

// Run is a stretch of contiguous octets of one direction.
type Run struct {
	Data  []byte
	Marks []Mark // ascending Off; Marks[0].Off == 0
	// Gap reports that octets that follow the run are missing from the
	// capture.
	Gap bool
}

// Add appends the octets p that frame delivered at t.
func (r *Run) Add(frame int, t time.Time, p []byte) {
	r.Marks = append(r.Marks, Mark{Off: len(r.Data), Frame: frame, Time: t})
	r.Data = append(r.Data, p...)
}

// Span returns the marks of the earliest and the latest frame that
// delivered octets from to end (exclusive).
func (r *Run) Span(from, end int) (first, last Mark) {
	i := sort.Search(len(r.Marks), func(i int) bool { return r.Marks[i].Off > from }) - 1
	i = max(i, 0)
	first, last = r.Marks[i], r.Marks[i]
	for i++; i < len(r.Marks) && r.Marks[i].Off < end; i++ {
		m := r.Marks[i]
		if m.Frame < first.Frame {
			first = m
		}
		if m.Frame > last.Frame {
			last = m
		}
	}
	return first, last
}

// Stats are the reassembly counters of one direction.
type Stats struct {
	// Retransmitted counts segments whose octets were all delivered
	// already; OutOfOrder segments that arrived after a segment with a
	// higher sequence number; Conflicts overlapping octets that differ
	// from the ones delivered first; Gaps sequence gaps.
	Retransmitted, OutOfOrder, Conflicts, Gaps int
}

// Direction is one direction of a TCP connection, reassembled.
type Direction struct {
	Runs []Run
	Stats
}

// Conn is one TCP connection to one of the server ports.
type Conn struct {
	Client, Server string // "ip:port"
	// FirstFrame is the frame that opened the connection, or the first
	// frame seen of it.
	FirstFrame int
	// SYN, FIN and RST seen in either direction.
	Opened, Closed, Reset bool
	// Up is the client to server direction, Down the other.
	Up, Down Direction
}

// segment is the TCP payload of one frame.
type segment struct {
	frame   int
	time    time.Time
	seq     uint32
	end     uint32 // seq plus the payload length
	payload []byte
}

// flow is one direction of a TCP connection, as captured.
type flow struct {
	syn  bool
	isn  uint32
	segs []segment
}

type connKey struct{ client, server string }

type conn struct {
	key                   connKey
	firstFrame            int
	up, down              flow
	opened, closed, reset bool
}

type assembler struct {
	ports   []uint16
	current map[connKey]*conn
	all     []*conn
}

// TCP reassembles the TCP connections of pkts whose server listens on one
// of ports, ordered by the frame that opened them. A connection that
// carried no payload and was not seen opening is left out. Frames that are
// not IPv4/TCP to or from a server port are ignored.
func TCP(pkts []Packet, ports ...uint16) ([]Conn, error) {
	a := &assembler{ports: ports, current: map[connKey]*conn{}}
	for _, p := range pkts {
		if err := a.frame(p); err != nil {
			return nil, fmt.Errorf("frame %d: %w", p.Num, err)
		}
	}
	return a.conns(), nil
}

func (a *assembler) frame(p Packet) error {
	d, ok, err := IPv4(p)
	if err != nil || !ok {
		return err
	}
	ihl := int(d[0]&0x0f) * 4
	total := int(binary.BigEndian.Uint16(d[2:4]))
	if ihl < 20 || total < ihl || d[9] != protoTCP {
		return nil
	}
	if total > len(d) {
		return fmt.Errorf("IPv4 packet of %d octets truncated to %d", total, len(d))
	}
	if binary.BigEndian.Uint16(d[6:8])&0x3fff != 0 {
		return errors.New("fragmented IPv4 is not supported")
	}
	src := fmt.Sprintf("%d.%d.%d.%d", d[12], d[13], d[14], d[15])
	dst := fmt.Sprintf("%d.%d.%d.%d", d[16], d[17], d[18], d[19])
	t := d[ihl:total]
	if len(t) < 20 {
		return nil
	}
	sport, dport := binary.BigEndian.Uint16(t[0:2]), binary.BigEndian.Uint16(t[2:4])
	thl := int(t[12]>>4) * 4
	if thl < 20 || thl > len(t) {
		return nil
	}
	var key connKey
	var toServer bool
	switch {
	case slices.Contains(a.ports, dport):
		key, toServer = connKey{fmt.Sprintf("%s:%d", src, sport), fmt.Sprintf("%s:%d", dst, dport)}, true
	case slices.Contains(a.ports, sport):
		key = connKey{fmt.Sprintf("%s:%d", dst, dport), fmt.Sprintf("%s:%d", src, sport)}
	default:
		return nil
	}
	flags := t[13]
	syn, ack := flags&0x02 != 0, flags&0x10 != 0
	seq := binary.BigEndian.Uint32(t[4:8])
	c := a.current[key]
	// A SYN from the client on a connection that already carried data or
	// ended starts a new connection on the same ports. So does, in a
	// recording that left the SYN out, a segment whose sequence number is
	// far from where its direction stands: a new initial sequence number.
	if c == nil || (syn && !ack && toServer && (len(c.up.segs)+len(c.down.segs) > 0 || c.closed || c.reset)) ||
		(!syn && c.jump(toServer, seq)) {
		c = &conn{key: key, firstFrame: p.Num}
		a.current[key] = c
		a.all = append(a.all, c)
	}
	f := &c.down
	if toServer {
		f = &c.up
	}
	switch {
	case syn:
		c.opened = true
		f.syn, f.isn = true, seq
	case flags&0x04 != 0:
		c.reset = true
	case flags&0x01 != 0:
		c.closed = true
	}
	if payload := t[thl:]; len(payload) > 0 && !syn {
		// Sequence numbers count modulo 2^32 (RFC 9293 3.4).
		end := seq + uint32(int64(len(payload))&0xFFFFFFFF)
		f.segs = append(f.segs, segment{frame: p.Num, time: p.Time, seq: seq, end: end, payload: payload})
	}
	return nil
}

// maxJump is the largest distance in sequence space between a segment and
// the end of the last segment of its direction that is taken for the same
// connection. Initial sequence numbers are random over 2^32.
const maxJump = 1 << 24

// jump reports whether seq lies too far from the direction's last segment
// to belong to the same connection.
func (c *conn) jump(toServer bool, seq uint32) bool {
	f := &c.down
	if toServer {
		f = &c.up
	}
	if len(f.segs) == 0 {
		return false
	}
	d := seqDiff(seq, f.segs[len(f.segs)-1].end)
	return d > maxJump || d < -maxJump
}

// IPv4 returns the IPv4 packet of a frame, or false for a frame that holds
// none.
func IPv4(p Packet) ([]byte, bool, error) {
	d := p.Data
	switch p.Link {
	case LinkEthernet:
		if len(d) < 14 {
			return nil, false, nil
		}
		etherType := binary.BigEndian.Uint16(d[12:14])
		d = d[14:]
		for etherType == etherVLAN {
			if len(d) < 4 {
				return nil, false, nil
			}
			etherType = binary.BigEndian.Uint16(d[2:4])
			d = d[4:]
		}
		if etherType != etherIPv4 {
			return nil, false, nil
		}
	case LinkNull, LinkLoop:
		// Address family in host (null) or network (loop) byte order; 2 is
		// AF_INET everywhere.
		if len(d) < 4 || (binary.LittleEndian.Uint32(d) != 2 && binary.BigEndian.Uint32(d) != 2) {
			return nil, false, nil
		}
		d = d[4:]
	case LinkSLL:
		if len(d) < 16 || binary.BigEndian.Uint16(d[14:16]) != etherIPv4 {
			return nil, false, nil
		}
		d = d[16:]
	case LinkRaw, LinkIPv4:
	case LinkRTACSerial:
		return nil, false, nil
	default:
		return nil, false, fmt.Errorf("link type %d is not supported", p.Link)
	}
	if len(d) < 20 || d[0]>>4 != 4 {
		return nil, false, nil
	}
	return d, true, nil
}

// seqDiff is a - b in TCP sequence space (RFC 1982 serial arithmetic):
// negative when a lies before b.
func seqDiff(a, b uint32) int64 {
	d := int64(a - b)
	if d >= 1<<31 {
		d -= 1 << 32
	}
	return d
}

// stream reassembles a flow in sequence order into runs of contiguous
// octets.
func (f *flow) stream() Direction {
	var st Stats
	if len(f.segs) == 0 {
		return Direction{}
	}
	var base uint32
	if f.syn {
		base = f.isn + 1
	} else {
		base = f.segs[0].seq
		for _, s := range f.segs[1:] {
			if seqDiff(s.seq, base) < 0 {
				base = s.seq
			}
		}
	}
	// Arrival order: a segment that starts before the end of one that
	// arrived earlier, without being a retransmission of it, arrived out of
	// order.
	var high int64
	for i, s := range f.segs {
		start := seqDiff(s.seq, base)
		if i > 0 && start < high && !f.repeated(i) {
			st.OutOfOrder++
		}
		high = max(high, start+int64(len(s.payload)))
	}
	segs := append([]segment(nil), f.segs...)
	sort.SliceStable(segs, func(i, j int) bool { return seqDiff(segs[i].seq, base) < seqDiff(segs[j].seq, base) })
	out := []Run{{}}
	var next int64 // offset from base of the next octet expected
	for _, s := range segs {
		start := seqDiff(s.seq, base)
		end := start + int64(len(s.payload))
		cur := &out[len(out)-1]
		if end <= next {
			st.Retransmitted++
			st.Conflicts += conflicts(cur, start, next, s.payload)
			continue
		}
		p := s.payload
		switch {
		case start > next && len(cur.Data) > 0: // octets missing from the capture
			st.Gaps++
			cur.Gap = true
			out = append(out, Run{})
			cur = &out[len(out)-1]
		case start < next:
			st.Conflicts += conflicts(cur, start, next, p[:next-start])
			p = p[next-start:]
		}
		cur.Add(s.frame, s.time, p)
		next = end
	}
	return Direction{Runs: out, Stats: st}
}

// repeated reports whether segment i starts where a segment that arrived
// before it started: a retransmission rather than a late segment.
func (f *flow) repeated(i int) bool {
	for _, e := range f.segs[:i] {
		if e.seq == f.segs[i].seq {
			return true
		}
	}
	return false
}

// conflicts counts the octets of p, which starts at stream offset start,
// that differ from the octets of r already delivered up to next. Only the
// part of p inside the current run is compared.
func conflicts(r *Run, start, next int64, p []byte) int {
	runStart := next - int64(len(r.Data))
	n := 0
	for i, c := range p {
		off := start + int64(i) - runStart
		if off >= 0 && off < int64(len(r.Data)) && off < next-runStart && r.Data[off] != c {
			n++
		}
	}
	return n
}

func (a *assembler) conns() []Conn {
	sort.SliceStable(a.all, func(i, j int) bool { return a.all[i].firstFrame < a.all[j].firstFrame })
	out := make([]Conn, 0, len(a.all))
	for _, c := range a.all {
		if len(c.up.segs)+len(c.down.segs) == 0 && !c.opened {
			continue
		}
		out = append(out, Conn{
			Client: c.key.client, Server: c.key.server, FirstFrame: c.firstFrame,
			Opened: c.opened, Closed: c.closed, Reset: c.reset,
			Up: c.up.stream(), Down: c.down.stream(),
		})
	}
	return out
}
