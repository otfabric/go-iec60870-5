// SPDX-License-Identifier: MIT

// Package serialline is test infrastructure for the serial companion
// standards (IEC 60870-5-101, -102, -103): an in-memory full-duplex line
// whose two directions can drop, duplicate, corrupt, delay, split,
// truncate, reorder and add noise to what is written to them, a raw FT 1.2
// peer that writes octets straight onto the line, and a goroutine leak
// check. It holds no production code.
package serialline

import (
	"errors"
	"io"
	"math/rand"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// bufCap bounds the octets a direction holds that the reader has not taken:
// a writer blocks beyond it, as a UART does when its peer stops reading.
const bufCap = 64 << 10

// pipe is one direction of the line.
type pipe struct {
	mu     sync.Mutex
	cond   *sync.Cond
	buf    []byte
	closed bool
}

func newPipe() *pipe {
	p := &pipe{}
	p.cond = sync.NewCond(&p.mu)
	return p
}

func (p *pipe) read(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for len(p.buf) == 0 && !p.closed {
		p.cond.Wait()
	}
	if len(p.buf) == 0 {
		return 0, io.EOF
	}
	n := copy(b, p.buf)
	p.buf = p.buf[n:]
	if len(p.buf) == 0 {
		p.buf = nil
	}
	p.cond.Broadcast()
	return n, nil
}

func (p *pipe) write(b []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	for len(b) > 0 {
		for len(p.buf) >= bufCap && !p.closed {
			p.cond.Wait()
		}
		if p.closed {
			return io.ErrClosedPipe
		}
		n := min(len(b), bufCap-len(p.buf))
		p.buf = append(p.buf, b[:n]...)
		b = b[n:]
		p.cond.Broadcast()
	}
	return nil
}

func (p *pipe) close() {
	p.mu.Lock()
	p.closed = true
	p.cond.Broadcast()
	p.mu.Unlock()
}

// Faults are the probabilities, per write, of each fault of a direction.
// A station writes one frame per write, so they are per frame.
type Faults struct {
	Drop     float64 // the write is lost
	Dup      float64 // the write is delivered twice
	Corrupt  float64 // one bit of one octet is flipped
	Noise    float64 // 1..4 random octets come before the write
	Truncate float64 // only a prefix arrives
	Split    float64 // the octets arrive in pieces, with pauses
	Reorder  float64 // the write is held back and delivered after the next one
	Delay    float64 // the write is delivered after a pause
	// MaxDelay bounds the pause of Delay and of the pieces of Split
	// together, and how long Reorder holds a write. Keep it below the link
	// time-out: a serial line does not deliver a frame after its answer
	// was due.
	MaxDelay time.Duration
}

// Stats count the faults a line applied.
type Stats struct {
	Writes, Drop, Dup, Corrupt, Noise, Truncate, Split, Reorder, Delay int64
}

// Line is a full-duplex line between two ends.
type Line struct {
	A, B *End

	calm  atomic.Bool
	rngMu sync.Mutex
	rng   *rand.Rand
	f     [2]Faults // A to B, B to A

	stats struct {
		writes, drop, dup, corrupt, noise, truncate, split, reorder, delay atomic.Int64
	}
}

// New returns a line whose direction A to B applies ab and B to A applies
// ba, with random choices from seed.
func New(seed int64, ab, ba Faults) *Line {
	l := &Line{rng: rand.New(rand.NewSource(seed)), f: [2]Faults{ab, ba}}
	pa, pb := newPipe(), newPipe() // pa: what A reads; pb: what B reads
	l.A = &End{line: l, dir: 0, in: pa, out: pb}
	l.B = &End{line: l, dir: 1, in: pb, out: pa}
	return l
}

// Pair returns the two ends of a line without faults.
func Pair() (*End, *End) {
	l := New(1, Faults{}, Faults{})
	return l.A, l.B
}

// Calm stops (true) or resumes (false) the faults.
func (l *Line) Calm(c bool) { l.calm.Store(c) }

// Stats returns the faults applied so far.
func (l *Line) Stats() Stats {
	s := &l.stats
	return Stats{Writes: s.writes.Load(), Drop: s.drop.Load(), Dup: s.dup.Load(), Corrupt: s.corrupt.Load(),
		Noise: s.noise.Load(), Truncate: s.truncate.Load(), Split: s.split.Load(), Reorder: s.reorder.Load(),
		Delay: s.delay.Load()}
}

func (l *Line) chance(p float64) bool {
	if p <= 0 {
		return false
	}
	l.rngMu.Lock()
	defer l.rngMu.Unlock()
	return l.rng.Float64() < p
}

func (l *Line) intn(n int) int {
	if n <= 0 {
		return 0
	}
	l.rngMu.Lock()
	defer l.rngMu.Unlock()
	return l.rng.Intn(n)
}

// octet returns a random octet.
func (l *Line) octet() byte {
	v := l.intn(256)
	if v < 0 || v > 255 {
		return 0
	}
	return byte(v)
}

// End is one end of a line: an io.ReadWriteCloser. Closing either end
// closes the line: reads return io.EOF, writes io.ErrClosedPipe.
type End struct {
	line *Line
	dir  int
	in   *pipe
	out  *pipe

	wmu  sync.Mutex // one write at a time, faults included
	held []byte     // a write Reorder holds back
	tmr  *time.Timer
	once sync.Once
}

// ErrClosed is returned by a write to a closed end.
var ErrClosed = errors.New("serialline: closed")

// Read reads what the other end wrote.
func (e *End) Read(b []byte) (int, error) { return e.in.read(b) }

// Close closes the line.
func (e *End) Close() error {
	e.once.Do(func() {
		e.in.close()
		e.out.close()
		e.wmu.Lock()
		if e.tmr != nil {
			e.tmr.Stop()
		}
		e.wmu.Unlock()
	})
	return nil
}

// Line returns the line of the end.
func (e *End) Line() *Line { return e.line }

// Write writes b to the other end, through the faults of this direction.
func (e *End) Write(b []byte) (int, error) {
	l := e.line
	f := l.f[e.dir]
	total := len(b)
	b = append([]byte(nil), b...)
	e.wmu.Lock()
	defer e.wmu.Unlock()
	l.stats.writes.Add(1)
	if l.calm.Load() || len(b) == 0 {
		if err := e.flushHeld(); err != nil {
			return 0, err
		}
		if err := e.out.write(b); err != nil {
			return 0, err
		}
		return total, nil
	}
	if l.chance(f.Drop) {
		l.stats.drop.Add(1)
		return total, e.flushHeld()
	}
	if l.chance(f.Corrupt) {
		l.stats.corrupt.Add(1)
		i := l.intn(len(b))
		b[i] ^= 1 << l.intn(8)
	}
	if l.chance(f.Truncate) && len(b) > 1 {
		l.stats.truncate.Add(1)
		b = b[:1+l.intn(len(b)-1)]
	}
	if l.chance(f.Noise) {
		l.stats.noise.Add(1)
		n := 1 + l.intn(4)
		noise := make([]byte, n)
		for i := range noise {
			// Not E5h: a single character is a whole frame, an ACK out of
			// nothing that no checksum can catch.
			for noise[i] = l.octet(); noise[i] == 0xE5; noise[i] = l.octet() {
			}
		}
		b = append(noise, b...)
	}
	if l.chance(f.Dup) {
		l.stats.dup.Add(1)
		b = append(b, b...)
	}
	if f.MaxDelay > 0 && l.chance(f.Reorder) && e.held == nil {
		l.stats.reorder.Add(1)
		e.held = b
		e.tmr = time.AfterFunc(time.Duration(1+l.intn(int(f.MaxDelay))), func() {
			e.wmu.Lock()
			defer e.wmu.Unlock()
			_ = e.flushHeld()
		})
		return total, nil
	}
	if f.MaxDelay > 0 && l.chance(f.Delay) {
		l.stats.delay.Add(1)
		time.Sleep(time.Duration(l.intn(int(f.MaxDelay))))
	}
	if f.MaxDelay > 0 && l.chance(f.Split) && len(b) > 1 {
		l.stats.split.Add(1)
		pieces := 2 + l.intn(3)
		pause := f.MaxDelay / time.Duration(pieces)
		for len(b) > 0 {
			n := 1 + l.intn(len(b))
			if err := e.out.write(b[:n]); err != nil {
				return 0, err
			}
			b = b[n:]
			if len(b) > 0 {
				time.Sleep(time.Duration(l.intn(int(pause) + 1)))
			}
		}
		return total, e.flushHeld()
	}
	if err := e.out.write(b); err != nil {
		return 0, err
	}
	return total, e.flushHeld()
}

// flushHeld delivers a write Reorder held back. Called with wmu held.
func (e *End) flushHeld() error {
	if e.held == nil {
		return nil
	}
	h := e.held
	e.held = nil
	if e.tmr != nil {
		e.tmr.Stop()
		e.tmr = nil
	}
	return e.out.write(h)
}

// ChaosFaults are the faults of each direction of the chaos suites:
// frequent enough that every kind happens within a second, rare enough
// that exchanges still succeed. The pauses stay below the link time-outs
// of the suites (50 ms and more).
var ChaosFaults = Faults{Drop: 0.03, Dup: 0.03, Corrupt: 0.03, Noise: 0.03, Truncate: 0.02,
	Split: 0.05, Reorder: 0.02, Delay: 0.05, MaxDelay: 20 * time.Millisecond}

// InOrder returns f without Reorder: the faults of a direction that carries
// primary messages. The FCB of IEC 60870-5-2 is one bit, so a repetition
// a transport delivers after the next message is a new message to the
// secondary and is executed again; a serial line never reorders, and the
// chaos suites keep primary messages in order to check the FCB rules, not
// that limit (LIMITATIONS.md).
func InOrder(f Faults) Faults {
	f.Reorder = 0
	return f
}

// ChaosRuns returns how long each chaos seed runs and how many seeds run:
// E2E_CHAOS_SECONDS (default 2) and E2E_CHAOS_SEEDS (default 3), the
// variables of the IEC 104 chaos test; with -short one seed of one second.
func ChaosRuns() (time.Duration, int) {
	seconds, seeds := envInt("E2E_CHAOS_SECONDS", 2), envInt("E2E_CHAOS_SEEDS", 3)
	if testing.Short() {
		seconds, seeds = 1, 1
	}
	return time.Duration(seconds) * time.Second, seeds
}

func envInt(name string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(name)); err == nil && v > 0 {
		return v
	}
	return def
}

// Goroutines records the number of goroutines and returns a check that
// fails t when, after waiting up to 5 s for them to end, more than two
// are left (the slack the IEC 104 suites allow for the runtime's own).
func Goroutines(t testing.TB) func() {
	t.Helper()
	baseline := runtime.NumGoroutine()
	return func() {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for runtime.NumGoroutine() > baseline+2 && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		if n := runtime.NumGoroutine(); n > baseline+2 {
			buf := make([]byte, 1<<20)
			buf = buf[:runtime.Stack(buf, true)]
			t.Errorf("goroutine leak: %d running, %d before\n%s", n, baseline, buf)
		}
	}
}

// Within runs call and fails t when it does not return within bound. It
// returns call's error.
func Within(t testing.TB, bound time.Duration, what string, call func() error) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- call() }()
	select {
	case err := <-done:
		return err
	case <-time.After(bound):
		t.Fatalf("%s did not return within %v", what, bound)
		return nil
	}
}
