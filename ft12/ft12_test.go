// SPDX-License-Identifier: MIT

package ft12

import (
	"bytes"
	"errors"
	"io"
	"testing"
	"testing/iotest"
)

// Frames from IEC 60870-5-101 traffic: request status of link (FC 9) to
// address 1, its fixed-length answer, and a variable frame carrying a
// C_IC_NA_1 general interrogation.
var (
	reqStatus = []byte{0x10, 0x49, 0x01, 0x4A, 0x16}
	gi        = []byte{0x68, 0x09, 0x09, 0x68, 0x73, 0x01, 0x64, 0x01, 0x06, 0x01, 0x00, 0x00, 0x14, 0xF4, 0x16}
)

func TestKnownFrames(t *testing.T) {
	p := Params{AddrSize: 1}
	f, n, err := Decode(reqStatus, p)
	if err != nil || n != 5 || f.Kind != KindFixed || f.Control != 0x49 || f.Addr != 1 {
		t.Fatalf("fixed: %+v %d %v", f, n, err)
	}
	f, n, err = Decode(gi, p)
	if err != nil || n != len(gi) || f.Kind != KindVariable || f.Control != 0x73 || f.Addr != 1 ||
		!bytes.Equal(f.Data, gi[6:13]) {
		t.Fatalf("variable: %+v %d %v", f, n, err)
	}
	if b, _ := f.Encode(p); !bytes.Equal(b, gi) {
		t.Errorf("re-encode % X", b)
	}
	if f, n, err := Decode([]byte{0xE5, 0x10}, p); err != nil || n != 1 || f.Kind != KindSingle {
		t.Errorf("single: %v %d %v", f, n, err)
	}
}

func TestRoundTrip(t *testing.T) {
	for size := 0; size <= 2; size++ {
		p := Params{AddrSize: size}
		data := make([]byte, p.MaxData())
		for i := range data {
			data[i] = byte(i * 7)
		}
		for _, f := range []Frame{
			{Kind: KindSingle},
			{Kind: KindFixed, Control: 0x40, Addr: p.MaxAddr()},
			{Kind: KindVariable, Control: 0x53, Addr: p.MaxAddr() / 2, Data: data},
			{Kind: KindVariable, Control: 0x08, Data: []byte{}},
		} {
			b, err := f.Encode(p)
			if err != nil || len(b) != f.Size(p) {
				t.Fatalf("size %d %v: % X %v", size, f, b, err)
			}
			g, n, err := Decode(b, p)
			if err != nil || n != len(b) || g.Kind != f.Kind || g.Control != f.Control || g.Addr != f.Addr || !bytes.Equal(g.Data, f.Data) {
				t.Errorf("size %d %v -> %v %d %v", size, f, g, n, err)
			}
			// Every proper prefix is short, never malformed.
			for i := range b {
				if _, _, err := Decode(b[:i], p); !errors.Is(err, ErrShort) {
					t.Fatalf("prefix %d of %v: %v", i, f, err)
				}
			}
		}
	}
}

func TestEncodeErrors(t *testing.T) {
	p := Params{AddrSize: 1}
	for _, tc := range []struct {
		f    Frame
		p    Params
		want error
	}{
		{Frame{Kind: KindFixed}, Params{AddrSize: 3}, ErrInvalidParams},
		{Frame{Kind: 9}, p, ErrMalformed},
		{Frame{Kind: KindFixed, Addr: 256}, p, ErrInvalidAddress},
		{Frame{Kind: KindVariable, Data: make([]byte, 254)}, p, ErrTooLong},
		{Frame{Kind: KindFixed, Addr: 1}, Params{}, ErrInvalidAddress},
	} {
		if _, err := tc.f.Encode(tc.p); !errors.Is(err, tc.want) {
			t.Errorf("%v: %v, want %v", tc.f, err, tc.want)
		}
	}
	if _, _, err := Decode(gi, Params{AddrSize: -1}); !errors.Is(err, ErrInvalidParams) {
		t.Error(err)
	}
}

func TestDecodeErrors(t *testing.T) {
	p := Params{AddrSize: 1}
	bad := func(b []byte, want error) {
		t.Helper()
		if _, _, err := Decode(b, p); !errors.Is(err, want) {
			t.Errorf("% X: %v, want %v", b, err, want)
		}
	}
	bad([]byte{0x00}, ErrMalformed)
	bad([]byte{0x10, 0x49, 0x01, 0x4B, 0x16}, ErrChecksum)
	bad([]byte{0x10, 0x49, 0x01, 0x4A, 0x17}, ErrMalformed)
	bad([]byte{0x68, 0x09, 0x08, 0x68}, ErrMalformed)
	bad([]byte{0x68, 0x09, 0x09, 0x69}, ErrMalformed)
	bad([]byte{0x68, 0x01, 0x01, 0x68}, ErrMalformed) // L below C + A
	g := bytes.Clone(gi)
	g[8] ^= 0x01
	bad(g, ErrChecksum)
}

func TestStrings(t *testing.T) {
	for _, s := range []string{
		KindSingle.String(), KindFixed.String(), KindVariable.String(), Kind(7).String(),
		Frame{Kind: KindSingle}.String(), Frame{Kind: KindFixed}.String(),
		Frame{Kind: KindVariable}.String(), Frame{}.String(),
	} {
		if s == "" {
			t.Error("empty string")
		}
	}
}

func TestDecoderResync(t *testing.T) {
	p := Params{AddrSize: 1}
	corrupt := bytes.Clone(gi)
	corrupt[9] ^= 0xFF
	var stream []byte
	stream = append(stream, 0x00, 0xFF) // line noise
	stream = append(stream, reqStatus...)
	stream = append(stream, corrupt...) // checksum error
	stream = append(stream, 0x68, 0x05) // truncated header
	stream = append(stream, 0xE5)
	stream = append(stream, gi...)
	stream = append(stream, gi[:5]...) // frame cut by EOF

	var discarded int
	d := NewDecoder(iotest.OneByteReader(bytes.NewReader(stream)), p)
	d.OnDiscard = func(b []byte, err error) { discarded += len(b) }
	var kinds []Kind
	for {
		f, err := d.Next()
		if err != nil {
			if !errors.Is(err, io.ErrUnexpectedEOF) {
				t.Fatalf("end: %v", err)
			}
			break
		}
		kinds = append(kinds, f.Kind)
		if f.Kind == KindVariable && !bytes.Equal(f.Data, gi[6:13]) {
			t.Errorf("data % X", f.Data)
		}
	}
	// The truncated header 68 05 is followed by E5, 68 09 09 68...: the
	// decoder must not lose the single character or the frame after it.
	want := []Kind{KindFixed, KindSingle, KindVariable}
	if len(kinds) != len(want) {
		t.Fatalf("frames %v, want %v", kinds, want)
	}
	if discarded == 0 || d.Buffered() != 5 {
		t.Errorf("discarded %d buffered %d", discarded, d.Buffered())
	}
	d.Reset()
	if d.Buffered() != 0 {
		t.Error("reset")
	}
}

func TestDecoderReadErrors(t *testing.T) {
	p := Params{AddrSize: 1}
	if _, err := NewDecoder(bytes.NewReader(nil), p).Next(); err != io.EOF {
		t.Errorf("empty: %v", err)
	}
	boom := errors.New("boom")
	if _, err := NewDecoder(iotest.ErrReader(boom), p).Next(); err != boom {
		t.Errorf("err: %v", err)
	}
	if _, err := NewDecoder(zeroReader{}, p).Next(); err != io.ErrNoProgress {
		t.Errorf("no progress: %v", err)
	}
	defer func() {
		if recover() == nil {
			t.Error("no panic on bad params")
		}
	}()
	NewDecoder(zeroReader{}, Params{AddrSize: 5})
}

type zeroReader struct{}

func (zeroReader) Read([]byte) (int, error) { return 0, nil }

func FuzzDecode(f *testing.F) {
	f.Add(reqStatus, 1)
	f.Add(gi, 1)
	f.Add([]byte{0xE5}, 0)
	f.Add([]byte{0x68, 0x03, 0x03, 0x68, 0x08, 0x01, 0x00, 0x09, 0x16}, 2)
	f.Fuzz(func(t *testing.T, b []byte, size int) {
		p := Params{AddrSize: (size%3 + 3) % 3}
		fr, n, err := Decode(b, p)
		if err == nil {
			if n > len(b) {
				t.Fatalf("n %d > %d", n, len(b))
			}
			enc, err := fr.Encode(p)
			if err != nil || !bytes.Equal(enc, b[:n]) {
				t.Fatalf("re-encode % X != % X (%v)", enc, b[:n], err)
			}
		}
		d := NewDecoder(bytes.NewReader(b), p)
		for i := 0; i < len(b)+1; i++ {
			if _, err := d.Next(); err != nil {
				break
			}
		}
	})
}
