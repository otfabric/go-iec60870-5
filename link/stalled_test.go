// SPDX-License-Identifier: MIT

package link

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/otfabric/go-iec60870-5/ft12"
	"github.com/otfabric/go-iec60870-5/internal/testutil/leakcheck"
)

// stalledLine is a transport whose peer stops reading: Write blocks until
// release is closed (or the line is closed, for the io.Closer variant).
// Read blocks the same way. writing receives a value when a Write starts.
type stalledLine struct {
	release chan struct{}
	once    sync.Once
	writing chan struct{}
	writes  atomic.Int32
}

func newStalledLine() *stalledLine {
	return &stalledLine{release: make(chan struct{}), writing: make(chan struct{}, 1)}
}

func (l *stalledLine) Read([]byte) (int, error) { <-l.release; return 0, io.EOF }

func (l *stalledLine) Write([]byte) (int, error) {
	l.writes.Add(1)
	select {
	case l.writing <- struct{}{}:
	default:
	}
	<-l.release
	return 0, errors.New("released")
}

func (l *stalledLine) free() { l.once.Do(func() { close(l.release) }) }

// closableStalledLine is a stalledLine that is an io.Closer: Close
// unblocks its Read and Write.
type closableStalledLine struct{ *stalledLine }

func (l closableStalledLine) Close() error { l.free(); return nil }

// fillQueue waits until the writer is blocked in Write with one frame and
// then fills the send queue, as a peer that stops reading leaves it.
func fillQueue(t *testing.T, p *port, l *stalledLine) {
	t.Helper()
	f := ft12.Frame{Kind: ft12.KindSingle}
	if err := p.write(context.Background(), f); err != nil {
		t.Fatal(err)
	}
	recv(t, l.writing, "the writer to block in Write")
	for len(p.tx) < cap(p.tx) {
		p.tx <- f
	}
}

// An exchange whose frame waits for room in a full send queue ends with
// its context (the peer stopped reading), for each kind of primary.
func TestExchangeEndsWithContextOnFullQueue(t *testing.T) {
	type stn struct {
		p        *port
		exchange func(context.Context) error
		close    func() error
	}
	for _, tc := range []struct {
		name  string
		start func(t *testing.T, rw io.ReadWriter) stn
	}{
		{"primary", func(t *testing.T, rw io.ReadWriter) stn {
			pr, err := NewPrimary(rw, fast)
			if err != nil {
				t.Fatal(err)
			}
			return stn{pr.p, func(ctx context.Context) error { _, err := pr.RequestStatus(ctx, 1); return err }, pr.Close}
		}},
		{"balanced", func(t *testing.T, rw io.ReadWriter) stn {
			b, err := NewBalanced(rw, fast, BalancedConfig{Addr: 1, Remote: 2}, &balancedUser{})
			if err != nil {
				t.Fatal(err)
			}
			return stn{b.p, b.Test, b.Close}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			check := leakcheck.Check(t)
			l := newStalledLine()
			st := tc.start(t, closableStalledLine{l})
			fillQueue(t, st.p, l)
			ctx, cancel := context.WithCancel(context.Background())
			res := make(chan error, 1)
			go func() { res <- st.exchange(ctx) }()
			cancel()
			if err := recv(t, res, "the exchange to end with its context"); !errors.Is(err, context.Canceled) {
				t.Fatalf("exchange: %v, want context.Canceled", err)
			}
			// A second exchange is not held behind the first one.
			ctx2, cancel2 := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel2()
			go func() { res <- st.exchange(ctx2) }()
			if err := recv(t, res, "the next exchange to end"); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("next exchange: %v, want context.DeadlineExceeded", err)
			}
			if err := st.close(); err != nil {
				t.Fatal(err)
			}
			check()
		})
	}
}

// balancedUser accepts everything.
type balancedUser struct{}

func (*balancedUser) UserData([]byte) bool { return true }
func (*balancedUser) Reset(bool)           {}

// On a transport that is not an io.Closer, Close does not wait for a
// writer blocked in Write; once that Write returns the writer ends without
// writing the queued frames.
func TestCloseWithoutCloserBlockedWrite(t *testing.T) {
	check := leakcheck.Check(t)
	l := newStalledLine()
	var traced atomic.Int32
	cfg := fast
	cfg.OnFrame = func(tx bool, _ ft12.Frame) {
		if tx {
			traced.Add(1)
		}
	}
	pr, err := NewPrimary(l, cfg)
	if err != nil {
		t.Fatal(err)
	}
	fillQueue(t, pr.p, l)
	closed := make(chan error, 1)
	go func() { closed <- pr.Close() }()
	if err := recv(t, closed, "Close with a writer blocked in Write"); err != nil {
		t.Fatal(err)
	}
	l.free()
	recv(t, pr.Done(), "the reader to end")
	check()
	if n := l.writes.Load(); n != 1 {
		t.Errorf("%d writes, want the 1 that was blocked", n)
	}
	if n := traced.Load(); n != 1 {
		t.Errorf("OnFrame called for %d sent frames, want 1", n)
	}
}

// On a transport that is not an io.Closer and an idle writer, Close still
// waits for the writer goroutine.
func TestCloseWithoutCloserIdleWriter(t *testing.T) {
	check := leakcheck.Check(t)
	l := newStalledLine()
	pr, err := NewPrimary(l, fast)
	if err != nil {
		t.Fatal(err)
	}
	if err := pr.Close(); err != nil {
		t.Fatal(err)
	}
	if writerRunning() {
		t.Fatal("the writer goroutine is still running after Close")
	}
	l.free()
	recv(t, pr.Done(), "the reader to end")
	check()
}

// A broadcast reaches the handler only as send/no reply of user data in a
// variable-length frame, and only when the handler's FunctionFilter serves
// function 4, as for an addressed message.
func TestSecondaryBroadcastKindAndFilter(t *testing.T) {
	a, b := net.Pipe()
	st := &extStation{refuse: map[byte]int{FuncUserDataNoRepl: 1}}
	sec, err := NewSecondary(b, fast, 1, st)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sec.Close() }()
	defer func() { _ = a.Close() }()
	send := func(f ft12.Frame) {
		t.Helper()
		raw, err := f.Encode(fast.FT12)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := a.Write(raw); err != nil {
			t.Fatal(err)
		}
	}
	// Reset the link and read the ACK.
	send(ft12.Frame{Kind: ft12.KindFixed, Addr: 1, Control: byte(PrimaryControl(FuncResetLink, false, false, false))})
	if _, err := ft12.NewDecoder(a, fast.FT12).Next(); err != nil {
		t.Fatal(err)
	}
	bcast := fast.Broadcast()
	noReply := byte(PrimaryControl(FuncUserDataNoRepl, false, false, false))
	send(ft12.Frame{Kind: ft12.KindFixed, Addr: bcast, Control: noReply})                     // no user data
	send(ft12.Frame{Kind: ft12.KindVariable, Addr: bcast, Control: noReply, Data: []byte{1}}) // refused by the filter
	send(ft12.Frame{Kind: ft12.KindVariable, Addr: bcast, Control: noReply, Data: []byte{2}}) // delivered
	waitFor(t, func() bool { return st.received() == 1 })
	// The station handles frames in order: a frame after the delivered
	// one makes sure nothing else is pending.
	send(ft12.Frame{Kind: ft12.KindVariable, Addr: bcast, Control: noReply, Data: []byte{3}})
	waitFor(t, func() bool { return st.received() == 2 })
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.bcast != 2 || !bytes.Equal(st.got[0], []byte{2}) || !bytes.Equal(st.got[1], []byte{3}) {
		t.Errorf("broadcasts %d, data % x", st.bcast, st.got)
	}
}
