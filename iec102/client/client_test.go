// SPDX-License-Identifier: MIT

package client

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/otfabric/go-iec60870-5/ft12"
	"github.com/otfabric/go-iec60870-5/iec102"
	"github.com/otfabric/go-iec60870-5/link"
)

// station is a scripted secondary: it refuses the first refuse user data
// messages, and answers each accepted request with the octets of answers,
// as class 1 data when class1 is set. Each accepted request is signalled on
// seen when it is set.
type station struct {
	mu      sync.Mutex
	refuse  int
	class1  bool
	answers [][]byte
	queue   [][]byte
	got     [][]byte
	seen    chan struct{}
}

func (s *station) UserData(data []byte, _ bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.refuse > 0 {
		s.refuse--
		return false
	}
	s.got = append(s.got, data)
	s.queue = append(s.queue, s.answers...)
	if s.seen != nil {
		s.seen <- struct{}{}
	}
	return true
}

func (s *station) pop(class1 bool) []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	if class1 != s.class1 || len(s.queue) == 0 {
		return nil
	}
	b := s.queue[0]
	s.queue = s.queue[1:]
	return b
}

func (s *station) Class1() []byte { return s.pop(true) }
func (s *station) Class2() []byte { return s.pop(false) }
func (s *station) HasClass1() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.class1 && len(s.queue) > 0
}
func (s *station) Reset(bool) {}

func enc(t *testing.T, a *iec102.ASDU) []byte {
	t.Helper()
	b, err := a.Encode(iec102.Params{})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func start(t *testing.T, st *station, mod func(*Config)) *Client {
	t.Helper()
	a, b := net.Pipe()
	sec, err := link.NewSecondary(b, link.Config{FT12: ft12.Params{AddrSize: 2}}, 1, st)
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{LinkAddr: 1, CommonAddr: 1, PollInterval: time.Millisecond, Link: link.Config{Timeout: 100 * time.Millisecond, Retries: 1}}
	if mod != nil {
		mod(&cfg)
	}
	c, err := New(a, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close(); _ = sec.Close() })
	return c
}

func tctx(t *testing.T) context.Context {
	c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return c
}

func TestNackClass1AndJunk(t *testing.T) {
	spec := iec102.New(iec102.CauseRequest, 1, 0, iec102.ProductSpec{Manufacturer: 3})
	st := &station{refuse: 2, class1: true, answers: [][]byte{{0xFF}, enc(t, spec)}}
	c := start(t, st, nil)
	got, err := c.ReadProductSpec(tctx(t))
	if err != nil || got.Manufacturer != 3 {
		t.Fatal(got, err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatal("second close", err)
	}
}

func TestWrongAnswers(t *testing.T) {
	other := enc(t, iec102.New(iec102.CauseRequest, 1, 0, iec102.SystemTime{}))
	st := &station{answers: [][]byte{other}}
	c := start(t, st, nil)
	if _, err := c.ReadProductSpec(tctx(t)); !errors.Is(err, iec102.ErrInvalid) {
		t.Fatal(err)
	}
	st.answers = [][]byte{enc(t, iec102.New(iec102.CauseRequest, 1, 0, iec102.ProductSpec{}))}
	if _, err := c.ReadTime(tctx(t)); !errors.Is(err, iec102.ErrInvalid) {
		t.Fatal(err)
	}
	// Encoding fails before anything is sent.
	if _, err := c.Request(tctx(t), &iec102.ASDU{}, UntilReply); !errors.Is(err, iec102.ErrInvalid) {
		t.Fatal(err)
	}
}

func TestTimeoutAndBusy(t *testing.T) {
	// Accepts, never answers: the client polls until the deadline.
	st := &station{seen: make(chan struct{}, 8)}
	c := start(t, st, func(cfg *Config) { cfg.RequestTimeout = 100 * time.Millisecond })
	if _, err := c.ReadTime(context.Background()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	<-st.seen
	// A second request waits for the first; its own context ends first.
	first, release := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = c.ReadTime(first)
	}()
	<-st.seen // the first request holds the request slot
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := c.Init(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	release()
	<-done
	// Closing ends a request in progress.
	closed := make(chan error, 1)
	go func() { <-st.seen; closed <- c.Close() }()
	if _, err := c.ReadTime(tctx(t)); !errors.Is(err, iec102.ErrClosed) {
		t.Fatal(err)
	}
	if err := <-closed; err != nil {
		t.Fatal(err)
	}
	if err := c.Init(tctx(t)); !errors.Is(err, iec102.ErrClosed) {
		t.Fatal(err)
	}
}

func TestHandlerQueueFull(t *testing.T) {
	block := make(chan struct{})
	var n int
	var mu sync.Mutex
	foreign := enc(t, iec102.New(iec102.CauseSpontaneous, 2, 0, iec102.Raw{Data: []byte{1}}))
	answers := make([][]byte, 0, 80)
	for i := 0; i < 70; i++ {
		answers = append(answers, foreign)
	}
	answers = append(answers, enc(t, iec102.New(iec102.CauseRequest, 1, 0, iec102.SystemTime{})))
	st := &station{answers: answers}
	c := start(t, st, func(cfg *Config) {
		cfg.Handler = func(*iec102.ASDU) { <-block; mu.Lock(); n++; mu.Unlock() }
	})
	if _, err := c.ReadTime(tctx(t)); err != nil {
		t.Fatal(err)
	}
	close(block)
}

// TestMirrorOfAnotherRange: an ASDU with the type, common address and
// record of the request but another time range does not end it; only the
// mirror of the request itself does.
func TestMirrorOfAnotherRange(t *testing.T) {
	at := func(h int) iec102.TimeA { return iec102.TimeAOf(time.Date(2024, 3, 2, h, 0, 0, 0, time.UTC), nil) }
	q := iec102.Read{T: iec102.C_CI_NU_2, First: 1, Last: 1, From: at(5), To: at(5)}
	other := iec102.Read{T: iec102.C_CI_NU_2, First: 1, Last: 1, From: at(1), To: at(2)}
	req := iec102.New(iec102.CauseActivation, 1, 11, q)
	st := &station{answers: [][]byte{
		enc(t, iec102.New(iec102.CauseActivationCon, 1, 11, q)),
		enc(t, iec102.New(iec102.CauseActivationTerm, 1, 11, other)),
		enc(t, iec102.New(iec102.CauseRequest, 1, 11, iec102.IntegratedTotals{T: iec102.M_IT_TK_2, Time: at(5),
			Totals: []iec102.IntegratedTotal{{Addr: 1, Value: 5}}})),
		enc(t, iec102.New(iec102.CauseActivationTerm, 1, 11, q)),
	}}
	c := start(t, st, nil)
	out, err := c.Request(tctx(t), req, UntilTermination)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 4 {
		t.Fatalf("%d ASDUs, want 4: the termination of another range does not end the request", len(out))
	}
	if last := out[len(out)-1]; last.Cause != iec102.CauseActivationTerm || last.Body.(iec102.Read) != q {
		t.Fatalf("ended by %v %+v", last, last.Body)
	}
}
