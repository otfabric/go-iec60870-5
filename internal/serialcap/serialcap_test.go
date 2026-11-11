// SPDX-License-Identifier: MIT

package serialcap

import (
	"encoding/binary"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/otfabric/go-iec60870-5/ft12"
	"github.com/otfabric/go-iec60870-5/internal/pcapio"
	"github.com/otfabric/go-iec60870-5/link"
)

var p1 = ft12.Params{AddrSize: 1}

func fixed(c link.Control, addr uint16) []byte {
	b, _ := ft12.Frame{Kind: ft12.KindFixed, Control: byte(c), Addr: addr}.Encode(p1)
	return b
}

func variable(c link.Control, addr uint16, data ...byte) []byte {
	b, _ := ft12.Frame{Kind: ft12.KindVariable, Control: byte(c), Addr: addr, Data: data}.Encode(p1)
	return b
}

func pri(fn byte, fcb bool) link.Control {
	return link.PrimaryControl(fn, fcb, fn == 2 || fn == 3 || fn == 10 || fn == 11, false)
}

func sec(fn byte) link.Control { return link.SecondaryControl(fn, false, false, false) }

// line builds a line from frames: dir and octets, one capture frame each.
func line(frames ...any) Line {
	var runs [2]pcapio.Run
	for i := 0; i < len(frames); i += 2 {
		d := frames[i].(int)
		runs[d].Add(i/2+1, time.Unix(int64(i), 0), frames[i+1].([]byte))
	}
	return Split([2][]pcapio.Run{{runs[0]}, {runs[1]}}, p1)
}

func TestSplit(t *testing.T) {
	var r pcapio.Run
	r.Add(1, time.Time{}, []byte{0x01, 0x02}) // noise
	r.Add(2, time.Time{}, fixed(pri(9, false), 1))
	bad := fixed(pri(9, false), 1)
	bad[3]++ // checksum
	r.Add(3, time.Time{}, bad)
	r.Add(4, time.Time{}, []byte{0xE5})
	r.Add(5, time.Time{}, variable(pri(3, true), 1, 1, 2, 3)[:6]) // cut
	l := Split([2][]pcapio.Run{{r}, nil}, p1)
	if len(l.Frames) != 2 || l.Frames[0].Last != 2 || l.Frames[1].Kind != ft12.KindSingle {
		t.Fatalf("frames %+v", l.Frames)
	}
	if len(l.Bad) != 3 || !errors.Is(l.Bad[0].Err, ErrNoise) || !errors.Is(l.Bad[1].Err, ft12.ErrChecksum) || !errors.Is(l.Bad[2].Err, ErrCut) {
		t.Fatalf("bad %v", l.Bad)
	}
	if !strings.Contains(l.Bad[1].String(), "frames 3..3: 5 octets") {
		t.Errorf("%s", l.Bad[1])
	}
	if !l.Frames[0].Exact(p1) || l.Frames[0].Exact(ft12.Params{AddrSize: 2}) {
		t.Error("exact")
	}
	v := line(0, variable(pri(3, true), 7, 9, 8))
	if f := v.Frames[0]; string(f.Data) != "\x09\x08" || f.Addr != 7 || f.Control() != pri(3, true) {
		t.Errorf("variable %+v", f)
	}
}

func TestAnalyzeExchanges(t *testing.T) {
	l := line(
		1, fixed(sec(9), 1), // before the first request
		0, fixed(pri(0, false), 1), // reset
		1, []byte{0xE5},
		0, fixed(pri(11, true), 1), // first after reset: FCB 1
		1, fixed(sec(9), 1),
		0, fixed(pri(11, false), 1),
		// not answered, repeated
		0, fixed(pri(11, false), 1),
		1, variable(sec(8), 1, 1, 2),
		// answered, repeated all the same
		0, fixed(pri(11, false), 1),
		1, fixed(sec(9), 1),
		0, fixed(pri(10, true), 1),
		// abandoned for a status request
		0, fixed(pri(9, false), 1),
		1, fixed(sec(11), 1),
		0, variable(pri(4, false), 255, 1), // send/no reply
		0, fixed(pri(10, false), 2),
		1, fixed(sec(15), 2), // not implemented answers anything
	)
	a := Analyze(l)
	if len(a.Violations) != 0 {
		t.Fatalf("violations %v", a.Violations)
	}
	if a.Leading != 1 || a.Exchanges != 6 || a.Repeated != 2 || a.RepeatedAnswered != 1 || len(a.Abandoned) != 1 || a.NoReply != 1 || a.FCBToggled != 2 {
		t.Errorf("%+v", a)
	}
	if a.Balanced || a.Controlling() != 0 || a.Primary != [2]int{9, 0} || a.Single != [2]int{0, 1} {
		t.Errorf("%+v", a)
	}
	if got := Funcs(a.PrimaryFunc); !slices.Equal(got, []byte{0, 4, 9, 10, 11}) {
		t.Errorf("funcs %v", got)
	}
}

func TestAnalyzeViolations(t *testing.T) {
	l := line(
		1, fixed(pri(9, false), 1), // the controlling station is direction 1
		0, fixed(sec(11), 1),
		1, fixed(pri(0, false), 1),
		0, fixed(sec(0), 1),
		1, fixed(pri(11, false), 1), // FCB 0 after the reset
		0, fixed(sec(9), 2), // other link address
		1, fixed(pri(11, false), 1), // FCB not toggled, other octets below
		0, fixed(sec(9), 1),
		1, fixed(link.PrimaryControl(3, true, false, false), 1), // FCV missing
		0, fixed(sec(9), 1), // no data does not answer user data
		0, fixed(sec(0), 1), // nothing outstanding
		1, fixed(pri(11, true), 1),
		0, fixed(sec(9), 1),
		1, fixed(pri(11, true), 1),
	)
	l.Frames[len(l.Frames)-1].Raw = append([]byte{}, 0) // same FCB, other octets
	a := Analyze(l)
	want := []string{"FCB 0 in the first", "from link address 2", "FCV false", "does not answer", "without an outstanding", "FCB not toggled"}
	if len(a.Violations) != len(want) {
		t.Fatalf("violations %v", a.Violations)
	}
	for i, w := range want {
		if !strings.Contains(a.Violations[i].String(), w) {
			t.Errorf("violation %d: %s, want %q", i, a.Violations[i], w)
		}
	}
	if a.Controlling() != 1 {
		t.Error("controlling direction")
	}
	// Balanced: both directions send primary messages.
	b := Analyze(line(0, fixed(pri(2, true), 1), 1, fixed(sec(0), 1), 1, fixed(pri(2, true), 1), 0, []byte{0xE5}))
	if !b.Balanced || b.Exchanges != 2 || len(b.Violations) != 0 {
		t.Errorf("balanced %+v", b)
	}
	// On a shared line the response to another station comes in the
	// direction of the request.
	o := Analyze(line(0, fixed(pri(11, true), 9), 0, []byte{0xE5}, 0, fixed(pri(11, true), 1), 1, fixed(sec(9), 1)))
	if o.Overheard != 1 || o.Exchanges != 2 || len(o.Violations) != 0 {
		t.Errorf("overheard %+v", o)
	}
}

func rtacPcap(frames ...[]byte) []byte {
	b := binary.LittleEndian.AppendUint32(nil, 0xa1b2c3d4)
	b = binary.LittleEndian.AppendUint16(b, 2)
	b = binary.LittleEndian.AppendUint16(b, 4)
	b = append(b, make([]byte, 8)...)
	b = binary.LittleEndian.AppendUint32(b, 65535)
	b = binary.LittleEndian.AppendUint32(b, pcapio.LinkRTACSerial)
	for _, f := range frames {
		b = binary.LittleEndian.AppendUint32(b, 1)
		b = binary.LittleEndian.AppendUint32(b, 0)
		b = binary.LittleEndian.AppendUint32(b, uint32(len(f)))
		b = binary.LittleEndian.AppendUint32(b, uint32(len(f)))
		b = append(b, f...)
	}
	return b
}

func TestRead(t *testing.T) {
	hdr := func(ev byte) []byte { return []byte{0, 0, 0, 1, 0, 0, 0, 0, ev, 0, 0, 0} }
	b := rtacPcap(append(hdr(pcapio.RTACRxStart), fixed(pri(9, false), 1)...), append(hdr(pcapio.RTACTxStart), fixed(sec(11), 1)...))
	recs, err := Read(b, p1)
	if err != nil || len(recs) != 1 || recs[0].Name != "rtac" || len(recs[0].Frames) != 2 || recs[0].Frames[1].Dir != 1 {
		t.Fatalf("%+v %v", recs, err)
	}
	if _, err := Read(rtacPcap(append(hdr(0), 1)), p1); err == nil {
		t.Error("data in a status event accepted")
	}
	if _, err := Read([]byte("no capture"), p1); err == nil {
		t.Error("not a capture accepted")
	}
	// A TCP stream: one IPv4 packet to the server port.
	ip := make([]byte, 40)
	ip[0], ip[9] = 0x45, 6
	payload := fixed(pri(9, false), 1)
	binary.BigEndian.PutUint16(ip[2:], uint16(40+len(payload)))
	copy(ip[12:], []byte{10, 0, 0, 1, 10, 0, 0, 2})
	binary.BigEndian.PutUint16(ip[20:], 40000)
	binary.BigEndian.PutUint16(ip[22:], 2101)
	ip[32], ip[33] = 0x50, 0x18
	raw := binary.LittleEndian.AppendUint32(nil, 0xa1b2c3d4)
	raw = binary.LittleEndian.AppendUint16(raw, 2)
	raw = binary.LittleEndian.AppendUint16(raw, 4)
	raw = append(raw, make([]byte, 8)...)
	raw = binary.LittleEndian.AppendUint32(raw, 65535)
	raw = binary.LittleEndian.AppendUint32(raw, pcapio.LinkRaw)
	pkt := append(ip, payload...)
	raw = binary.LittleEndian.AppendUint32(raw, 1)
	raw = binary.LittleEndian.AppendUint32(raw, 0)
	raw = binary.LittleEndian.AppendUint32(raw, uint32(len(pkt)))
	raw = binary.LittleEndian.AppendUint32(raw, uint32(len(pkt)))
	raw = append(raw, pkt...)
	recs, err = Read(raw, p1, 2101)
	if err != nil || len(recs) != 1 || recs[0].Conn == nil || recs[0].Name != "10.0.0.1:40000 -> 10.0.0.2:2101" || len(recs[0].Frames) != 1 {
		t.Fatalf("tcp %+v %v", recs, err)
	}
	trunc := append([]byte(nil), raw...)
	binary.BigEndian.PutUint16(trunc[24+16+2:], 200)
	if _, err := Read(trunc, p1, 2101); err == nil {
		t.Error("truncated IPv4 accepted")
	}
	if _, err := ReadFile(t.TempDir()+"/missing.pcap", p1); err == nil {
		t.Error("missing file accepted")
	}
}
