// SPDX-License-Identifier: MIT

package e2e

import (
	"context"
	"errors"
	"math/rand"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/otfabric/go-iec60870-5/asdu"
	"github.com/otfabric/go-iec60870-5/ft12"
	"github.com/otfabric/go-iec60870-5/iec102"
	"github.com/otfabric/go-iec60870-5/iec102/client"
	"github.com/otfabric/go-iec60870-5/iec102/server"
	"github.com/otfabric/go-iec60870-5/iec103"
	"github.com/otfabric/go-iec60870-5/internal/testutil/serialline"
	"github.com/otfabric/go-iec60870-5/link"
)

// The hostile tests put a peer that does not follow IEC 60870-5-2 on the
// other end of an in-memory line and write its frames as raw octets, the
// way the IEC 101 suite does: whatever it sends, the station under test
// must not panic, must stay silent or answer as the procedures say, keep
// its queues bounded, serve a peer that behaves afterwards, close within a
// bound and leave no goroutine behind.

const closeBound = 2 * time.Second

// p2 is the FT 1.2 address size of the IEC 102 default.
var p2 = ft12.Params{AddrSize: iec102.DefaultLinkAddrSize}

// privateType is a private type identification the handler executes.
const privateType iec102.TypeID = 200

// executions counts the private requests the handler executed, per first
// octet of their body.
type executions struct {
	mu   sync.Mutex
	runs map[byte]int
}

func (e *executions) HandleASDU(s *server.Server, a *iec102.ASDU) {
	if raw, ok := a.Body.(iec102.Raw); ok && a.Type == privateType && len(raw.Data) > 0 {
		e.mu.Lock()
		e.runs[raw.Data[0]]++
		e.mu.Unlock()
		_ = s.Mirror(a, iec102.CauseActivationCon)
		return
	}
	_ = s.Mirror(a, iec102.CauseUnknownType)
}

func (e *executions) count(k byte) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.runs[k]
}

func waitRuns(t *testing.T, e *executions, k byte, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for e.count(k) < want && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(30 * time.Millisecond)
	if n := e.count(k); n != want {
		t.Fatalf("request %d executed %d times, want %d", k, n, want)
	}
}

func private(t testing.TB, k byte) []byte {
	t.Helper()
	b, err := iec102.New(iec102.CauseActivation, commonAddr, 0, iec102.Raw{Data: []byte{k}}).Encode(iec102.Params{})
	if err != nil {
		t.Fatal(err)
	}
	b[0] = byte(privateType)
	return b
}

func readTime(t testing.TB) []byte {
	t.Helper()
	b, err := iec102.New(iec102.CauseRequest, commonAddr, 0, iec102.Read{T: iec102.C_TI_NA_2}).Encode(iec102.Params{})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func hostileServer(t *testing.T, queue int) (*server.Server, *serialline.RawPrimary, *executions, func()) {
	t.Helper()
	check := serialline.Goroutines(t)
	a, b := serialline.Pair()
	e := &executions{runs: map[byte]int{}}
	s, err := server.New(a, server.Config{Link: link.Config{Timeout: 100 * time.Millisecond}, LinkAddr: linkAddr,
		CommonAddr: commonAddr, Store: meter(), Handler: e, QueueSize: queue})
	if err != nil {
		t.Fatal(err)
	}
	rp := serialline.NewRawPrimary(serialline.NewPeer(t, b, p2), linkAddr)
	rp.Timeout = 200 * time.Millisecond
	return s, rp, e, func() {
		t.Helper()
		if err := serialline.Within(t, closeBound, "server Close", s.Close); err != nil {
			t.Errorf("close: %v", err)
		}
		_ = b.Close()
		check()
	}
}

func frame(t testing.TB, fn byte, fcb, fcv bool, addr uint16, data []byte) []byte {
	t.Helper()
	f := serialline.Fixed(serialline.Primary(fn, fcb, fcv), addr)
	if data != nil {
		f = serialline.Variable(f.Control, addr, data)
	}
	b, err := f.Encode(p2)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// legitimate checks that the server serves a primary that behaves: it
// initializes the link and reads the system time.
func legitimate(t *testing.T, rp *serialline.RawPrimary) {
	t.Helper()
	time.Sleep(50 * time.Millisecond)
	rp.Drain()
	rp.Init()
	rp.PollAll(1 << 14)
	rp.Expect(link.FuncUserDataConf, readTime(t), 0, -1)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		d, _ := rp.Poll(2)
		if d == nil {
			time.Sleep(5 * time.Millisecond)
			continue
		}
		a, err := iec102.Decode(d, iec102.Params{})
		if err != nil {
			t.Fatalf("station sent an undecodable ASDU % X: %v", d, err)
		}
		if a.Type == iec102.M_TI_TA_2 {
			return
		}
	}
	t.Fatal("no M_TI_TA_2")
}

func TestHostileServer(t *testing.T) {
	cases := []struct {
		name  string
		queue int
		run   func(t *testing.T, s *server.Server, rp *serialline.RawPrimary, e *executions)
	}{
		{"line noise between frames", 0, func(t *testing.T, _ *server.Server, rp *serialline.RawPrimary, _ *executions) {
			rng := rand.New(rand.NewSource(1))
			for range 16 {
				noise := make([]byte, 256)
				rng.Read(noise)
				rp.MustRaw(noise)
				rp.MustRaw(frame(t, link.FuncRequestStatus, false, false, linkAddr, nil))
			}
			legitimate(t, rp)
		}},
		{"truncated, oversized and bad checksum", 0, func(t *testing.T, _ *server.Server, rp *serialline.RawPrimary, e *executions) {
			full := frame(t, link.FuncUserDataConf, true, true, linkAddr, private(t, 1))
			for n := 1; n < len(full); n++ {
				rp.MustRaw(full[:n])
			}
			bad := append([]byte(nil), full...)
			bad[len(bad)-2]++
			rp.MustRaw(bad)
			rp.MustRaw([]byte{0x68, 0x10, 0x11, 0x68, 0x73, 0x01, 0x00, 0x00, 0x16})
			rp.Silent(150*time.Millisecond, "broken frames")
			legitimate(t, rp)
			rp.Expect(link.FuncUserDataConf, make([]byte, p2.MaxData()), 0, -1)
			if n := e.count(1); n != 0 {
				t.Fatalf("broken request executed %d times", n)
			}
		}},
		{"other link addresses and secondary frames", 0, func(t *testing.T, _ *server.Server, rp *serialline.RawPrimary, e *executions) {
			for _, addr := range []uint16{0, 2, 0xFFFE, 0xFFFF} {
				rp.MustRaw(frame(t, link.FuncRequestStatus, false, false, addr, nil))
			}
			for _, fn := range []byte{0, 1, 8, 9, 11, 14, 15} {
				rp.MustRaw(rp.Encode(serialline.Fixed(serialline.Secondary(fn, true, true), linkAddr)))
			}
			rp.Silent(150*time.Millisecond, "another address or a secondary frame")
			legitimate(t, rp)
			rp.MustRaw(frame(t, link.FuncUserDataConf, false, true, 2, private(t, 2)))
			rp.Silent(150*time.Millisecond, "request to another address")
			if n := e.count(2); n != 0 {
				t.Fatalf("request to another address executed %d times", n)
			}
		}},
		{"wrong function codes", 0, func(t *testing.T, _ *server.Server, rp *serialline.RawPrimary, e *executions) {
			rp.MustRaw(frame(t, link.FuncUserDataConf, true, true, linkAddr, private(t, 3)))
			rp.Silent(150*time.Millisecond, "user data before the reset")
			rp.Init()
			for _, fn := range []byte{2, 5, 6, 7, 12, 13, 14, 15} {
				rp.Expect(fn, nil, int(link.FuncNotImplemented))
			}
			if n := e.count(3); n != 0 {
				t.Fatalf("request before the reset executed %d times", n)
			}
			legitimate(t, rp)
		}},
		{"stuck FCB and repetitions", 0, func(t *testing.T, _ *server.Server, rp *serialline.RawPrimary, e *executions) {
			rp.Init()
			req := frame(t, link.FuncUserDataConf, true, true, linkAddr, private(t, 4))
			for range 8 {
				rp.MustRaw(req)
				if f, err := rp.Next(time.Second); err != nil || serialline.Func(f) > 0 {
					t.Fatalf("repetition answered %v %v", f, err)
				}
			}
			waitRuns(t, e, 4, 1)
			rp.MustRaw(frame(t, link.FuncUserDataConf, true, true, linkAddr, private(t, 5)))
			if _, err := rp.Next(time.Second); err != nil {
				t.Fatal(err)
			}
			waitRuns(t, e, 5, 1)
			legitimate(t, rp)
		}},
		{"foreign and malformed ASDUs at every length", 0, func(t *testing.T, _ *server.Server, rp *serialline.RawPrimary, _ *executions) {
			rp.Init()
			gi103, _ := iec103.New(iec103.CauseGI, 1, iec103.FunGlobal, iec103.InfGIEnd, iec103.GIInit{SCN: 1}).Encode()
			gi101, _ := asdu.New(asdu.CauseActivation, 1, asdu.Interrogation{Qualifier: asdu.QOIStation}).Encode(asdu.Params{CauseSize: 1, CommonAddrSize: 1, IOASize: 2})
			for _, d := range [][]byte{gi103, gi101} {
				rp.Expect(link.FuncUserDataConf, d, 0, 1, -1)
			}
			rng := rand.New(rand.NewSource(2))
			templates := [][]byte{readTime(t), private(t, 6)}
			for _, typ := range []iec102.TypeID{iec102.C_CI_NU_2, iec102.C_CI_ND_2, iec102.C_SP_NB_2, iec102.C_CI_NB_2} {
				b, err := iec102.New(iec102.CauseActivation, commonAddr, 11, iec102.Read{T: typ, First: 1, Last: 6, From: hour(1, 0), To: hour(3, 0)}).Encode(iec102.Params{})
				if err != nil {
					t.Fatal(err)
				}
				templates = append(templates, b)
			}
			for n := 1; n <= p2.MaxData(); n++ {
				for _, tmpl := range templates {
					d := make([]byte, n)
					rng.Read(d)
					copy(d, tmpl[:min(len(tmpl), n)])
					rp.Expect(link.FuncUserDataConf, d, 0, 1, -1)
				}
				if n%8 == 0 {
					rp.PollAll(1 << 14)
				}
			}
			legitimate(t, rp)
		}},
		{"request flood keeps the queue bounded", 64, func(t *testing.T, _ *server.Server, rp *serialline.RawPrimary, _ *executions) {
			rp.Init()
			all, err := iec102.New(iec102.CauseActivation, commonAddr, 11, iec102.Read{T: iec102.C_CI_NU_2, First: 1, Last: 6, From: hour(1, 0), To: hour(3, 0)}).Encode(iec102.Params{})
			if err != nil {
				t.Fatal(err)
			}
			for range 300 {
				rp.Expect(link.FuncUserDataConf, all, 0, 1, -1)
			}
			time.Sleep(200 * time.Millisecond) // the requests still waiting are served
			if got := rp.PollAll(1 << 14); len(got) > 64 {
				t.Fatalf("%d ASDUs queued, the bound is 64", len(got))
			}
			legitimate(t, rp)
		}},
		{"half-open line with the longest length", 0, func(t *testing.T, _ *server.Server, rp *serialline.RawPrimary, _ *executions) {
			rp.MustRaw([]byte{0x68, 0xFF, 0xFF, 0x68, 0x73, 0x01, 0x00})
			time.Sleep(250 * time.Millisecond)
			legitimate(t, rp)
		}},
		{"slow drip", 0, func(t *testing.T, _ *server.Server, rp *serialline.RawPrimary, _ *executions) {
			for _, c := range frame(t, link.FuncRequestStatus, false, false, linkAddr, nil) {
				rp.MustRaw([]byte{c})
				time.Sleep(30 * time.Millisecond)
			}
			if f, err := rp.Next(time.Second); err != nil || serialline.Func(f) != int(link.FuncStatus) {
				t.Fatalf("dripped request status answered %v %v", f, err)
			}
			legitimate(t, rp)
		}},
		{"reset in the middle of a read", 0, func(t *testing.T, _ *server.Server, rp *serialline.RawPrimary, _ *executions) {
			rp.Init()
			all, _ := iec102.New(iec102.CauseActivation, commonAddr, 11, iec102.Read{T: iec102.C_CI_NU_2, First: 1, Last: 6, From: hour(1, 0), To: hour(3, 0)}).Encode(iec102.Params{})
			rp.Expect(link.FuncUserDataConf, all, 0, -1)
			time.Sleep(50 * time.Millisecond)
			rp.Poll(2)
			rp.Init() // reset of remote link discards the answers left
			if got := rp.PollAll(16); len(got) != 0 {
				t.Fatalf("%d answers left after reset of remote link", len(got))
			}
			legitimate(t, rp)
		}},
		{"peer vanishes mid-frame", 0, func(t *testing.T, s *server.Server, rp *serialline.RawPrimary, _ *executions) {
			rp.Init()
			rp.MustRaw(frame(t, link.FuncUserDataConf, true, true, linkAddr, readTime(t))[:7])
			_ = rp.End.Close()
			select {
			case <-s.Done():
			case <-time.After(closeBound):
				t.Fatal("server did not stop when the line closed")
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, rp, e, done := hostileServer(t, tc.queue)
			tc.run(t, s, rp, e)
			done()
		})
	}
}

// misbehave returns a hook that applies bad while *on is set.
func misbehave(m *sync.Mutex, on *bool, bad func(f ft12.Frame, a [][]byte) [][]byte) func(ft12.Frame, [][]byte) [][]byte {
	return func(f ft12.Frame, a [][]byte) [][]byte {
		m.Lock()
		b := *on
		m.Unlock()
		if b {
			return bad(f, a)
		}
		return a
	}
}

func encode102(a *iec102.ASDU) []byte {
	b, err := a.Encode(iec102.Params{})
	if err != nil {
		panic(err)
	}
	return b
}

// rawMeter is a raw secondary that answers C_TI_NA_2 and the private
// request the way the server does.
func rawMeter(t *testing.T, b *serialline.End) *serialline.RawSecondary {
	rs := serialline.NewRawSecondary(serialline.NewPeer(t, b, p2), linkAddr)
	rs.OnUserData = func(d []byte) [][]byte {
		a, err := iec102.Decode(d, iec102.Params{})
		if err != nil {
			return nil
		}
		if a.Type == iec102.C_TI_NA_2 {
			return [][]byte{encode102(iec102.New(iec102.CauseRequest, a.CommonAddr, 0, iec102.SystemTime{Time: iec102.TimeBOf(time.Now(), nil)}))}
		}
		m := *a
		m.Cause = iec102.CauseActivationCon
		return [][]byte{encode102(&m)}
	}
	return rs
}

func TestHostileClient(t *testing.T) {
	const reqTimeout = 400 * time.Millisecond
	user := func(fn int) bool { return fn == 10 || fn == 11 }
	cases := []struct {
		name string
		bad  func(rs *serialline.RawSecondary, rng *rand.Rand) func(ft12.Frame, [][]byte) [][]byte
		down bool
	}{
		{"silent station", func(*serialline.RawSecondary, *rand.Rand) func(ft12.Frame, [][]byte) [][]byte {
			return func(ft12.Frame, [][]byte) [][]byte { return nil }
		}, true},
		{"noise for answers", func(_ *serialline.RawSecondary, rng *rand.Rand) func(ft12.Frame, [][]byte) [][]byte {
			return func(ft12.Frame, [][]byte) [][]byte {
				b := make([]byte, 1+rng.Intn(40))
				rng.Read(b)
				for i := range b {
					if b[i] == ft12.SingleChar {
						b[i] = 0
					}
				}
				return [][]byte{b}
			}
		}, true},
		{"wrong functions for answers", func(rs *serialline.RawSecondary, _ *rand.Rand) func(ft12.Frame, [][]byte) [][]byte {
			return func(f ft12.Frame, _ [][]byte) [][]byte {
				fn := link.FuncStatus
				if serialline.Func(f) == int(link.FuncRequestStatus) {
					fn = link.FuncAck
				}
				return [][]byte{rs.Encode(serialline.Fixed(serialline.Secondary(fn, false, false), linkAddr))}
			}
		}, true},
		{"answers from another address", func(rs *serialline.RawSecondary, _ *rand.Rand) func(ft12.Frame, [][]byte) [][]byte {
			return func(f ft12.Frame, _ [][]byte) [][]byte {
				return [][]byte{rs.Encode(serialline.Fixed(serialline.Secondary(link.FuncAck, false, false), linkAddr+1))}
			}
		}, true},
		{"ACD flood without data", func(rs *serialline.RawSecondary, _ *rand.Rand) func(ft12.Frame, [][]byte) [][]byte {
			return func(f ft12.Frame, a [][]byte) [][]byte {
				if user(serialline.Func(f)) {
					return [][]byte{rs.Encode(serialline.Fixed(serialline.Secondary(link.FuncNoData, true, false), linkAddr))}
				}
				return a
			}
		}, false},
		{"answers that never end", func(rs *serialline.RawSecondary, _ *rand.Rand) func(ft12.Frame, [][]byte) [][]byte {
			var n int
			return func(f ft12.Frame, a [][]byte) [][]byte {
				if user(serialline.Func(f)) {
					n++
					d := encode102(iec102.New(iec102.CauseSpontaneous, uint16(commonAddr+n%2), 0, iec102.Raw{Data: []byte{byte(n)}}))
					return [][]byte{rs.Encode(serialline.Variable(serialline.Secondary(link.FuncUserData, true, false), linkAddr, d))}
				}
				return a
			}
		}, false},
		{"undecodable and foreign user data", func(rs *serialline.RawSecondary, rng *rand.Rand) func(ft12.Frame, [][]byte) [][]byte {
			gi103, _ := iec103.New(iec103.CauseGI, 1, iec103.FunGlobal, iec103.InfGIEnd, iec103.GITermination{SCN: 1}).Encode()
			return func(f ft12.Frame, a [][]byte) [][]byte {
				if user(serialline.Func(f)) {
					d := gi103
					if rng.Intn(2) == 0 {
						d = make([]byte, 1+rng.Intn(240))
						rng.Read(d)
					}
					return [][]byte{rs.Encode(serialline.Variable(serialline.Secondary(link.FuncUserData, false, false), linkAddr, d))}
				}
				return a
			}
		}, false},
		{"NACK forever", func(rs *serialline.RawSecondary, _ *rand.Rand) func(ft12.Frame, [][]byte) [][]byte {
			return func(f ft12.Frame, a [][]byte) [][]byte {
				if serialline.Func(f) == int(link.FuncUserDataConf) {
					return [][]byte{rs.Encode(serialline.Fixed(serialline.Secondary(link.FuncNack, false, true), linkAddr))}
				}
				return a
			}
		}, false},
		{"not functioning", func(rs *serialline.RawSecondary, _ *rand.Rand) func(ft12.Frame, [][]byte) [][]byte {
			return func(f ft12.Frame, a [][]byte) [][]byte {
				if fn := serialline.Func(f); user(fn) || fn == 3 {
					return [][]byte{rs.Encode(serialline.Fixed(serialline.Secondary(link.FuncNotFunctioning, false, false), linkAddr))}
				}
				return a
			}
		}, false},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			check := serialline.Goroutines(t)
			a, b := serialline.Pair()
			rs := rawMeter(t, b)
			var mu sync.Mutex
			bad := false
			rs.Hook = misbehave(&mu, &bad, tc.bad(rs, rand.New(rand.NewSource(int64(i)))))
			rs.Start()
			// The link gives up (200 ms) before the request (400 ms).
			c, err := client.New(a, client.Config{Link: link.Config{Timeout: 100 * time.Millisecond, Retries: 1}, LinkAddr: linkAddr,
				CommonAddr: commonAddr, PollInterval: 5 * time.Millisecond, RequestTimeout: reqTimeout,
				Handler: func(*iec102.ASDU) { time.Sleep(time.Millisecond) }})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := c.ReadTime(context.Background()); err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			bad = true
			mu.Unlock()
			for range 3 {
				start := time.Now()
				err := serialline.Within(t, reqTimeout+closeBound, "request", func() error {
					_, err := c.Request(context.Background(), iec102.New(iec102.CauseActivation, commonAddr, 0, iec102.Raw{Data: []byte{9}}), client.UntilTermination)
					return err
				})
				if d := time.Since(start); d > reqTimeout+time.Second {
					t.Errorf("request with a time-out of %v took %v: %v", reqTimeout, d, err)
				}
				if err == nil {
					t.Fatal("a request without termination ended without error")
				}
				if tc.down && !errors.Is(err, iec102.ErrLinkDown) {
					t.Errorf("request to a station that does not answer: %v", err)
				}
			}
			if tc.down && c.State() == iec102.StateUp {
				t.Error("link still up")
			}
			var ms runtime.MemStats
			runtime.ReadMemStats(&ms)
			if ms.HeapAlloc > 256<<20 {
				t.Fatalf("heap %d MiB", ms.HeapAlloc>>20)
			}
			mu.Lock()
			bad = false
			mu.Unlock()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			for {
				if _, err := c.ReadTime(ctx); err == nil {
					break
				} else if ctx.Err() != nil {
					t.Fatalf("not usable again: %v", err)
				}
			}
			if err := serialline.Within(t, closeBound, "client Close", c.Close); err != nil {
				t.Error(err)
			}
			_ = b.Close()
			check()
		})
	}
}
