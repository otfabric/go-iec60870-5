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

type station struct {
	mu     sync.Mutex
	got    [][]byte
	bcast  int
	class1 [][]byte
	class2 [][]byte
	resets []bool
	refuse bool
}

func (s *station) UserData(d []byte, broadcast bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if broadcast {
		s.bcast++
	}
	if s.refuse {
		return false
	}
	s.got = append(s.got, bytes.Clone(d))
	return true
}

func pop(q *[][]byte) []byte {
	if len(*q) == 0 {
		return nil
	}
	d := (*q)[0]
	*q = (*q)[1:]
	return d
}

func (s *station) Class1() []byte  { s.mu.Lock(); defer s.mu.Unlock(); return pop(&s.class1) }
func (s *station) Class2() []byte  { s.mu.Lock(); defer s.mu.Unlock(); return pop(&s.class2) }
func (s *station) HasClass1() bool { s.mu.Lock(); defer s.mu.Unlock(); return len(s.class1) > 0 }
func (s *station) Reset(user bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resets = append(s.resets, user)
}
func (s *station) received() int { s.mu.Lock(); defer s.mu.Unlock(); return len(s.got) }

var fast = Config{FT12: ft12.Params{AddrSize: 1}, Timeout: 100 * time.Millisecond, Retries: 2}

func pair(t *testing.T, cfg Config, addr uint16, h SecondaryHandler) (*Primary, *Secondary) {
	t.Helper()
	a, b := net.Pipe()
	pr, err := NewPrimary(a, cfg)
	if err != nil {
		t.Fatal(err)
	}
	se, err := NewSecondary(b, cfg, addr, h)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pr.Close(); _ = se.Close() })
	return pr, se
}

func TestUnbalanced(t *testing.T) {
	for _, single := range []bool{false, true} {
		cfg := fast
		cfg.SingleCharAck = single
		h := &station{class1: [][]byte{{1}}, class2: [][]byte{{2, 2}}}
		pr, _ := pair(t, cfg, 7, h)
		ctx := context.Background()

		st, err := pr.Init(ctx, 7)
		if err != nil {
			t.Fatalf("single %v: init: %v", single, err)
		}
		if _, err := pr.ResetUser(ctx, 7); err != nil {
			t.Fatal(err)
		}
		if st, err = pr.SendConfirm(ctx, 7, []byte{0xAA, 0xBB}); err != nil || !st.ACD {
			t.Fatalf("send: %+v %v", st, err)
		}
		d, st, err := pr.RequestData(ctx, 7, Class2)
		if err != nil || !bytes.Equal(d, []byte{2, 2}) || !st.ACD {
			t.Fatalf("class 2: % x %+v %v", d, st, err)
		}
		d, st, err = pr.RequestData(ctx, 7, Class1)
		if err != nil || !bytes.Equal(d, []byte{1}) || st.ACD {
			t.Fatalf("class 1: % x %+v %v", d, st, err)
		}
		if d, _, err = pr.RequestData(ctx, 7, Class2); err != nil || d != nil {
			t.Fatalf("no data: % x %v", d, err)
		}
		if _, err := pr.ResetFCB(ctx, 7); err != nil {
			t.Fatal(err)
		}
		if _, err := pr.SendConfirm(ctx, 7, nil); err != nil {
			t.Fatal(err)
		}
		if err := pr.SendNoReply(cfg.Broadcast(), []byte{9}); err != nil {
			t.Fatal(err)
		}
		if err := pr.SendNoReply(7, nil); err != nil {
			t.Fatal(err)
		}
		waitFor(t, func() bool { return h.received() == 4 })
		if h.bcast != 1 || len(h.resets) != 2 || h.resets[0] || !h.resets[1] {
			t.Errorf("bcast %d resets %v", h.bcast, h.resets)
		}
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not reached")
}

func TestWrongAddressTimesOut(t *testing.T) {
	pr, _ := pair(t, fast, 7, &station{})
	start := time.Now()
	_, err := pr.RequestStatus(context.Background(), 8)
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("err %v", err)
	}
	if el := time.Since(start); el < 3*fast.Timeout {
		t.Errorf("gave up after %v, want 3 attempts", el)
	}
}

func TestNackAndDFC(t *testing.T) {
	h := &station{refuse: true}
	pr, _ := pair(t, fast, 3, h)
	st, err := pr.SendConfirm(context.Background(), 3, []byte{1})
	if !errors.Is(err, ErrNack) || !st.DFC {
		t.Fatalf("%+v %v", st, err)
	}
}

// lossy drops the first n frames the wrapped side writes.
type lossy struct {
	net.Conn
	drop atomic.Int32
}

func (l *lossy) Write(b []byte) (int, error) {
	if l.drop.Add(-1) >= 0 {
		return len(b), nil
	}
	return l.Conn.Write(b)
}

func TestRepetitionNotProcessedTwice(t *testing.T) {
	a, b := net.Pipe()
	lb := &lossy{Conn: b}
	pr, _ := NewPrimary(a, fast)
	h := &station{}
	se, _ := NewSecondary(lb, fast, 1, h)
	defer func() { _ = pr.Close() }()
	defer func() { _ = se.Close() }()
	ctx := context.Background()
	if _, err := pr.Init(ctx, 1); err != nil {
		t.Fatal(err)
	}
	lb.drop.Store(1) // lose the ACK of the next message
	if _, err := pr.SendConfirm(ctx, 1, []byte{5}); err != nil {
		t.Fatal(err)
	}
	if _, err := pr.SendConfirm(ctx, 1, []byte{6}); err != nil {
		t.Fatal(err)
	}
	if h.received() != 2 {
		t.Fatalf("user data delivered %d times, want 2: %v", h.received(), h.got)
	}
	// A lost class 2 response is repeated, not regenerated.
	h.mu.Lock()
	h.class2 = [][]byte{{1}, {2}}
	h.mu.Unlock()
	lb.drop.Store(1)
	d, _, err := pr.RequestData(ctx, 1, Class2)
	if err != nil || !bytes.Equal(d, []byte{1}) {
		t.Fatalf("% x %v", d, err)
	}
	if d, _, _ = pr.RequestData(ctx, 1, Class2); !bytes.Equal(d, []byte{2}) {
		t.Fatalf("second % x", d)
	}
}

func TestCloseAndCancel(t *testing.T) {
	pr, se := pair(t, fast, 1, &station{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := pr.RequestStatus(ctx, 2); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
	_ = se.Close()
	<-se.Done()
	if se.Err() != nil {
		t.Errorf("err after close: %v", se.Err())
	}
	if _, err := pr.RequestStatus(context.Background(), 1); !errors.Is(err, ErrClosed) {
		t.Fatalf("peer closed: %v", err)
	}
	<-pr.Done()
	if pr.Err() == nil {
		t.Error("transport error not reported")
	}
	if err := pr.SendNoReply(1, nil); !errors.Is(err, ErrClosed) {
		t.Fatalf("after close: %v", err)
	}
}

func TestConfigErrors(t *testing.T) {
	a, b := net.Pipe()
	defer func() { _ = a.Close() }()
	defer func() { _ = b.Close() }()
	if _, err := NewPrimary(a, Config{FT12: ft12.Params{AddrSize: 4}}); !errors.Is(err, ErrInvalidConfig) {
		t.Error(err)
	}
	if _, err := NewPrimary(a, Config{Timeout: -1}); !errors.Is(err, ErrInvalidConfig) {
		t.Error(err)
	}
	if _, err := NewSecondary(a, fast, 300, &station{}); !errors.Is(err, ErrInvalidConfig) {
		t.Error(err)
	}
	if _, err := NewBalanced(a, fast, BalancedConfig{Remote: 300}, nil); !errors.Is(err, ErrInvalidConfig) {
		t.Error(err)
	}
	if c, _ := (Config{Retries: -1}).withDefaults(); c.Retries != 0 || c.Timeout != DefaultTimeout {
		t.Errorf("%+v", c)
	}
}

func TestControl(t *testing.T) {
	c := PrimaryControl(FuncUserDataConf, true, true, true)
	if c != 0xF3 || !c.PRM() || !c.FCB() || !c.FCV() || !c.DIR() || c.ACD() || c.Func() != 3 {
		t.Errorf("%02X %v", byte(c), c)
	}
	s := SecondaryControl(FuncUserData, true, true, false)
	if s != 0x38 || s.PRM() || !s.ACD() || !s.DFC() || s.FCB() || s.FCV() {
		t.Errorf("%02X %v", byte(s), s)
	}
	if c.String() != "DIR PRM FCB FCV fn=3" || s.String() != "ACD DFC fn=8" {
		t.Errorf("%q %q", c, s)
	}
}

type peer struct {
	mu   sync.Mutex
	got  [][]byte
	user int
}

func (p *peer) UserData(d []byte) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.got = append(p.got, bytes.Clone(d))
	return len(d) == 0 || d[0] != 0xFF
}
func (p *peer) Reset(user bool) {
	if user {
		p.mu.Lock()
		p.user++
		p.mu.Unlock()
	}
}
func (p *peer) n() int { p.mu.Lock(); defer p.mu.Unlock(); return len(p.got) }

func TestBalanced(t *testing.T) {
	for _, size := range []int{0, 2} {
		for _, single := range []bool{false, true} {
			cfg := fast
			cfg.FT12.AddrSize = size
			cfg.SingleCharAck = single
			x, y := net.Pipe()
			var frames atomic.Int32
			cfgA := cfg
			cfgA.OnFrame = func(tx bool, f ft12.Frame) {
				if tx && f.Kind != ft12.KindSingle && Control(f.Control).DIR() != true {
					t.Errorf("station A sent DIR=0: %v", f)
				}
				frames.Add(1)
			}
			pa, pb := &peer{}, &peer{}
			ra, rb := uint16(0), uint16(0)
			if size > 0 {
				ra, rb = 1, 2
			}
			a, err := NewBalanced(x, cfgA, BalancedConfig{Addr: ra, Remote: rb, DIR: true}, pa)
			if err != nil {
				t.Fatal(err)
			}
			b, _ := NewBalanced(y, cfg, BalancedConfig{Addr: rb, Remote: ra}, pb)
			ctx := context.Background()
			var wg sync.WaitGroup
			for _, s := range []*Balanced{a, b} {
				wg.Add(1)
				go func() {
					defer wg.Done()
					if err := s.Init(ctx); err != nil {
						t.Error(err)
						return
					}
					for i := range 5 {
						if err := s.Send(ctx, []byte{byte(i)}); err != nil {
							t.Error(err)
						}
					}
					if err := s.Test(ctx); err != nil {
						t.Error(err)
					}
				}()
			}
			wg.Wait()
			if pa.n() != 5 || pb.n() != 5 {
				t.Errorf("size %d single %v: received %d / %d", size, single, pa.n(), pb.n())
			}
			if err := a.Send(ctx, []byte{0xFF}); !errors.Is(err, ErrNack) {
				t.Errorf("refused: %v", err)
			}
			if frames.Load() == 0 {
				t.Error("OnFrame not called")
			}
			_ = a.Close()
			<-b.Done()
			if b.Err() == nil {
				t.Error("b: transport error not reported")
			}
			_ = b.Close()
			if a.Err() != nil {
				t.Errorf("a closed by the application reports %v", a.Err())
			}
		}
	}
}
