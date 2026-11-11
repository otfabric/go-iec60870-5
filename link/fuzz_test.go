// SPDX-License-Identifier: MIT

package link

import (
	"bytes"
	"context"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/otfabric/go-iec60870-5/ft12"
	"github.com/otfabric/go-iec60870-5/internal/testutil/serialline"
)

// The fuzz targets of the link procedures feed arbitrary sequences of
// frames to a Secondary and a Balanced station (straight into their
// receive path, one at a time) and arbitrary answers to a Primary, and
// check what must hold whatever the peer sends:
//
//   - no panic;
//   - a frame for another address, or a secondary frame, delivers nothing;
//   - before the first reset of remote link nothing is delivered;
//   - the repetition of an accepted send/confirm (same control field,
//     address and user data) is not delivered again;
//   - every frame the station sends is a well-formed secondary frame
//     (or E5h) with its own address, or for Balanced the peer's;
//   - a primary request returns within its time-outs.

// capture is a transport that keeps what the station writes and blocks
// reads until it is closed.
type capture struct {
	mu     sync.Mutex
	out    bytes.Buffer
	closed chan struct{}
	once   sync.Once
}

func newCapture() *capture { return &capture{closed: make(chan struct{})} }

func (c *capture) Read([]byte) (int, error) { <-c.closed; return 0, io.EOF }

func (c *capture) Write(b []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.out.Write(b)
}

func (c *capture) Close() error { c.once.Do(func() { close(c.closed) }); return nil }

func (c *capture) frames(t *testing.T, p ft12.Params) []ft12.Frame {
	c.mu.Lock()
	b := bytes.Clone(c.out.Bytes())
	c.mu.Unlock()
	var out []ft12.Frame
	for len(b) > 0 {
		f, n, err := ft12.Decode(b, p)
		if err != nil {
			t.Fatalf("station sent octets that are no frame: % X: %v", b, err)
		}
		out = append(out, f)
		b = b[n:]
	}
	return out
}

// fuzzFrames turns fuzz input into frames: per frame a control field, an
// address selector (0 own, 1 another, 2 broadcast, 3 the next octet), a
// length (0 fixed, FFh the single character, FEh the previous frame
// again, else variable with that many octets of user data).
func fuzzFrames(data []byte, p ft12.Params, own, other uint16) []ft12.Frame {
	var out []ft12.Frame
	for len(data) >= 3 {
		c, sel, n := data[0], data[1], int(data[2])
		data = data[3:]
		var addr uint16
		switch sel & 3 {
		case 0:
			addr = own
		case 1:
			addr = other
		case 2:
			addr = p.MaxAddr()
		default:
			if len(data) > 0 {
				addr = uint16(data[0]) & p.MaxAddr()
				data = data[1:]
			}
		}
		if p.AddrSize == 0 {
			addr = 0
		}
		switch n {
		case 0xFF:
			out = append(out, ft12.Frame{Kind: ft12.KindSingle})
		case 0xFE:
			if len(out) > 0 {
				out = append(out, out[len(out)-1])
			}
		case 0:
			out = append(out, ft12.Frame{Kind: ft12.KindFixed, Control: c, Addr: addr})
		default:
			n = min(n, p.MaxData(), len(data))
			d := []byte{0}
			if n > 0 {
				d = bytes.Clone(data[:n])
			}
			out = append(out, ft12.Frame{Kind: ft12.KindVariable, Control: c, Addr: addr, Data: d})
			data = data[n:]
		}
	}
	return out
}

// fuzzSeeds are frame sequences of a well-behaved primary: request status,
// reset, user data repeated, polls, then the same with flags flipped.
func fuzzSeeds(f *testing.F) {
	f.Add([]byte{0x49, 0, 0, 0x40, 0, 0, 0x73, 0, 3, 1, 2, 3, 0, 0, 0xFE, 0x5B, 0, 0, 0x7A, 0, 0}, uint8(0))
	f.Add([]byte{0x40, 0, 0, 0x53, 0, 2, 9, 9, 0x53, 0, 2, 9, 9, 0x73, 0, 2, 9, 9, 0x44, 2, 1, 7, 0x47, 0, 0}, uint8(1))
	f.Add([]byte{0x00, 0, 0, 0x0B, 1, 0, 0xE5, 0, 0xFF, 0x49, 3, 9, 0, 0x73, 0, 0xFF, 0x42, 0, 0, 0x4E, 0, 0}, uint8(2))
	f.Add([]byte{0xC9, 0, 0, 0xC0, 0, 0, 0xD3, 0, 1, 5, 0x00, 0, 0xFE, 0xF3, 0, 1, 6, 0xD2, 0, 0}, uint8(3))
}

func FuzzSecondary(f *testing.F) {
	fuzzSeeds(f)
	f.Fuzz(func(t *testing.T, data []byte, opts uint8) {
		p := ft12.Params{AddrSize: 1 + int(opts&1)}
		var h SecondaryHandler = &station{}
		count := h.(*station).received
		if opts&2 != 0 {
			st := &extStation{busyFlag: opts&4 != 0, refuse: map[byte]int{FuncResetUser: 1 << 30}}
			h, count = st, st.received
		}
		rw := newCapture()
		cfg := Config{FT12: p, SingleCharAck: opts&8 != 0}
		s, err := NewSecondary(rw, cfg, 3, h)
		if err != nil {
			t.Fatal(err)
		}
		reset := false
		var prev *ft12.Frame
		prevAccepted := false
		for _, fr := range fuzzFrames(data, p, 3, 4) {
			before := count()
			s.receive(fr)
			after := count()
			c := Control(fr.Control)
			own := fr.Kind != ft12.KindSingle && fr.Addr == 3
			switch {
			case fr.Kind == ft12.KindSingle || !c.PRM():
				if after != before {
					t.Fatalf("%v delivered user data", fr)
				}
			case !own && fr.Addr != p.MaxAddr():
				if after != before {
					t.Fatalf("%v for another station delivered user data", fr)
				}
			case !reset && after != before:
				t.Fatalf("%v delivered before the reset of remote link", fr)
			case own && prevAccepted && prev != nil && prev.Control == fr.Control && prev.Kind == fr.Kind &&
				bytes.Equal(prev.Data, fr.Data) && c.FCV() && c.Func() == FuncUserDataConf && after != before:
				t.Fatalf("repetition %v delivered again", fr)
			}
			if own && c.PRM() && (c.Func() == FuncResetLink || c.Func() == FuncResetFCB && opts&2 != 0) {
				reset = true
			}
			if own && c.PRM() {
				f := fr
				prev, prevAccepted = &f, after > before
			}
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		for _, fr := range rw.frames(t, p) {
			if fr.Kind != ft12.KindSingle && (Control(fr.Control).PRM() || fr.Addr != 3) {
				t.Fatalf("secondary sent %v", fr)
			}
		}
	})
}

func FuzzBalanced(f *testing.F) {
	fuzzSeeds(f)
	f.Fuzz(func(t *testing.T, data []byte, opts uint8) {
		p := ft12.Params{AddrSize: int(opts % 3)}
		h := &peer{}
		rw := newCapture()
		b, err := NewBalanced(rw, Config{FT12: p, SingleCharAck: opts&8 != 0, SenderAddr: opts&16 != 0},
			BalancedConfig{Addr: 3 & p.MaxAddr(), Remote: 1 & p.MaxAddr(), DIR: opts&32 != 0}, h)
		if err != nil {
			t.Fatal(err)
		}
		reset := false
		var prev *ft12.Frame
		prevAccepted := false
		for _, fr := range fuzzFrames(data, p, 3&p.MaxAddr(), 4&p.MaxAddr()) {
			before := h.n()
			b.receive(fr)
			after := h.n()
			c := Control(fr.Control)
			own := fr.Kind != ft12.KindSingle && (p.AddrSize == 0 || fr.Addr == 3)
			switch {
			case fr.Kind == ft12.KindSingle || !c.PRM() || !own:
				if after != before {
					t.Fatalf("%v delivered user data", fr)
				}
			case !reset && after != before:
				t.Fatalf("%v delivered before the reset of remote link", fr)
			case prevAccepted && prev != nil && prev.Control == fr.Control && prev.Kind == fr.Kind &&
				bytes.Equal(prev.Data, fr.Data) && c.FCV() && c.Func() == FuncUserDataConf && after != before:
				t.Fatalf("repetition %v delivered again", fr)
			}
			if own && c.PRM() && c.Func() == FuncResetLink {
				reset = true
			}
			if own && c.PRM() {
				f := fr
				prev, prevAccepted = &f, after > before
			}
		}
		if len(b.x.resp) > respQueue {
			t.Fatalf("%d responses queued", len(b.x.resp))
		}
		if err := b.Close(); err != nil {
			t.Fatal(err)
		}
		for _, fr := range rw.frames(t, p) {
			if fr.Kind != ft12.KindSingle && (Control(fr.Control).PRM() || p.AddrSize > 0 && fr.Addr != 3 && fr.Addr != 1) {
				t.Fatalf("balanced station answered with %v", fr)
			}
		}
	})
}

// FuzzPrimary plays a secondary that answers each frame of a primary with
// the next chunk of the input (a length octet, then that many raw octets),
// while the primary runs every kind of request.
func FuzzPrimary(f *testing.F) {
	ok := func(fn byte) []byte {
		b, _ := ft12.Frame{Kind: ft12.KindFixed, Control: byte(SecondaryControl(fn, false, false, false)), Addr: 3}.Encode(ft12.Params{AddrSize: 1})
		return append([]byte{byte(len(b))}, b...)
	}
	var seed []byte
	for _, fn := range []byte{FuncStatus, FuncAck, FuncAck, FuncAck, FuncNoData, FuncNoData, FuncStatus, FuncAck} {
		seed = append(seed, ok(fn)...)
	}
	f.Add(seed)
	f.Add([]byte{1, 0xE5, 5, 0x10, 0x0B, 0x03, 0x0E, 0x16, 3, 0x68, 0xFF, 0xFF})
	f.Fuzz(primaryAnswers)
}

func primaryAnswers(t *testing.T, data []byte) {
	p := ft12.Params{AddrSize: 1}
	a, b := serialline.Pair()
	var mu sync.Mutex
	go func() {
		d := ft12.NewDecoder(b, p)
		for {
			if _, err := d.Next(); err != nil {
				return
			}
			mu.Lock()
			var chunk []byte
			if len(data) > 0 {
				n := min(int(data[0]), len(data)-1)
				chunk, data = data[1:1+n], data[1+n:]
			}
			mu.Unlock()
			if len(chunk) > 0 {
				if _, err := b.Write(chunk); err != nil {
					return
				}
			}
		}
	}()
	cfg := Config{FT12: p, Timeout: 5 * time.Millisecond, Retries: 1}
	pr, err := NewPrimary(a, cfg)
	if err != nil {
		t.Fatal(err)
	}
	bound := 2 * time.Second // (Retries+1) x Timeout, and slack for the race detector
	ctx := context.Background()
	ops := []func() error{
		func() error { _, err := pr.RequestStatus(ctx, 3); return err },
		func() error { _, err := pr.ResetLink(ctx, 3); return err },
		func() error { _, err := pr.SendConfirm(ctx, 3, []byte{1, 2, 3}); return err },
		func() error {
			d, _, err := pr.RequestData(ctx, 3, Class1)
			if err != nil && d != nil {
				t.Fatalf("data %v with error %v", d, err)
			}
			return err
		},
		func() error { _, _, err := pr.RequestData(ctx, 3, Class2); return err },
		func() error { _, err := pr.ResetUser(ctx, 3); return err },
		func() error { _, err := pr.ResetFCB(ctx, 3); return err },
		func() error { return pr.SendNoReply(255, []byte{9}) },
		func() error { _, err := pr.Init(ctx, 3); return err },
	}
	for _, op := range ops {
		start := time.Now()
		_ = op()
		if d := time.Since(start); d > bound {
			t.Fatalf("request took %v", d)
		}
	}
	if err := pr.Close(); err != nil {
		t.Fatal(err)
	}
	_ = b.Close()
}
