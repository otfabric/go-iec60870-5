// SPDX-License-Identifier: MIT

package server

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/otfabric/go-iec60870-5/iec103"
	"github.com/otfabric/go-iec60870-5/internal/testutil/serialline"
)

type warnRecorder struct {
	mu    sync.Mutex
	warns []string
}

func (*warnRecorder) Debugf(string, ...any) {}
func (*warnRecorder) Infof(string, ...any)  {}
func (*warnRecorder) Errorf(string, ...any) {}
func (r *warnRecorder) Warnf(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.warns = append(r.warns, fmt.Sprintf(format, args...))
}

func (r *warnRecorder) has(s string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, w := range r.warns {
		if strings.Contains(w, s) {
			return true
		}
	}
	return false
}

// TestClass1QueueFull: an identification or disturbance ASDU that does not
// fit the full class 1 queue is dropped with a warning, and the queue stays
// at QueueSize.
func TestClass1QueueFull(t *testing.T) {
	a, b := serialline.Pair()
	defer func() { _ = b.Close() }()
	log := &warnRecorder{}
	s, err := New(a, Config{LinkAddr: 3, Fun: 128, QueueSize: 2, SyncDelay: -1, Logger: log})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	eot := iec103.EndOfTransmission{TOO: iec103.TOO(34)}
	for {
		err := s.Send(iec103.New(iec103.CauseDisturbanceData, s.ca, 128, 0, eot), Class1)
		if errors.Is(err, ErrQueueFull) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	s.identify(iec103.CauseResetFCB, true)
	s.distSend(128, eot)
	if n := s.Pending(Class1); n != 2 {
		t.Errorf("class 1 queue %d, want 2", n)
	}
	for _, w := range []string{"identification not queued", "disturbance data not queued"} {
		if !log.has(w) {
			t.Errorf("no warning %q in %q", w, log.warns)
		}
	}
}
