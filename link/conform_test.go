// SPDX-License-Identifier: MIT

package link

import (
	"bytes"
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/otfabric/go-iec60870-5/ft12"
)

// wire is a scripted primary: it writes frames to a station under test
// and reads what the station answers.
type wire struct {
	t    *testing.T
	conn net.Conn
	d    *ft12.Decoder
	dir  bool
}

func newWire(t *testing.T, conn net.Conn) *wire {
	return &wire{t: t, conn: conn, d: ft12.NewDecoder(conn, fast.FT12)}
}

func (w *wire) send(fn byte, fcb bool, data []byte) {
	w.t.Helper()
	f := ft12.Frame{Kind: ft12.KindFixed, Addr: 1, Control: byte(PrimaryControl(fn, fcb, fcvRequired(fn), w.dir))}
	if data != nil {
		f.Kind, f.Data = ft12.KindVariable, data
	}
	b, err := f.Encode(fast.FT12)
	if err != nil {
		w.t.Fatal(err)
	}
	if _, err := w.conn.Write(b); err != nil {
		w.t.Fatal(err)
	}
}

func (w *wire) expect(fn byte) ft12.Frame {
	w.t.Helper()
	f, err := w.d.Next()
	if err != nil {
		w.t.Fatal(err)
	}
	if f.Kind == ft12.KindSingle || Control(f.Control).Func() != fn {
		w.t.Fatalf("got %v, want function %d", f, fn)
	}
	return f
}

// stations starts the station under test, unbalanced or balanced, with
// address 1 behind the returned wire.
func stations(t *testing.T, balanced bool, h *extStation, bc BalancedConfig) *wire {
	t.Helper()
	a, b := net.Pipe()
	var c interface{ Close() error }
	var err error
	if balanced {
		bc.Addr = 1
		c, err = NewBalanced(b, fast, bc, &balancedExt{h})
	} else {
		c, err = NewSecondary(b, fast, 1, h)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close(); _ = a.Close() })
	return newWire(t, a)
}

// balancedExt adapts an extStation to BalancedHandler, Busy included.
type balancedExt struct{ s *extStation }

func (b *balancedExt) UserData(d []byte) bool { return b.s.UserData(d, false) }
func (b *balancedExt) Reset(user bool)        { b.s.Reset(user) }
func (b *balancedExt) Busy() bool             { return b.s.Busy() }

// TestNotResetState: before reset of remote link a secondary answers only
// request status of link (with status of link) and reset of remote link;
// every other function is neither answered nor processed
// (IEC 60870-5-101:2003 6.2.1, figure 6 and figure 9, state "secondary
// link not reset").
func TestNotResetState(t *testing.T) {
	for _, balanced := range []bool{false, true} {
		h := &extStation{station: station{class2: [][]byte{{7}}}}
		h.refuse = map[byte]int{}
		w := stations(t, balanced, h, BalancedConfig{Remote: 2})
		w.send(FuncUserDataConf, true, []byte{1})
		w.send(FuncUserDataNoRepl, false, []byte{2})
		w.send(FuncResetUser, false, nil)
		w.send(FuncTestLink, true, nil)
		w.send(FuncRequestClass2, true, nil)
		w.send(FuncAccessDemand, false, nil)
		w.send(FuncRequestStatus, false, nil)
		w.expect(FuncStatus) // the first answer: nothing before it was answered
		if h.received() != 0 || len(h.resets) != 0 {
			t.Fatalf("balanced %v: processed before reset: %d user data, resets %v", balanced, h.received(), h.resets)
		}
		w.send(FuncResetLink, false, nil)
		w.expect(FuncAck)
		w.send(FuncUserDataConf, true, []byte{3})
		w.expect(FuncAck)
		if h.received() != 1 {
			t.Fatalf("balanced %v: user data after reset delivered %d times", balanced, h.received())
		}
	}
}

// TestResetFCBOptIn: function 7 is not in IEC 60870-5-101:2003 table 3; it
// is served only by a handler implementing FCBResetHandler (the
// IEC 60870-5-103 extension), and resets the link for it.
func TestResetFCBOptIn(t *testing.T) {
	// Without the extension: no answer before reset, 15 after.
	a, b := net.Pipe()
	s, err := NewSecondary(b, fast, 1, &station{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close(); _ = a.Close() })
	w := newWire(t, a)
	w.send(FuncResetFCB, false, nil)
	w.send(FuncRequestStatus, false, nil)
	w.expect(FuncStatus)
	w.send(FuncResetLink, false, nil)
	w.expect(FuncAck)
	w.send(FuncResetFCB, false, nil)
	w.expect(FuncNotImplemented)

	// With it: function 7 resets the link like function 0.
	h := &extStation{refuse: map[byte]int{}}
	w = stations(t, false, h, BalancedConfig{})
	w.send(FuncResetFCB, false, nil)
	w.expect(FuncAck)
	w.send(FuncUserDataConf, true, []byte{1})
	w.expect(FuncAck)
	if h.fcbReset != 1 || h.received() != 1 {
		t.Fatalf("reset FCB %d, received %d", h.fcbReset, h.received())
	}
}

// TestAccessDemand: request for access demand (function 8) is answered with
// status of link (IEC 60870-5-101:2003 table 3).
func TestAccessDemand(t *testing.T) {
	h := &extStation{refuse: map[byte]int{}}
	w := stations(t, false, h, BalancedConfig{})
	w.send(FuncResetLink, false, nil)
	w.expect(FuncAck)
	h.mu.Lock()
	h.class1 = [][]byte{{1}}
	h.mu.Unlock()
	if f := w.expect2(FuncAccessDemand, FuncStatus); !Control(f.Control).ACD() {
		t.Errorf("status of link without ACD: %v", f)
	}
}

func (w *wire) expect2(send, want byte) ft12.Frame {
	w.t.Helper()
	w.send(send, false, nil)
	return w.expect(want)
}

// TestSameFCBOtherRequest: a frame is a repetition only when it repeats the
// previous message: FCB not toggled (IEC 60870-5-101:2003 figures 6 and 9)
// and the same function and user data. Masters that do not toggle the FCB
// get their new requests processed, not the previous response replayed.
func TestSameFCBOtherRequest(t *testing.T) {
	for _, balanced := range []bool{false, true} {
		h := &extStation{station: station{class2: [][]byte{{0xC2}}}, refuse: map[byte]int{}}
		w := stations(t, balanced, h, BalancedConfig{Remote: 2})
		w.send(FuncResetLink, false, nil)
		w.expect(FuncAck)
		w.send(FuncUserDataConf, true, []byte{1})
		w.expect(FuncAck)
		// A true repetition: answered again, not delivered twice.
		w.send(FuncUserDataConf, true, []byte{1})
		w.expect(FuncAck)
		// The same FCB with other user data: a new message.
		w.send(FuncUserDataConf, true, []byte{2})
		w.expect(FuncAck)
		if h.received() != 2 {
			t.Fatalf("balanced %v: delivered %d, want 2", balanced, h.received())
		}
		if balanced {
			// The same FCB with another function: a new message.
			w.send(FuncTestLink, true, nil)
			w.expect(FuncAck)
			continue
		}
		// The same FCB, another function: a class 2 poll after user data
		// is answered with the class 2 data, not with the stored ACK.
		w.send(FuncRequestClass2, true, nil)
		if f := w.expect(FuncUserData); !bytes.Equal(f.Data, []byte{0xC2}) {
			t.Fatalf("class 2 answer %v", f)
		}
		// And user data after a poll with the same FCB again.
		w.send(FuncUserDataConf, true, []byte{3})
		w.expect(FuncAck)
		if h.received() != 3 {
			t.Fatalf("delivered %d, want 3", h.received())
		}
	}
}

// TestBusyRefusesUserData: while the handler is busy every response carries
// DFC = 1 and user data with confirmation is answered NACK, DFC = 1, and not
// delivered (IEC 60870-5-101:2003 figures 6 and 9, "secondary link busy").
func TestBusyRefusesUserData(t *testing.T) {
	for _, balanced := range []bool{false, true} {
		h := &extStation{refuse: map[byte]int{}}
		w := stations(t, balanced, h, BalancedConfig{Remote: 2})
		w.send(FuncResetLink, false, nil)
		w.expect(FuncAck)
		h.mu2.Lock()
		h.busyFlag = true
		h.mu2.Unlock()
		if f := w.expect2(FuncRequestStatus, FuncStatus); !Control(f.Control).DFC() {
			t.Errorf("balanced %v: status without DFC while busy", balanced)
		}
		w.send(FuncUserDataConf, true, []byte{1})
		if f := w.expect(FuncNack); !Control(f.Control).DFC() {
			t.Errorf("balanced %v: NACK without DFC", balanced)
		}
		if h.received() != 0 {
			t.Fatalf("balanced %v: delivered while busy", balanced)
		}
	}
}

// scripted answers a primary's frames: answer returns the response to
// each, nil for none.
func scripted(t *testing.T, answer func(f ft12.Frame) []ft12.Frame) net.Conn {
	t.Helper()
	a, b := net.Pipe()
	scriptedPeer(t, b, answer)
	t.Cleanup(func() { _ = b.Close() })
	return a
}

func fixedDFC(fn byte, addr uint16, dfc bool) ft12.Frame {
	return ft12.Frame{Kind: ft12.KindFixed, Addr: addr, Control: byte(SecondaryControl(fn, false, dfc, false))}
}

// TestInitWaitsForDFC: the primary resets the secondary link only after a
// status of link with DFC = 0, repeating request status of link meanwhile
// (IEC 60870-5-101:2003 figure 5: Rx[FC11, DFC = 1] returns to "request
// status of secondary link"; figure 8 likewise for balanced transmission).
func TestInitWaitsForDFC(t *testing.T) {
	for _, balanced := range []bool{false, true} {
		var mu sync.Mutex
		var seen []byte
		busy := 2
		conn := scripted(t, func(f ft12.Frame) []ft12.Frame {
			mu.Lock()
			defer mu.Unlock()
			fn := Control(f.Control).Func()
			seen = append(seen, fn)
			switch fn {
			case FuncRequestStatus:
				busy--
				return []ft12.Frame{fixedDFC(FuncStatus, 1, busy >= 0)}
			case FuncResetLink:
				return []ft12.Frame{fixedDFC(FuncAck, 1, false)}
			}
			return nil
		})
		var err error
		if balanced {
			var b *Balanced
			b, err = NewBalanced(conn, fast, BalancedConfig{Addr: 2, Remote: 1}, &peer{})
			if err == nil {
				defer func() { _ = b.Close() }()
				err = b.Init(context.Background())
			}
		} else {
			var pr *Primary
			pr, err = NewPrimary(conn, fast)
			if err == nil {
				defer func() { _ = pr.Close() }()
				_, err = pr.Init(context.Background(), 1)
			}
		}
		if err != nil {
			t.Fatalf("balanced %v: %v", balanced, err)
		}
		mu.Lock()
		got := string(seen)
		mu.Unlock()
		if want := string([]byte{9, 9, 9, 0}); got != want {
			t.Errorf("balanced %v: functions % x, want % x", balanced, got, want)
		}
	}
	// DFC that stays set: Init gives up with ErrDataFlow, never resets.
	var resets atomic.Int32
	conn := scripted(t, func(f ft12.Frame) []ft12.Frame {
		if Control(f.Control).Func() == FuncResetLink {
			resets.Add(1)
		}
		return []ft12.Frame{fixedDFC(FuncStatus, 1, true)}
	})
	pr, _ := NewPrimary(conn, fast)
	defer func() { _ = pr.Close() }()
	if _, err := pr.Init(context.Background(), 1); !errors.Is(err, ErrDataFlow) || resets.Load() != 0 {
		t.Fatalf("%v, %d resets", err, resets.Load())
	}
	ctx, cancel := context.WithTimeout(context.Background(), fast.Timeout/2)
	defer cancel()
	if _, err := pr.Init(ctx, 1); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancelled while waiting: %v", err)
	}
}

// TestBalancedSendWaitsForDFC: after a response with DFC = 1 the balanced
// primary requests status of link until DFC = 0 before it sends user data
// again (IEC 60870-5-101:2003 figure 8, "secondary link busy").
func TestBalancedSendWaitsForDFC(t *testing.T) {
	var mu sync.Mutex
	var seen []byte
	statusBusy := 1
	conn := scripted(t, func(f ft12.Frame) []ft12.Frame {
		mu.Lock()
		defer mu.Unlock()
		fn := Control(f.Control).Func()
		seen = append(seen, fn)
		switch fn {
		case FuncUserDataConf:
			return []ft12.Frame{fixedDFC(FuncAck, 1, true)}
		case FuncRequestStatus:
			statusBusy--
			return []ft12.Frame{fixedDFC(FuncStatus, 1, statusBusy >= 0)}
		}
		return []ft12.Frame{fixedDFC(FuncAck, 1, false)}
	})
	b, err := NewBalanced(conn, fast, BalancedConfig{Addr: 2, Remote: 1}, &peer{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = b.Close() }()
	ctx := context.Background()
	for i := range 2 {
		if err := b.Send(ctx, []byte{byte(i)}); err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	got := string(seen)
	mu.Unlock()
	if want := string([]byte{3, 9, 9, 3}); got != want {
		t.Errorf("functions % x, want % x", got, want)
	}
}

// TestLateResponseDiscarded: a response whose function code cannot answer
// the request (an ACK repeated late, while a class 2 poll is outstanding) is
// discarded and the real answer awaited (IEC 60870-5-101:2003 table 3).
func TestLateResponseDiscarded(t *testing.T) {
	conn := scripted(t, func(f ft12.Frame) []ft12.Frame {
		switch Control(f.Control).Func() {
		case FuncRequestClass2:
			return []ft12.Frame{fixed(FuncAck, 1), {Kind: ft12.KindVariable, Addr: 1, Data: []byte{0x42},
				Control: byte(SecondaryControl(FuncUserData, false, false, false))}}
		case FuncUserDataConf:
			return []ft12.Frame{fixed(FuncStatus, 1), fixed(FuncAck, 1)}
		case FuncRequestStatus:
			return []ft12.Frame{{Kind: ft12.KindSingle}, fixed(FuncStatus, 1)}
		}
		return nil
	})
	pr, _ := NewPrimary(conn, fast)
	defer func() { _ = pr.Close() }()
	ctx := context.Background()
	start := time.Now()
	if d, _, err := pr.RequestData(ctx, 1, Class2); err != nil || !bytes.Equal(d, []byte{0x42}) {
		t.Fatalf("class 2: % x %v", d, err)
	}
	if _, err := pr.SendConfirm(ctx, 1, []byte{1}); err != nil {
		t.Fatal(err)
	}
	if _, err := pr.RequestStatus(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if el := time.Since(start); el >= fast.Timeout {
		t.Errorf("took %v: the stale answers were waited out, not discarded", el)
	}
}

// TestBalancedReceiverAddr: by default the responses carry the peer's
// address, the receiver's (IEC 60870-5-101:2003 6.2.1.2); a station with
// SenderAddr (lib60870-C's convention) interworks with it, since responses
// with either address are accepted.
func TestBalancedReceiverAddr(t *testing.T) {
	x, y := net.Pipe()
	var addrs, addrsB sync.Map
	cfgA := fast
	cfgA.OnFrame = func(tx bool, f ft12.Frame) {
		if tx && !Control(f.Control).PRM() && f.Kind == ft12.KindFixed {
			addrs.Store(f.Addr, true)
		}
	}
	cfgB := fast
	cfgB.SenderAddr = true
	cfgB.OnFrame = func(tx bool, f ft12.Frame) {
		if tx && !Control(f.Control).PRM() && f.Kind == ft12.KindFixed {
			addrsB.Store(f.Addr, true)
		}
	}
	a, err := NewBalanced(x, cfgA, BalancedConfig{Addr: 1, Remote: 2, DIR: true}, &peer{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()
	pb := &peer{}
	b, err := NewBalanced(y, cfgB, BalancedConfig{Addr: 2, Remote: 1}, pb)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = b.Close() }()
	ctx := context.Background()
	for _, s := range []*Balanced{a, b} {
		if err := s.Init(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if err := b.Send(ctx, []byte{1}); err != nil {
		t.Fatal(err)
	}
	if err := a.Send(ctx, []byte{2}); err != nil {
		t.Fatal(err)
	}
	if _, own := addrs.Load(uint16(1)); own {
		t.Error("station A answered with its own address")
	}
	if _, rcv := addrs.Load(uint16(2)); !rcv {
		t.Error("station A did not answer with the receiver's address")
	}
	if _, own := addrsB.Load(uint16(2)); !own {
		t.Error("station B with SenderAddr did not answer with its own address")
	}
}

// TestEmptyUserData: without an ASDU a frame is a fixed-length frame
// (IEC 60870-5-101:2003 6.1); empty user data is refused.
func TestEmptyUserData(t *testing.T) {
	x, y := net.Pipe()
	b, _ := NewBalanced(x, fast, BalancedConfig{Addr: 1, Remote: 2}, &peer{})
	defer func() { _ = b.Close(); _ = y.Close() }()
	if err := b.Send(context.Background(), nil); !errors.Is(err, ErrNoData) {
		t.Fatal(err)
	}
}
