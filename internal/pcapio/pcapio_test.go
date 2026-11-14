// SPDX-License-Identifier: MIT

package pcapio

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// seg is one TCP segment between 10.0.0.1:40000 (client) and 10.0.0.2:port.
type seg struct {
	toServer bool
	flags    byte
	seq      uint32
	payload  []byte
}

const (
	finFlag = 0x01
	synFlag = 0x02
	rstFlag = 0x04
	ackFlag = 0x10
	port    = 2404
)

func ipTCP(s seg) []byte {
	src, dst := []byte{10, 0, 0, 1}, []byte{10, 0, 0, 2}
	sport, dport := uint16(40000), uint16(port)
	if !s.toServer {
		src, dst, sport, dport = dst, src, dport, sport
	}
	tcp := make([]byte, 20)
	binary.BigEndian.PutUint16(tcp[0:], sport)
	binary.BigEndian.PutUint16(tcp[2:], dport)
	binary.BigEndian.PutUint32(tcp[4:], s.seq)
	tcp[12] = 5 << 4
	flags := s.flags
	if flags == 0 {
		flags = ackFlag | 0x08
	}
	tcp[13] = flags
	tcp = append(tcp, s.payload...)
	ip := make([]byte, 20)
	ip[0] = 0x45
	binary.BigEndian.PutUint16(ip[2:], uint16(20+len(tcp)))
	ip[9] = protoTCP
	copy(ip[12:], src)
	copy(ip[16:], dst)
	return append(ip, tcp...)
}

func ether(ip []byte) []byte {
	return append([]byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 0x08, 0x00}, ip...)
}

// pcapFile builds a little-endian microsecond pcap file.
func pcapFile(link uint32, frames ...[]byte) []byte {
	b := binary.LittleEndian.AppendUint32(nil, 0xa1b2c3d4)
	b = binary.LittleEndian.AppendUint16(b, 2)
	b = binary.LittleEndian.AppendUint16(b, 4)
	b = append(b, make([]byte, 8)...)
	b = binary.LittleEndian.AppendUint32(b, 65535)
	b = binary.LittleEndian.AppendUint32(b, link)
	for i, f := range frames {
		b = binary.LittleEndian.AppendUint32(b, uint32(1700000000+i))
		b = binary.LittleEndian.AppendUint32(b, 0)
		b = binary.LittleEndian.AppendUint32(b, uint32(len(f)))
		b = binary.LittleEndian.AppendUint32(b, uint32(len(f)))
		b = append(b, f...)
	}
	return b
}

func ethPcap(segs ...seg) []byte {
	frames := make([][]byte, len(segs))
	for i, s := range segs {
		frames[i] = ether(ipTCP(s))
	}
	return pcapFile(LinkEthernet, frames...)
}

func conns(t *testing.T, b []byte) []Conn {
	t.Helper()
	ps, err := Packets(b)
	if err != nil {
		t.Fatal(err)
	}
	cs, err := TCP(ps, port)
	if err != nil {
		t.Fatal(err)
	}
	return cs
}

func data(d Direction) string {
	var s []byte
	for _, r := range d.Runs {
		s = append(s, r.Data...)
		if r.Gap {
			s = append(s, '|')
		}
	}
	return string(s)
}

func TestTCPReassembly(t *testing.T) {
	segs := []seg{
		{toServer: true, flags: synFlag, seq: 99},
		{flags: synFlag | ackFlag, seq: 499},
		{toServer: true, seq: 100, payload: []byte("abc")},
		{toServer: true, seq: 106, payload: []byte("ghi")}, // out of order
		{toServer: true, seq: 103, payload: []byte("def")},
		{toServer: true, seq: 103, payload: []byte("dXf")}, // retransmission with a conflict
		{toServer: true, seq: 108, payload: []byte("ijk")}, // overlaps
		{seq: 500, payload: []byte("123")},
		{seq: 510, payload: []byte("456")}, // gap
		{toServer: true, flags: finFlag | ackFlag, seq: 111},
	}
	cs := conns(t, ethPcap(segs...))
	if len(cs) != 1 {
		t.Fatalf("%d connections", len(cs))
	}
	c := cs[0]
	if c.Client != "10.0.0.1:40000" || c.Server != "10.0.0.2:2404" || !c.Opened || !c.Closed || c.Reset || c.FirstFrame != 1 {
		t.Errorf("conn %+v", c)
	}
	if got := data(c.Up); got != "abcdefghijk" {
		t.Errorf("up %q", got)
	}
	if got := data(c.Down); got != "123|456" {
		t.Errorf("down %q", got)
	}
	if c.Up.OutOfOrder != 2 || c.Up.Retransmitted != 1 || c.Up.Conflicts != 1 || c.Down.Gaps != 1 {
		t.Errorf("stats up %+v down %+v", c.Up.Stats, c.Down.Stats)
	}
	first, last := c.Up.Runs[0].Span(2, 7)
	if first.Frame != 3 || last.Frame != 5 {
		t.Errorf("span %+v %+v", first, last)
	}
}

func TestTCPConnections(t *testing.T) {
	segs := []seg{
		{toServer: true, flags: synFlag, seq: 1},
		{toServer: true, seq: 2, payload: []byte("a")},
		{toServer: true, flags: rstFlag, seq: 3},
		// The same ports again, from a SYN.
		{toServer: true, flags: synFlag, seq: 100},
		{toServer: true, seq: 101, payload: []byte("b")},
		// The same ports again, without a SYN: the sequence number jumps.
		{toServer: true, seq: 2000000000, payload: []byte("c")},
		// No payload and no SYN: not a connection.
	}
	cs := conns(t, ethPcap(segs...))
	if len(cs) != 3 || !cs[0].Reset || data(cs[1].Up) != "b" || data(cs[2].Up) != "c" || cs[2].Opened {
		t.Fatalf("%+v", cs)
	}
	// Another port is ignored; an empty connection is left out.
	other := ethPcap(seg{toServer: true, seq: 5})
	if cs := conns(t, other); len(cs) != 0 {
		t.Errorf("%+v", cs)
	}
}

func TestIPv4(t *testing.T) {
	ip := ipTCP(seg{toServer: true, seq: 1, payload: []byte("x")})
	for name, p := range map[string]Packet{
		"null": {Link: LinkNull, Data: append([]byte{2, 0, 0, 0}, ip...)},
		"loop": {Link: LinkLoop, Data: append([]byte{0, 0, 0, 2}, ip...)},
		"sll":  {Link: LinkSLL, Data: append(append(make([]byte, 14), 0x08, 0x00), ip...)},
		"vlan": {Link: LinkEthernet, Data: append([]byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 0x81, 0x00, 0, 5, 0x08, 0x00}, ip...)},
		"raw":  {Link: LinkRaw, Data: ip},
		"ipv4": {Link: LinkIPv4, Data: ip},
	} {
		if _, ok, err := IPv4(p); !ok || err != nil {
			t.Errorf("%s: %v %v", name, ok, err)
		}
	}
	for name, p := range map[string]Packet{
		"short ether": {Link: LinkEthernet, Data: []byte{1}},
		"arp":         {Link: LinkEthernet, Data: append(make([]byte, 12), 0x08, 0x06)},
		"short vlan":  {Link: LinkEthernet, Data: append(make([]byte, 12), 0x81, 0x00)},
		"af inet6":    {Link: LinkNull, Data: append([]byte{30, 0, 0, 0}, ip...)},
		"sll ipv6":    {Link: LinkSLL, Data: append(append(make([]byte, 14), 0x86, 0xdd), ip...)},
		"rtac":        {Link: LinkRTACSerial, Data: make([]byte, 12)},
		"ipv6":        {Link: LinkRaw, Data: append([]byte{0x60}, make([]byte, 39)...)},
	} {
		if _, ok, err := IPv4(p); ok || err != nil {
			t.Errorf("%s: %v %v", name, ok, err)
		}
	}
	if _, _, err := IPv4(Packet{Link: 147}); err == nil {
		t.Error("unsupported link type accepted")
	}
	// IPv4 errors and segments that are not TCP to the port.
	trunc := append([]byte(nil), ip...)
	binary.BigEndian.PutUint16(trunc[2:], uint16(len(ip)+10))
	frag := append([]byte(nil), ip...)
	frag[6] = 0x20
	for name, b := range map[string][]byte{"truncated": trunc, "fragmented": frag} {
		if _, err := TCP([]Packet{{Num: 1, Link: LinkRaw, Data: b}}, port); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	udp := append([]byte(nil), ip...)
	udp[9] = 17
	short := append([]byte(nil), ip[:30]...)
	binary.BigEndian.PutUint16(short[2:], 30)
	badHL := append([]byte(nil), ip...)
	badHL[32] = 0x10
	for name, b := range map[string][]byte{"udp": udp, "short tcp": short, "tcp header length": badHL} {
		if cs, err := TCP([]Packet{{Num: 1, Link: LinkRaw, Data: b}}, port); err != nil || len(cs) != 0 {
			t.Errorf("%s: %v %v", name, cs, err)
		}
	}
}

// pcapng builds a little-endian pcapng file with the given if_tsresol.
func pcapng(resol byte, frames [][]byte) []byte {
	block := func(typ uint32, body []byte) []byte {
		for len(body)%4 != 0 {
			body = append(body, 0)
		}
		n := uint32(12 + len(body))
		b := binary.LittleEndian.AppendUint32(nil, typ)
		b = binary.LittleEndian.AppendUint32(b, n)
		b = append(b, body...)
		return binary.LittleEndian.AppendUint32(b, n)
	}
	shb := binary.LittleEndian.AppendUint32(nil, 0x1a2b3c4d)
	shb = append(shb, 1, 0, 0, 0, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff)
	idb := []byte{1, 0, 0, 0, 0, 0, 1, 0}
	idb = append(idb, 2, 0, 3, 0, 'a', 'b', 'c', 0) // a comment option first
	idb = append(idb, 9, 0, 1, 0, resol, 0, 0, 0, 0, 0, 0, 0)
	out := append(block(0x0a0d0d0a, shb), block(1, idb)...)
	for i, f := range frames {
		ts := uint64(1700000000+i) * 1e9
		body := binary.LittleEndian.AppendUint32(nil, 0)
		body = binary.LittleEndian.AppendUint32(body, uint32(ts>>32))
		body = binary.LittleEndian.AppendUint32(body, uint32(ts))
		body = binary.LittleEndian.AppendUint32(body, uint32(len(f)))
		body = binary.LittleEndian.AppendUint32(body, uint32(len(f)))
		out = append(out, block(6, append(body, f...))...)
	}
	spb := binary.LittleEndian.AppendUint32(nil, 3)
	out = append(out, block(3, append(spb, 'x', 'y', 'z'))...)
	out = append(out, block(5, []byte{1, 2, 3, 4})...) // a block type that is skipped
	return out
}

func TestPackets(t *testing.T) {
	frames := [][]byte{{1}, {2, 2}}
	p, err := Packets(pcapng(9, frames))
	if err != nil || len(p) != 3 || p[1].Num != 2 || !bytes.Equal(p[1].Data, []byte{2, 2}) || p[0].Time.Unix() != 1700000000 {
		t.Fatalf("%+v %v", p, err)
	}
	if string(p[2].Data) != "xyz" || p[2].Link != 1 {
		t.Errorf("simple packet %+v", p[2])
	}
	// An if_tsresol this package does not take means microseconds.
	if p, _ := Packets(pcapng(0x83, frames)); p[0].Time.Unix() != 1700000000*1000 {
		t.Errorf("tsresol 0x83: %v", p[0].Time)
	}
	first, last := Span(p[:2])
	if !first.Before(last) {
		t.Errorf("span %v %v", first, last)
	}
	if f, l := Span(nil); !f.IsZero() || !l.IsZero() {
		t.Error("span of nothing")
	}
	if !stamp(1<<63, time.Nanosecond).IsZero() {
		t.Error("stamp past 2^63")
	}
	// The pcap variants: nanosecond and big-endian.
	le := pcapFile(LinkRaw, []byte{1, 2, 3})
	ns := append([]byte(nil), le...)
	binary.LittleEndian.PutUint32(ns, 0xa1b23c4d)
	be := append([]byte(nil), le...)
	binary.BigEndian.PutUint32(be, 0xa1b2c3d4)
	binary.BigEndian.PutUint32(be[20:], LinkRaw)
	binary.BigEndian.PutUint32(be[24:], 1700000000)
	binary.BigEndian.PutUint32(be[28:], 0)
	binary.BigEndian.PutUint32(be[32:], 3)
	binary.BigEndian.PutUint32(be[36:], 3)
	bens := append([]byte(nil), be...)
	binary.BigEndian.PutUint32(bens, 0xa1b23c4d)
	for name, b := range map[string][]byte{"le": le, "ns": ns, "be": be, "be ns": bens} {
		p, err := Packets(b)
		if err != nil || len(p) != 1 || p[0].Link != LinkRaw || len(p[0].Data) != 3 {
			t.Errorf("%s: %+v %v", name, p, err)
		}
	}
	var gz bytes.Buffer
	w := gzip.NewWriter(&gz)
	_, _ = w.Write(le)
	_ = w.Close()
	if p, err := Packets(gz.Bytes()); err != nil || len(p) != 1 {
		t.Errorf("gzip: %v", err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "x.pcap.gz"), gz.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	if b, err := ReadFile(filepath.Join(dir, "x.pcap.gz")); err != nil || !bytes.Equal(b, le) {
		t.Errorf("ReadFile: %v", err)
	}
	if _, err := ReadFile(filepath.Join(dir, "missing.pcap")); err == nil {
		t.Error("missing file accepted")
	}
	if _, err := ReadFile(filepath.Join(dir, "no-such-dir", "x.pcap")); err == nil {
		t.Error("missing directory accepted")
	}
}

func TestPacketsErrors(t *testing.T) {
	good := pcapFile(LinkRaw, []byte{1, 2, 3})
	ng := pcapng(9, [][]byte{{1}})
	badIfc := append([]byte(nil), ng...)
	// The enhanced packet block refers to interface 5.
	idx := bytes.Index(badIfc, []byte{6, 0, 0, 0})
	binary.LittleEndian.PutUint32(badIfc[idx+8:], 5)
	shortIDB := []byte{0x0a, 0x0d, 0x0d, 0x0a, 28, 0, 0, 0, 0x4d, 0x3c, 0x2b, 0x1a, 1, 0, 0, 0, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 28, 0, 0, 0,
		1, 0, 0, 0, 16, 0, 0, 0, 1, 0, 0, 0, 16, 0, 0, 0}
	shortEPB := append(append([]byte(nil), ng[:bytes.Index(ng, []byte{6, 0, 0, 0})]...), 6, 0, 0, 0, 16, 0, 0, 0, 0, 0, 0, 0, 16, 0, 0, 0)
	spbNoIfc := []byte{0x0a, 0x0d, 0x0d, 0x0a, 28, 0, 0, 0, 0x4d, 0x3c, 0x2b, 0x1a, 1, 0, 0, 0, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 28, 0, 0, 0,
		3, 0, 0, 0, 16, 0, 0, 0, 1, 0, 0, 0, 16, 0, 0, 0}
	var bigGzip bytes.Buffer
	w := gzip.NewWriter(&bigGzip)
	_, _ = w.Write(make([]byte, maxGunzip+1))
	_ = w.Close()
	for name, b := range map[string][]byte{
		"empty":         nil,
		"magic":         []byte("not a capture"),
		"short header":  good[:20],
		"short record":  good[:30],
		"cut record":    good[:len(good)-1],
		"bad gzip":      {0x1f, 0x8b, 0, 0},
		"cut gzip":      bigGzip.Bytes()[:40],
		"big gzip":      bigGzip.Bytes(),
		"pcapng block":  {0x0a, 0x0d, 0x0d, 0x0a, 8, 0, 0, 0, 0x4d, 0x3c, 0x2b, 0x1a},
		"pcapng magic":  {0x0a, 0x0d, 0x0d, 0x0a, 12, 0, 0, 0, 1, 2, 3, 4},
		"pcapng short":  ng[:10],
		"pcapng ifc":    badIfc,
		"pcapng idb":    shortIDB,
		"pcapng epb":    shortEPB,
		"pcapng spb":    spbNoIfc,
		"pcapng length": append(append([]byte(nil), ng...), 6, 0, 0, 0, 13, 0, 0, 0, 0, 0, 0, 0),
	} {
		if _, err := Packets(b); !errors.Is(err, ErrFormat) {
			t.Errorf("%s: err = %v, want ErrFormat", name, err)
		}
	}
	// A big-endian section.
	be := []byte{0x0a, 0x0d, 0x0d, 0x0a, 0, 0, 0, 28, 0x1a, 0x2b, 0x3c, 0x4d, 0, 1, 0, 0, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0, 0, 0, 28}
	if p, err := Packets(be); err != nil || len(p) != 0 {
		t.Errorf("big-endian section: %v %v", p, err)
	}
}

// Lengths of 2^31 and more, which do not fit a 32-bit int, are compared
// as unsigned values before they are used: the capture is refused, or for
// a simple packet the length is cut to the block.
func TestPacketsLengthsBeyondInt32(t *testing.T) {
	huge := uint32(0xfffffff0)
	record := pcapFile(LinkRaw, []byte{1, 2, 3})
	binary.LittleEndian.PutUint32(record[32:], huge)
	ng := pcapng(9, [][]byte{{1}})
	block := append([]byte(nil), ng...)
	binary.LittleEndian.PutUint32(block[4:], huge)
	epb := append([]byte(nil), ng...)
	binary.LittleEndian.PutUint32(epb[bytes.Index(epb, []byte{6, 0, 0, 0})+20:], huge)
	ifc := append([]byte(nil), ng...)
	binary.LittleEndian.PutUint32(ifc[bytes.Index(ifc, []byte{6, 0, 0, 0})+8:], huge)
	for name, b := range map[string][]byte{"pcap record": record, "pcapng block": block, "pcapng epb": epb, "pcapng interface": ifc} {
		if _, err := Packets(b); !errors.Is(err, ErrFormat) {
			t.Errorf("%s: err = %v, want ErrFormat", name, err)
		}
	}
	spb := append([]byte(nil), ng...)
	binary.LittleEndian.PutUint32(spb[len(spb)-28:], huge) // before the skipped block of 16 octets
	p, err := Packets(spb)
	if err != nil || len(p) != 2 || len(p[1].Data) != 4 {
		t.Fatalf("simple packet: %+v %v", p, err)
	}
}

func rtacFrame(ev byte, data ...byte) []byte {
	h := []byte{0, 0, 0, 1, 0, 0, 0, 2, ev, 0x3c, 0, 0}
	return append(h, data...)
}

func TestRTAC(t *testing.T) {
	file := pcapFile(LinkRTACSerial,
		rtacFrame(RTACStatusChange),
		rtacFrame(RTACRxStart, 0x10, 0x5b),
		rtacFrame(RTACRxStart, 0x01, 0x5c, 0x16),
		rtacFrame(RTACRxEnd),
		rtacFrame(RTACTxStart, 0xe5),
		rtacFrame(RTACTxEnd),
	)
	ps, err := Packets(file)
	if err != nil {
		t.Fatal(err)
	}
	l, err := RTAC(append(ps, Packet{Link: LinkEthernet}))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(l.RX.Data, []byte{0x10, 0x5b, 0x01, 0x5c, 0x16}) || !bytes.Equal(l.TX.Data, []byte{0xe5}) {
		t.Errorf("rx % x tx % x", l.RX.Data, l.TX.Data)
	}
	if len(l.RX.Marks) != 2 || l.RX.Marks[1].Off != 2 || l.RX.Marks[1].Frame != 3 || l.Events[RTACRxStart] != 2 {
		t.Errorf("marks %+v events %v", l.RX.Marks, l.Events)
	}
	if conns, err := TCP(ps, port); err != nil || len(conns) != 0 {
		t.Errorf("TCP over RTAC: %v %v", conns, err)
	}
	for name, p := range map[string]Packet{
		"short":         {Link: LinkRTACSerial, Data: []byte{1, 2, 3}},
		"data in event": {Link: LinkRTACSerial, Data: rtacFrame(RTACStatusChange, 1)},
	} {
		if _, err := RTAC([]Packet{p}); !errors.Is(err, ErrFormat) {
			t.Errorf("%s: %v", name, err)
		}
	}
}
