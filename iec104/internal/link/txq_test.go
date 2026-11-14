// SPDX-License-Identifier: MIT

package link

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"sync/atomic"
	"testing"
	"time"

	iec104 "github.com/otfabric/go-iec60870-5/iec104"
	"github.com/otfabric/go-iec60870-5/iec104/apci"
)

// answering is a Security that answers every received ASDU with per frames
// of size octets, numbered in the order produced, and records the largest
// transmit queue it found when called.
type answering struct {
	per, size int
	link      atomic.Pointer[Link]
	received  atomic.Int64
	maxQueued atomic.Int64
	next      uint32 // protocol goroutine only
}

func (a *answering) Attach(any, time.Time) [][]byte { return nil }
func (a *answering) Detach(any)                     {}
func (a *answering) Owns(any) bool                  { return true }
func (a *answering) Tick(time.Time) [][]byte        { return nil }
func (a *answering) Deadline() time.Time            { return time.Time{} }
func (a *answering) Changed() <-chan struct{}       { return nil }

func (a *answering) Protect(_ time.Time, data []byte) ([][]byte, error) {
	return [][]byte{data}, nil
}

func (a *answering) Receive(time.Time, []byte) ([]byte, [][]byte, error) {
	a.received.Add(1)
	if l := a.link.Load(); l != nil {
		// Called on the protocol goroutine, which owns txBytes.
		if q := int64(l.txBytes); q > a.maxQueued.Load() {
			a.maxQueued.Store(q)
		}
	}
	out := make([][]byte, a.per)
	for i := range out {
		f := make([]byte, a.size)
		binary.BigEndian.PutUint32(f, a.next)
		a.next++
		out[i] = f
	}
	return nil, out, nil
}

// limitTx sets the transmit queue limits for one test.
func limitTx(t *testing.T, soft, hard int) {
	t.Helper()
	oldSoft, oldHard := txSoftLimit, txHardLimit
	txSoftLimit, txHardLimit = soft, hard
	// Registered before the link is made, so it runs after the link closed.
	t.Cleanup(func() { txSoftLimit, txHardLimit = oldSoft, oldHard })
}

func answeringLink(t *testing.T, sec *answering) (*Link, *peer, *recorder) {
	t.Helper()
	l, p, rec := setupConfig(t, Controlled, fastParams(), func(c *Config) {
		c.Security = sec
		c.NotReady = errors.New("not ready")
	})
	sec.link.Store(l)
	p.send(apci.NewU(apci.StartDTAct))
	p.expectU(apci.StartDTCon)
	return l, p, rec
}

// A peer that goes on sending I frames without acknowledging what the
// station answers, past its own window, ends the link once the answers
// waiting for the send window exceed the limit, instead of having them
// queued without bound.
func TestSecurityOutputPeerPastWindowIsClosed(t *testing.T) {
	limitTx(t, 1000, 4000)
	sec := &answering{per: 2, size: 100}
	l, p, rec := answeringLink(t, sec)

	const n = 200
	go func() {
		for i := range n {
			b, _ := apci.NewI(uint16(i), 0, []byte{byte(i)}).MarshalBinary()
			if _, err := p.conn.Write(b); err != nil {
				return
			}
		}
	}()
	waitClosed(t, l, rec, iec104.ErrProtocol)
	if got := sec.received.Load(); got >= n {
		t.Fatalf("link took all %d frames before it ended", got)
	}
	if q := sec.maxQueued.Load(); q > int64(txHardLimit) {
		t.Fatalf("transmit queue reached %d octets, limit %d", q, txHardLimit)
	}

	// The station sent one window of answers, and acknowledged nothing it
	// received after the soft limit was reached.
	var frames, maxNR int
	for {
		_ = p.conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		f, err := apci.ReadFrame(p.conn)
		if err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
				var ne interface{ Timeout() bool }
				if errors.As(err, &ne) && ne.Timeout() {
					t.Fatal("connection not closed")
				}
			}
			break
		}
		if f.Format == apci.FormatI {
			frames++
		}
		if f.Format != apci.FormatU {
			maxNR = max(maxNR, int(f.RecvSeq))
		}
	}
	if frames != fastParams().K {
		t.Fatalf("peer got %d I frames, want the window of %d", frames, fastParams().K)
	}
	if limit := txSoftLimit/(sec.per*sec.size) + fastParams().W; maxNR > limit {
		t.Fatalf("station acknowledged N(R)=%d, beyond the soft limit at about %d", maxNR, limit)
	}
}

// A peer that keeps to its window and acknowledges the station's I frames
// only when the station's window is full is throttled while the answers
// queue: the station withholds acknowledgements, the peer stops after k
// frames, the queue drains and everything arrives in order on a link that
// stays up.
func TestSecurityOutputSlowAcknowledgementStaysBounded(t *testing.T) {
	limitTx(t, 1000, 4000)
	sec := &answering{per: 2, size: 100}
	l, p, _ := answeringLink(t, sec)
	k := fastParams().K

	const total = 60
	var (
		sent     int // I frames the peer sent
		ackedUs  int // of those, acknowledged by the station
		got      int // I frames received from the station
		ackedOut int // of those, acknowledged by the peer
	)
	for got < total*sec.per || ackedUs < total {
		for sent < total && sent-ackedUs < k {
			p.send(apci.NewI(uint16(sent), uint16(got), []byte{byte(sent)}))
			sent++
			ackedOut = got
		}
		f := p.read()
		switch f.Format {
		case apci.FormatI:
			if f.SendSeq != uint16(got) {
				t.Fatalf("I frame N(S)=%d, want %d", f.SendSeq, got)
			}
			if n := binary.BigEndian.Uint32(f.ASDU); n != uint32(got) {
				t.Fatalf("answer %d arrived as frame %d", n, got)
			}
			got++
			ackedUs = max(ackedUs, int(f.RecvSeq))
			if got-ackedOut >= k {
				p.send(apci.NewS(uint16(got)))
				ackedOut = got
			}
		case apci.FormatS:
			ackedUs = max(ackedUs, int(f.RecvSeq))
		default:
			t.Fatalf("peer got %s", f)
		}
	}
	// Application data still goes out once the answers are through.
	p.send(apci.NewS(uint16(got)))
	errc := make(chan error, 1)
	go func() { errc <- l.Send(context.Background(), []byte{0xAA}) }()
	if f := p.expectI(uint16(got), total); !bytes.Equal(f.ASDU, []byte{0xAA}) {
		t.Fatalf("peer got % X, want the application data", f.ASDU)
	}
	if err := <-errc; err != nil {
		t.Fatalf("Send: %v", err)
	}
	if err := l.Err(); err != nil {
		t.Fatalf("link ended: %v", err)
	}
	q := sec.maxQueued.Load()
	if q < int64(txSoftLimit) {
		t.Fatalf("transmit queue peaked at %d octets: the soft limit %d was never reached", q, txSoftLimit)
	}
	if limit := int64(txSoftLimit + (k+1)*sec.per*sec.size); q > limit {
		t.Fatalf("transmit queue reached %d octets, bound %d", q, limit)
	}
}
