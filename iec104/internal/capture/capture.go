// SPDX-License-Identifier: MIT

// Package capture reads recorded IEC 60870-5-104 traffic for tests: classic
// pcap and pcapng files, gzip-compressed or not, with Ethernet, BSD
// loopback, Linux cooked or raw IPv4 frames. It reassembles each TCP
// direction deterministically and splits the byte stream into APDUs.
//
// Reassembly orders the segments of a direction by sequence number, from
// the SYN when the capture holds it and from the lowest sequence number
// otherwise. A segment whose bytes were all delivered already (a
// retransmission) is dropped; a segment that overlaps delivered bytes
// contributes only its new bytes, and differing overlapping bytes are
// counted as conflicts. Segments that arrive out of order are put back in
// order. A sequence gap (bytes missing from the capture) ends a run of
// contiguous bytes: an APDU cut by it is counted as dropped, and framing
// resumes at the next start octet after it.
//
// The APDU framing is the one of IEC 60870-5-104 clause 5: start octet
// 0x68, a length octet of 4..253 and that many octets. Octets that are not
// a start octet where an APDU should begin are skipped and counted, never
// interpreted. A segment may carry several APDUs and an APDU may span
// several segments; each APDU records the frames its first and last octet
// came in.
//
// Reading the files and the TCP reassembly are package pcapio's, shared
// with the serial companion standards.
//
// It is test infrastructure for the captures in testdata/captures and is not
// part of the public API. It reads files from this repository, but it
// still checks every length it reads.
package capture

import (
	"fmt"
	"strings"
	"time"

	"github.com/otfabric/go-iec60870-5/internal/pcapio"
)

// Port is the IEC 60870-5-104 port.
const Port = 2404

// APDU is one APDU of a conversation, start and length octets included.
type APDU struct {
	FromClient bool      // sent by the side that is not on the server port
	First      int       // 1-based number of the frame that carried the first octet
	Frame      int       // 1-based number of the frame that carried the last octet
	Time       time.Time // time of Frame
	Data       []byte
}

// Conversation is one TCP connection to the server port.
type Conversation struct {
	Client, Server string // "ip:port"
	// SYN, FIN and RST seen in either direction.
	Opened, Closed, Reset bool
	// Retransmitted counts segments whose bytes were all delivered already;
	// OutOfOrder segments that arrived after a segment with a higher
	// sequence number; Conflicts overlapping bytes that differ from the
	// ones delivered first.
	Retransmitted, OutOfOrder, Conflicts int
	// Gaps counts sequence gaps in both directions; Dropped the APDUs cut by
	// a gap or by the end of the capture; Skipped the octets outside any
	// APDU (noise before a start octet, or the rest of an APDU cut by a
	// gap).
	Gaps, Dropped, Skipped int
	// Split counts APDUs that span more than one segment; Shared the
	// segments that carry more than one APDU.
	Split, Shared int
	// APDUs of both directions. Each direction is in sequence order; the
	// two are merged by completing frame.
	APDUs []APDU
}

// ErrFormat reports a file that is neither pcap nor pcapng, or is cut short.
var ErrFormat = pcapio.ErrFormat

// ReadFile reads a capture file and returns its conversations on [Port].
func ReadFile(path string) ([]Conversation, error) {
	b, err := pcapio.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Read(b)
}

// Read parses a capture held in memory and returns its conversations on
// [Port].
func Read(b []byte) ([]Conversation, error) { return ReadPort(b, Port) }

// ReadPort parses a capture held in memory and returns the conversations
// whose server listens on port, ordered by the frame that opened them.
func ReadPort(b []byte, port uint16) ([]Conversation, error) {
	pkts, err := pcapio.Packets(b)
	if err != nil {
		return nil, err
	}
	conns, err := pcapio.TCP(pkts, port)
	if err != nil {
		return nil, err
	}
	return conversations(conns), nil
}

// apdus splits a run into APDUs.
func apdus(r pcapio.Run, fromClient bool) (out []APDU, dropped, skipped, split int, shared map[int]int) {
	shared = map[int]int{}
	d := r.Data
	for off := 0; off < len(d); {
		if d[off] != 0x68 {
			skipped++
			off++
			continue
		}
		if len(d)-off < 2 || int(d[off+1])+2 > len(d)-off {
			// Cut by a gap or by the end of the capture.
			dropped++
			skipped += len(d) - off
			break
		}
		n := int(d[off+1]) + 2
		if n < 6 {
			skipped++
			off++
			continue
		}
		// The octets may have come in several frames, out of order: the
		// APDU is complete with the latest of them.
		first, last := r.Span(off, off+n)
		if first.Frame != last.Frame {
			split++
		}
		shared[last.Frame]++
		out = append(out, APDU{
			FromClient: fromClient, First: first.Frame, Frame: last.Frame, Time: last.Time,
			Data: append([]byte(nil), d[off:off+n]...),
		})
		off += n
	}
	return out, dropped, skipped, split, shared
}

// conversations splits the reassembled connections into APDUs.
func conversations(conns []pcapio.Conn) []Conversation {
	out := make([]Conversation, 0, len(conns))
	for _, c := range conns {
		cv := Conversation{Client: c.Client, Server: c.Server, Opened: c.Opened, Closed: c.Closed, Reset: c.Reset}
		var sides [2][]APDU
		for k, dir := range []struct {
			d          pcapio.Direction
			fromClient bool
		}{{c.Up, true}, {c.Down, false}} {
			cv.Retransmitted += dir.d.Retransmitted
			cv.OutOfOrder += dir.d.OutOfOrder
			cv.Conflicts += dir.d.Conflicts
			cv.Gaps += dir.d.Gaps
			for _, r := range dir.d.Runs {
				p, dropped, skipped, split, shared := apdus(r, dir.fromClient)
				sides[k] = append(sides[k], p...)
				cv.Dropped += dropped
				cv.Skipped += skipped
				cv.Split += split
				for _, n := range shared {
					if n > 1 {
						cv.Shared++
					}
				}
			}
		}
		cv.APDUs = merge(sides[0], sides[1])
		if len(cv.APDUs) > 0 || cv.Opened {
			out = append(out, cv)
		}
	}
	return out
}

// merge merges the APDUs of the two directions by completing frame,
// keeping each direction in sequence order.
func merge(up, down []APDU) []APDU {
	out := make([]APDU, 0, len(up)+len(down))
	for len(up) > 0 && len(down) > 0 {
		if up[0].Frame <= down[0].Frame {
			out, up = append(out, up[0]), up[1:]
		} else {
			out, down = append(out, down[0]), down[1:]
		}
	}
	return append(append(out, up...), down...)
}

// String describes the conversation for test logs.
func (c Conversation) String() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s -> %s: %d APDUs", c.Client, c.Server, len(c.APDUs))
	for _, kv := range []struct {
		k string
		v int
	}{{"retransmitted", c.Retransmitted}, {"out-of-order", c.OutOfOrder}, {"conflicts", c.Conflicts},
		{"gaps", c.Gaps}, {"dropped", c.Dropped}, {"skipped", c.Skipped}, {"split", c.Split}, {"shared", c.Shared}} {
		if kv.v != 0 {
			fmt.Fprintf(&sb, ", %s %d", kv.k, kv.v)
		}
	}
	return sb.String()
}
