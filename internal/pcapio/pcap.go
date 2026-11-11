// SPDX-License-Identifier: MIT

// Package pcapio reads recorded traffic for the capture tests of every
// companion standard: classic pcap and pcapng files, gzip-compressed or
// not, with Ethernet, BSD loopback, Linux cooked, raw IPv4 or SEL RTAC
// serial-line frames. It reassembles each TCP direction deterministically
// into runs of contiguous octets ([TCP]) and collects the octets of a
// serial line per direction ([RTAC]). Splitting the octets into APDUs or
// FT 1.2 frames is left to the caller.
//
// It is test infrastructure for the captures in the testdata/captures
// directories and is not part of the public API. It reads files from this
// repository, but it still checks every length it reads.
package pcapio

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"time"
)

// ErrFormat reports a file that is neither pcap nor pcapng, or is cut short.
var ErrFormat = errors.New("pcapio: unsupported or malformed capture file")

// Link-layer header types (LINKTYPE_*).
const (
	LinkNull       = 0
	LinkEthernet   = 1
	LinkRaw        = 101
	LinkLoop       = 108
	LinkSLL        = 113
	LinkIPv4       = 228
	LinkRTACSerial = 250 // SEL RTAC serial line (Wireshark "rtacser")
)

// Packet is one link-layer frame of a capture.
type Packet struct {
	Num  int // 1-based frame number, as Wireshark counts
	Link uint32
	Time time.Time
	Data []byte
}

// ReadFile reads a capture file from disk, decompressing it when it is
// gzip-compressed.
func ReadFile(path string) ([]byte, error) {
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	b, err := root.ReadFile(filepath.Base(path))
	if err != nil {
		return nil, err
	}
	return Gunzip(b)
}

// maxGunzip bounds the size of a decompressed capture.
const maxGunzip = 256 << 20

// Gunzip returns b decompressed when it starts with the gzip magic, and b
// itself otherwise.
func Gunzip(b []byte) ([]byte, error) {
	if len(b) < 2 || b[0] != 0x1f || b[1] != 0x8b {
		return b, nil
	}
	r, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrFormat, err)
	}
	out, err := io.ReadAll(io.LimitReader(r, maxGunzip+1))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrFormat, err)
	}
	if len(out) > maxGunzip {
		return nil, fmt.Errorf("%w: more than %d octets decompressed", ErrFormat, maxGunzip)
	}
	return out, nil
}

// Packets returns the frames of a pcap or pcapng file held in memory,
// decompressing it first when it is gzip-compressed.
func Packets(b []byte) ([]Packet, error) {
	b, err := Gunzip(b)
	if err != nil {
		return nil, err
	}
	if len(b) < 4 {
		return nil, ErrFormat
	}
	var out []Packet
	switch binary.LittleEndian.Uint32(b) {
	case 0xa1b2c3d4:
		out, err = pcapPackets(b, binary.LittleEndian, time.Microsecond)
	case 0xa1b23c4d:
		out, err = pcapPackets(b, binary.LittleEndian, time.Nanosecond)
	case 0xd4c3b2a1:
		out, err = pcapPackets(b, binary.BigEndian, time.Microsecond)
	case 0x4d3cb2a1:
		out, err = pcapPackets(b, binary.BigEndian, time.Nanosecond)
	case 0x0a0d0d0a:
		out, err = pcapngPackets(b)
	default:
		return nil, ErrFormat
	}
	for i := range out {
		out[i].Num = i + 1
	}
	return out, err
}

func pcapPackets(b []byte, bo binary.ByteOrder, unit time.Duration) ([]Packet, error) {
	if len(b) < 24 {
		return nil, ErrFormat
	}
	link := bo.Uint32(b[20:24]) & 0x0fffffff
	var out []Packet
	for off := 24; off < len(b); {
		if len(b)-off < 16 {
			return nil, ErrFormat
		}
		sec, frac := int64(bo.Uint32(b[off:])), int64(bo.Uint32(b[off+4:]))
		n := int(bo.Uint32(b[off+8 : off+12]))
		off += 16
		if n > len(b)-off {
			return nil, ErrFormat
		}
		ts := time.Unix(sec, frac*int64(unit)).UTC()
		out = append(out, Packet{Link: link, Time: ts, Data: b[off : off+n]})
		off += n
	}
	return out, nil
}

// iface is a pcapng interface: its link type and time stamp unit.
type iface struct {
	link uint32
	unit time.Duration
}

func pcapngPackets(b []byte) ([]Packet, error) {
	var (
		bo    binary.ByteOrder = binary.LittleEndian
		ifcs  []iface
		out   []Packet
		block int
	)
	for off := 0; off < len(b); off += block {
		if len(b)-off < 12 {
			return nil, ErrFormat
		}
		typ := bo.Uint32(b[off:])
		if typ == 0x0a0d0d0a { // section header: byte order from its magic
			switch binary.LittleEndian.Uint32(b[off+8:]) {
			case 0x1a2b3c4d:
				bo = binary.LittleEndian
			case 0x4d3c2b1a:
				bo = binary.BigEndian
			default:
				return nil, ErrFormat
			}
			ifcs = ifcs[:0]
		}
		block = int(bo.Uint32(b[off+4:]))
		if block < 12 || block%4 != 0 || block > len(b)-off {
			return nil, ErrFormat
		}
		body := b[off+8 : off+block-4]
		switch typ {
		case 1: // interface description
			if len(body) < 8 {
				return nil, ErrFormat
			}
			ifcs = append(ifcs, iface{link: uint32(bo.Uint16(body)), unit: tsresol(body[8:], bo)})
		case 6: // enhanced packet
			if len(body) < 20 {
				return nil, ErrFormat
			}
			ifc := int(bo.Uint32(body))
			capLen := int(bo.Uint32(body[12:]))
			if ifc >= len(ifcs) || capLen > len(body)-20 {
				return nil, ErrFormat
			}
			ts := uint64(bo.Uint32(body[4:]))<<32 | uint64(bo.Uint32(body[8:]))
			out = append(out, Packet{Link: ifcs[ifc].link, Time: stamp(ts, ifcs[ifc].unit), Data: body[20 : 20+capLen]})
		case 3: // simple packet, interface 0, no time stamp
			if len(body) < 4 || len(ifcs) == 0 {
				return nil, ErrFormat
			}
			capLen := min(int(bo.Uint32(body)), len(body)-4)
			out = append(out, Packet{Link: ifcs[0].link, Data: body[4 : 4+capLen]})
		}
	}
	return out, nil
}

// tsresol returns the time stamp unit from the options of an interface
// description block: microseconds unless if_tsresol says otherwise.
func tsresol(opts []byte, bo binary.ByteOrder) time.Duration {
	for len(opts) >= 4 {
		code, n := bo.Uint16(opts), int(bo.Uint16(opts[2:]))
		if code == 0 || n > len(opts)-4 {
			break
		}
		if code == 9 && n == 1 {
			r := opts[4]
			if r&0x80 == 0 && r <= 9 {
				u := time.Second
				for range r {
					u /= 10
				}
				return u
			}
		}
		opts = opts[4+(n+3)&^3:]
	}
	return time.Microsecond
}

// stamp converts a pcapng time stamp in units of unit (a power of ten of
// seconds, at least a nanosecond) to a time. A stamp past 2^63 units is
// not a time any recording has and gives the zero time.
func stamp(ts uint64, unit time.Duration) time.Time {
	if ts > math.MaxInt64 {
		return time.Time{}
	}
	t := int64(ts)
	per := int64(time.Second / unit)
	return time.Unix(t/per, t%per*int64(unit)).UTC()
}

// Span returns the time stamps of the first and the last packet.
func Span(ps []Packet) (first, last time.Time) {
	if len(ps) == 0 {
		return first, last
	}
	return ps[0].Time, ps[len(ps)-1].Time
}
