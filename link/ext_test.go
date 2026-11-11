// SPDX-License-Identifier: MIT

package link

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
)

// extStation is a station that implements the optional interfaces.
type extStation struct {
	station
	mu2      sync.Mutex
	fcbReset int
	busyFlag bool
	refuse   map[byte]int // function code: how many more times to refuse it
}

func (s *extStation) ResetFCB()  { s.mu2.Lock(); s.fcbReset++; s.mu2.Unlock() }
func (s *extStation) Busy() bool { s.mu2.Lock(); defer s.mu2.Unlock(); return s.busyFlag }
func (s *extStation) Implemented(fn byte) bool {
	s.mu2.Lock()
	defer s.mu2.Unlock()
	if s.refuse[fn] > 0 {
		s.refuse[fn]--
		return false
	}
	return true
}

// TestSecondaryExtensions covers the IEC 60870-5-103 hooks: reset FCB is
// reported (6.3), DFC follows Busy, unimplemented function codes are
// answered with function 15 and still advance the FCB (6.3).
func TestSecondaryExtensions(t *testing.T) {
	a, b := net.Pipe()
	st := &extStation{refuse: map[byte]int{FuncResetUser: 1 << 30, FuncUserDataConf: 1, FuncUserDataNoRepl: 1}}
	sec, err := NewSecondary(b, fast, 1, st)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sec.Close() }()
	pr, err := NewPrimary(a, fast)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pr.Close() }()
	ctx := context.Background()
	if _, err := pr.Init(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := pr.ResetFCB(ctx, 1); err != nil {
		t.Fatal(err)
	}
	st.mu2.Lock()
	n := st.fcbReset
	st.mu2.Unlock()
	if n != 1 {
		t.Fatalf("ResetFCB called %d times", n)
	}
	if _, err := pr.ResetUser(ctx, 1); !errors.Is(err, ErrNotImplemented) {
		t.Fatal(err)
	}
	st.mu.Lock()
	resets := len(st.resets)
	st.mu.Unlock()
	if resets != 1 { // only the reset of remote link of Init
		t.Fatalf("resets %d", resets)
	}
	// Refused once with function 15; the next message (new FCB) is
	// processed, not taken for a repetition.
	if _, err := pr.SendConfirm(ctx, 1, []byte{1}); !errors.Is(err, ErrNotImplemented) {
		t.Fatal(err)
	}
	if _, err := pr.SendConfirm(ctx, 1, []byte{2}); err != nil {
		t.Fatal(err)
	}
	if st.received() != 1 {
		t.Fatalf("received %d", st.received())
	}
	// Refused send/no reply is dropped silently.
	if err := pr.SendNoReply(1, []byte{3}); err != nil {
		t.Fatal(err)
	}
	st.mu2.Lock()
	st.busyFlag = true
	st.mu2.Unlock()
	s, err := pr.RequestStatus(ctx, 1)
	if err != nil || !s.DFC {
		t.Fatal(s, err)
	}
	st.mu.Lock()
	st.class2 = append(st.class2, []byte{9})
	st.mu.Unlock()
	d, s, err := pr.RequestData(ctx, 1, Class2)
	if err != nil || !s.DFC || len(d) != 1 {
		t.Fatal(d, s, err)
	}
	if st.received() != 1 {
		t.Fatalf("received %d", st.received())
	}
}
