// SPDX-License-Identifier: MIT

package link

import (
	"errors"
	"io"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/otfabric/go-iec60870-5/ft12"
)

// slowLine is a transport whose writes block until it is closed, and then take a while to
// return, as a serial driver or a TCP stack flushing on close can.
type slowLine struct {
	closed chan struct{}
	once   sync.Once
}

func (l *slowLine) Read([]byte) (int, error) { <-l.closed; return 0, io.EOF }

func (l *slowLine) Write([]byte) (int, error) {
	<-l.closed
	time.Sleep(50 * time.Millisecond)
	return 0, errors.New("closed")
}

func (l *slowLine) Close() error { l.once.Do(func() { close(l.closed) }); return nil }

// writerRunning reports whether a writer goroutine of a port is alive.
func writerRunning() bool {
	buf := make([]byte, 1<<20)
	return strings.Contains(string(buf[:runtime.Stack(buf, true)]), "link.(*port).writer")
}

// Close returns only after the writer goroutine has: no frame is written or passed to
// OnFrame after Close, and no goroutine outlives the station. It used to wait for the
// reader only.
func TestCloseWaitsForTheWriter(t *testing.T) {
	var traced atomic.Int32
	cfg := fast
	cfg.OnFrame = func(tx bool, _ ft12.Frame) {
		if tx {
			traced.Add(1)
		}
	}
	line := &slowLine{closed: make(chan struct{})}
	pr, err := NewPrimary(line, cfg)
	if err != nil {
		t.Fatal(err)
	}
	for range 5 {
		_ = pr.SendNoReply(1, []byte{1})
	}
	time.Sleep(20 * time.Millisecond) // the writer is blocked in Write
	if err := pr.Close(); err != nil {
		t.Fatal(err)
	}
	if writerRunning() {
		t.Fatal("the writer goroutine is still running after Close")
	}
	after := traced.Load()
	time.Sleep(100 * time.Millisecond)
	if n := traced.Load(); n != after {
		t.Errorf("OnFrame called %d times after Close", n-after)
	}
}
