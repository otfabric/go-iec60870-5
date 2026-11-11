// SPDX-License-Identifier: MIT

package pcapio

import "fmt"

// SEL RTAC serial-line captures (LINKTYPE_RTAC_SERIAL, 250). Every frame
// starts with a 12-octet header, as Wireshark's rtacser dissector reads it:
// a time stamp (seconds and microseconds, 4 octets each, big-endian), the
// event type, the state of the control lines and a 2-octet footer. The
// octets of the line follow the header in the events that start a
// transmission or a reception; the other events (status changes, the end
// of a transmission or reception) carry none.

// RTAC event types.
const (
	RTACStatusChange = 0x00
	RTACTxStart      = 0x01 // the RTAC transmits
	RTACRxStart      = 0x02 // the RTAC receives
	RTACTxEnd        = 0x03
	RTACRxEnd        = 0x04
)

// rtacHeader is the size of the RTAC serial header.
const rtacHeader = 12

// RTACLine is the serial line of an RTAC capture, per direction.
type RTACLine struct {
	// TX holds the octets the RTAC transmitted, RX those it received,
	// each in one run with a mark per frame.
	TX, RX Run
	// Events counts the frames by event type.
	Events map[byte]int
}

// RTAC collects the octets of the RTAC serial-line frames of pkts.
// Frames of other link types are ignored.
func RTAC(pkts []Packet) (RTACLine, error) {
	l := RTACLine{Events: map[byte]int{}}
	for _, p := range pkts {
		if p.Link != LinkRTACSerial {
			continue
		}
		if len(p.Data) < rtacHeader {
			return l, fmt.Errorf("%w: frame %d: RTAC serial frame of %d octets", ErrFormat, p.Num, len(p.Data))
		}
		ev := p.Data[8]
		l.Events[ev]++
		data := p.Data[rtacHeader:]
		if len(data) == 0 {
			continue
		}
		switch ev {
		case RTACTxStart:
			l.TX.Add(p.Num, p.Time, data)
		case RTACRxStart:
			l.RX.Add(p.Num, p.Time, data)
		default:
			return l, fmt.Errorf("%w: frame %d: %d octets in RTAC event 0x%02x", ErrFormat, p.Num, len(data), ev)
		}
	}
	return l, nil
}
