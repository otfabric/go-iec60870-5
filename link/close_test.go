// SPDX-License-Identifier: MIT

package link

import (
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/otfabric/go-iec60870-5/ft12"
)

// slowLine is a transport whose writes block until it is closed, and then take a while to
// return, as a serial driver or a TCP stack flushing on close can. writing receives a value
// when a write starts.
type slowLine struct {
	closed  chan struct{}
	once    sync.Once
	writing chan struct{}
}

func (l *slowLine) Read([]byte) (int, error) { <-l.closed; return 0, io.EOF }

func (l *slowLine) Write([]byte) (int, error) {
	select {
	case l.writing <- struct{}{}:
	default:
	}
	<-l.closed
	time.Sleep(50 * time.Millisecond) // the transport's own latency, not synchronisation
	return 0, errors.New("closed")
}

func (l *slowLine) Close() error { l.once.Do(func() { close(l.closed) }); return nil }

// writerRunning reports whether the writer goroutine of p is alive. It
// looks at p alone: a writer of a station an earlier test closed may still
// be on its way out, and is not this test's.
func writerRunning(p *port) bool {
	select {
	case <-p.writeDone:
		return false
	default:
		return true
	}
}

// Close returns only after the writer goroutine has: no frame is written or passed to
// OnFrame after Close, and no goroutine outlives the station.
func TestCloseWaitsForTheWriter(t *testing.T) {
	var traced atomic.Int32
	cfg := fast
	cfg.OnFrame = func(tx bool, _ ft12.Frame) {
		if tx {
			traced.Add(1)
		}
	}
	line := &slowLine{closed: make(chan struct{}), writing: make(chan struct{}, 1)}
	pr, err := NewPrimary(line, cfg)
	if err != nil {
		t.Fatal(err)
	}
	for range 5 {
		_ = pr.SendNoReply(1, []byte{1})
	}
	<-line.writing // the writer is blocked in Write
	if err := pr.Close(); err != nil {
		t.Fatal(err)
	}
	if writerRunning(pr.p) {
		t.Fatal("the writer goroutine is still running after Close")
	}
	// The writer, the only caller of OnFrame for sent frames, has returned:
	// the count is final. Only the frame that was being written was traced.
	if n := traced.Load(); n != 1 {
		t.Errorf("OnFrame called for %d sent frames, want the 1 being written", n)
	}
}
