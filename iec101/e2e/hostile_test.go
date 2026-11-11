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
	"github.com/otfabric/go-iec60870-5/iec101"
	"github.com/otfabric/go-iec60870-5/iec101/client"
	"github.com/otfabric/go-iec60870-5/iec101/server"
	"github.com/otfabric/go-iec60870-5/iec103"
	"github.com/otfabric/go-iec60870-5/internal/testutil/serialline"
	"github.com/otfabric/go-iec60870-5/link"
)

// The hostile tests put a peer that does not follow IEC 60870-5-2 on the
// other end of an in-memory line and write its frames as raw octets.
// Whatever it sends, the station under test must not panic, must answer as
// the procedures say or stay silent, must keep its queues bounded, must
// serve a peer that behaves afterwards, must close within a bound and must
// leave no goroutine behind.

// closeBound is how long Close may take.
const closeBound = 2 * time.Second

// counted is the e2e station that counts the commands it executes, per
// information object address and value.
type counted struct {
	*station
	mu   sync.Mutex
	runs map[asdu.IOA]int
}

func newCounted() *counted { return &counted{station: &station{}, runs: map[asdu.IOA]int{}} }

func (c *counted) HandleASDU(s *server.Server, a *asdu.ASDU) {
	if o, ok := a.First().(asdu.SingleCommand); ok {
		c.mu.Lock()
		c.runs[o.IOA]++
		c.mu.Unlock()
	}
	c.station.HandleASDU(s, a)
}

func (c *counted) count(ioa asdu.IOA) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.runs[ioa]
}

// hostileServer starts an IEC 101 server on one end of a line and returns
// a raw primary on the other.
func hostileServer(t *testing.T, mode iec101.Mode, mod func(*server.Config)) (*server.Server, *serialline.RawPrimary, *counted, func()) {
	t.Helper()
	check := serialline.Goroutines(t)
	a, b := serialline.Pair()
	st := newCounted()
	cfg := server.Config{Params: params(mode), LinkAddr: 3, Remote: 1, CommonAddr: ca, Handler: st,
		RetryInterval: 50 * time.Millisecond}
	if mod != nil {
		mod(&cfg)
	}
	s, err := server.New(a, cfg)
	if err != nil {
		t.Fatal(err)
	}
	peer := serialline.NewPeer(t, b, ft12.Params{AddrSize: 1})
	rp := serialline.NewRawPrimary(peer, 3)
	rp.Timeout = 200 * time.Millisecond
	done := func() {
		t.Helper()
		if err := serialline.Within(t, closeBound, "server Close", s.Close); err != nil {
			t.Errorf("close: %v", err)
		}
		_ = b.Close()
		check()
	}
	return s, rp, st, done
}

func encode(t testing.TB, a *asdu.ASDU) []byte {
	t.Helper()
	raw, err := a.Encode(iec101.DefaultParams().ASDU)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func decode(t testing.TB, raw []byte) *asdu.ASDU {
	t.Helper()
	a, err := asdu.Decode(raw, iec101.DefaultParams().ASDU)
	if err != nil {
		t.Fatalf("station sent an undecodable ASDU % X: %v", raw, err)
	}
	return a
}

// legitimate checks that the server serves a primary that behaves: it
// initializes the link and runs an interrogation to its termination.
func legitimate(t *testing.T, rp *serialline.RawPrimary) {
	t.Helper()
	time.Sleep(50 * time.Millisecond) // late answers to what came before
	rp.Drain()
	rp.Init()
	rp.PollAll(4096)
	rp.Expect(link.FuncUserDataConf, encode(t, asdu.New(asdu.CauseActivation, ca, asdu.Interrogation{Qualifier: asdu.QOIStation})), 0, -1)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		d, _ := rp.Poll(1)
		if d == nil {
			time.Sleep(5 * time.Millisecond)
			continue
		}
		if a := decode(t, d); a.Type == asdu.C_IC_NA_1 && a.Cause == asdu.CauseActivationTerm {
			return
		}
	}
	t.Fatal("no activation termination of the interrogation")
}

// frame returns the octets of a primary message to address addr.
func frame(t testing.TB, fn byte, fcb, fcv bool, addr uint16, data []byte) []byte {
	t.Helper()
	f := serialline.Fixed(serialline.Primary(fn, fcb, fcv), addr)
	if data != nil {
		f = serialline.Variable(f.Control, addr, data)
	}
	b, err := f.Encode(ft12.Params{AddrSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestHostileServer(t *testing.T) {
	giReq := func(t *testing.T) []byte {
		return encode(t, asdu.New(asdu.CauseActivation, ca, asdu.Interrogation{Qualifier: asdu.QOIStation}))
	}
	cases := []struct {
		name string
		mod  func(*server.Config)
		run  func(t *testing.T, s *server.Server, rp *serialline.RawPrimary, st *counted)
	}{
		{"line noise between frames", nil, func(t *testing.T, _ *server.Server, rp *serialline.RawPrimary, _ *counted) {
			rng := rand.New(rand.NewSource(1))
			for range 16 {
				noise := make([]byte, 256)
				rng.Read(noise)
				rp.MustRaw(noise)
				rp.MustRaw(frame(t, link.FuncRequestStatus, false, false, 3, nil))
			}
			legitimate(t, rp)
		}},
		{"truncated frames", nil, func(t *testing.T, _ *server.Server, rp *serialline.RawPrimary, _ *counted) {
			full := frame(t, link.FuncUserDataConf, true, true, 3, giReq(t))
			for n := 1; n < len(full); n++ {
				rp.MustRaw(full[:n])
			}
			legitimate(t, rp)
		}},
		{"oversized and inconsistent lengths", nil, func(t *testing.T, _ *server.Server, rp *serialline.RawPrimary, st *counted) {
			rp.Init()
			// A frame as long as FT 1.2 allows carries an ASDU longer than
			// the profile's: it is acknowledged and dropped.
			big := make([]byte, ft12.Params{AddrSize: 1}.MaxData())
			copy(big, giReq(t))
			rp.Expect(link.FuncUserDataConf, big, 0, -1)
			// The two length octets disagree; the length is below C and A.
			rp.MustRaw([]byte{0x68, 0x10, 0x11, 0x68, 0x73, 0x03, 0x00, 0x00, 0x16})
			rp.MustRaw([]byte{0x68, 0x01, 0x01, 0x68, 0x73, 0x73, 0x16})
			rp.MustRaw([]byte{0x68, 0xFF, 0xFF, 0x68})
			rp.MustRaw(make([]byte, 300))
			legitimate(t, rp)
		}},
		{"bad checksum", nil, func(t *testing.T, _ *server.Server, rp *serialline.RawPrimary, st *counted) {
			b := frame(t, link.FuncRequestStatus, false, false, 3, nil)
			b[len(b)-2]++
			rp.MustRaw(b)
			rp.Silent(150*time.Millisecond, "bad checksum")
			legitimate(t, rp)
			cmd := frame(t, link.FuncUserDataConf, true, true, 3, encode(t, asdu.New(asdu.CauseActivation, ca, asdu.SingleCommand{IOA: 500, Select: true})))
			cmd[len(cmd)-2]--
			rp.MustRaw(cmd)
			rp.Silent(150*time.Millisecond, "command with a bad checksum")
			if n := st.count(500); n != 0 {
				t.Fatalf("command with a bad checksum executed %d times", n)
			}
		}},
		{"other link addresses", nil, func(t *testing.T, _ *server.Server, rp *serialline.RawPrimary, st *counted) {
			for _, addr := range []uint16{0, 2, 4, 254} {
				rp.MustRaw(frame(t, link.FuncRequestStatus, false, false, addr, nil))
				rp.MustRaw(frame(t, link.FuncResetLink, false, false, addr, nil))
			}
			// Only send/no reply goes to the broadcast address; nothing is
			// answered there.
			rp.MustRaw(frame(t, link.FuncRequestStatus, false, false, 255, nil))
			rp.MustRaw(frame(t, link.FuncResetLink, false, false, 255, nil))
			rp.Silent(150*time.Millisecond, "another address")
			legitimate(t, rp)
			rp.MustRaw(frame(t, link.FuncUserDataConf, true, true, 4, encode(t, asdu.New(asdu.CauseActivation, ca, asdu.SingleCommand{IOA: 500, Select: true}))))
			rp.Silent(150*time.Millisecond, "command to another address")
			if n := st.count(500); n != 0 {
				t.Fatalf("command to another address executed %d times", n)
			}
		}},
		{"secondary frames to a secondary", nil, func(t *testing.T, _ *server.Server, rp *serialline.RawPrimary, _ *counted) {
			for _, fn := range []byte{0, 1, 8, 9, 11, 14, 15} {
				rp.MustRaw(rp.Encode(serialline.Fixed(serialline.Secondary(fn, true, true), 3)))
			}
			rp.MustRaw([]byte{ft12.SingleChar, ft12.SingleChar})
			rp.Silent(150*time.Millisecond, "secondary frames")
			legitimate(t, rp)
		}},
		{"wrong function codes", nil, func(t *testing.T, _ *server.Server, rp *serialline.RawPrimary, st *counted) {
			// Before the reset only 9 and 0 are answered.
			rp.MustRaw(frame(t, link.FuncUserDataConf, true, true, 3, encode(t, asdu.New(asdu.CauseActivation, ca, asdu.SingleCommand{IOA: 500, Select: true}))))
			rp.MustRaw(frame(t, link.FuncRequestClass1, true, true, 3, nil))
			rp.Silent(150*time.Millisecond, "user data before the reset")
			rp.Init()
			for _, fn := range []byte{2, 5, 6, 7, 12, 13, 14, 15} {
				rp.Expect(fn, nil, int(link.FuncNotImplemented))
			}
			rp.Expect(link.FuncAccessDemand, nil, int(link.FuncStatus))
			// User data in a fixed frame.
			rp.Expect(link.FuncUserDataConf, nil, int(link.FuncNotImplemented))
			if n := st.count(500); n != 0 {
				t.Fatalf("command before the reset executed %d times", n)
			}
			legitimate(t, rp)
		}},
		{"stuck FCB and repetitions", nil, func(t *testing.T, s *server.Server, rp *serialline.RawPrimary, st *counted) {
			rp.Init()
			cmd := frame(t, link.FuncUserDataConf, true, true, 3, encode(t, asdu.New(asdu.CauseActivation, ca, asdu.SingleCommand{IOA: 500, Select: true})))
			for range 8 {
				rp.MustRaw(cmd)
				if f, err := rp.Next(time.Second); err != nil || serialline.Func(f) > 0 {
					t.Fatalf("repetition answered %v %v", f, err)
				}
			}
			waitCount(t, st, 500, 1)
			time.Sleep(50 * time.Millisecond)
			if n := st.count(500); n != 1 {
				t.Fatalf("repeated command executed %d times", n)
			}
			// The same FCB with other user data is a new message: masters
			// that never toggle the FCB exist (link.fcbState.repeated).
			other := frame(t, link.FuncUserDataConf, true, true, 3, encode(t, asdu.New(asdu.CauseActivation, ca, asdu.SingleCommand{IOA: 501, Select: true})))
			rp.MustRaw(other)
			if _, err := rp.Next(time.Second); err != nil {
				t.Fatal(err)
			}
			waitCount(t, st, 501, 1)
			// Toggling every message, the same command twice: executed twice.
			for _, fcb := range []bool{false, true} {
				rp.MustRaw(frame(t, link.FuncUserDataConf, fcb, true, 3, encode(t, asdu.New(asdu.CauseActivation, ca, asdu.SingleCommand{IOA: 502, Select: true}))))
				if _, err := rp.Next(time.Second); err != nil {
					t.Fatal(err)
				}
			}
			waitCount(t, st, 502, 2)
			legitimate(t, rp)
		}},
		{"undecodable and foreign ASDUs", nil, func(t *testing.T, _ *server.Server, rp *serialline.RawPrimary, _ *counted) {
			rp.Init()
			gi103, _ := iec103.New(iec103.CauseGI, 3, iec103.FunGlobal, iec103.InfGIEnd, iec103.GIInit{SCN: 1}).Encode()
			for _, d := range [][]byte{gi103, {0xFF}, {0, 0, 0, 0}, {byte(asdu.C_IC_NA_1), 0x01, 6}} {
				rp.Expect(link.FuncUserDataConf, d, 0, -1)
			}
			legitimate(t, rp)
		}},
		{"malformed ASDUs at every length", func(c *server.Config) { c.QueueSize = 64 }, func(t *testing.T, s *server.Server, rp *serialline.RawPrimary, _ *counted) {
			rp.Init()
			rng := rand.New(rand.NewSource(2))
			valid := encode(t, asdu.New(asdu.CauseActivation, ca, asdu.SingleCommand{IOA: 500, Select: true}))
			max := ft12.Params{AddrSize: 1}.MaxData()
			for n := 1; n <= max; n++ {
				d := make([]byte, n)
				rng.Read(d)
				copy(d, valid)
				rp.Expect(link.FuncUserDataConf, d, 0, 1, -1)
				if n%16 == 0 {
					rp.PollAll(4096)
				}
				if p := s.Pending(server.Class1); p > 64 {
					t.Fatalf("class 1 queue %d beyond its bound", p)
				}
			}
			for n := 1; n < len(valid); n++ {
				rp.Expect(link.FuncUserDataConf, valid[:n], 0, 1, -1)
			}
			legitimate(t, rp)
		}},
		{"request flood keeps the queues bounded", func(c *server.Config) { c.QueueSize = 32 }, func(t *testing.T, s *server.Server, rp *serialline.RawPrimary, _ *counted) {
			rp.Init()
			nacks := 0
			for range 500 {
				a := rp.Expect(link.FuncUserDataConf, giReq(t), 0, 1, -1)
				if serialline.Func(a) == 1 {
					nacks++
				}
				if p := s.Pending(server.Class1); p > 32 {
					t.Fatalf("class 1 queue %d beyond its bound", p)
				}
			}
			var ms runtime.MemStats
			runtime.ReadMemStats(&ms)
			t.Logf("%d NACK, heap %d KiB", nacks, ms.HeapAlloc>>10)
			legitimate(t, rp)
		}},
		{"half-open line", nil, func(t *testing.T, _ *server.Server, rp *serialline.RawPrimary, _ *counted) {
			full := frame(t, link.FuncUserDataConf, true, true, 3, giReq(t))
			rp.MustRaw(full[:7])
			rp.Silent(300*time.Millisecond, "half a frame")
			legitimate(t, rp)
		}},
		{"half-open line with the longest length", nil, func(t *testing.T, _ *server.Server, rp *serialline.RawPrimary, _ *counted) {
			// A start of a frame of 261 octets, then nothing: without the
			// frame gap the station took the next 52 requests of status of
			// link as the rest of it.
			rp.MustRaw([]byte{0x68, 0xFF, 0xFF, 0x68, 0x73, 0x03})
			time.Sleep(250 * time.Millisecond)
			legitimate(t, rp)
		}},
		{"slow drip", nil, func(t *testing.T, _ *server.Server, rp *serialline.RawPrimary, _ *counted) {
			for _, c := range frame(t, link.FuncRequestStatus, false, false, 3, nil) {
				rp.MustRaw([]byte{c})
				time.Sleep(30 * time.Millisecond)
			}
			f, err := rp.Next(time.Second)
			if err != nil || serialline.Func(f) != int(link.FuncStatus) {
				t.Fatalf("dripped request status answered %v %v", f, err)
			}
			legitimate(t, rp)
		}},
		{"reset in the middle of an exchange", nil, func(t *testing.T, s *server.Server, rp *serialline.RawPrimary, _ *counted) {
			rp.Init()
			rp.PollAll(16)
			rp.Expect(link.FuncUserDataConf, giReq(t), 0, -1)
			time.Sleep(50 * time.Millisecond)
			rp.Poll(1)
			// Reset of remote link keeps the queued data; reset of user
			// process discards it and announces end of initialization.
			rp.Init()
			rp.Expect(link.FuncResetUser, nil, 0, -1)
			got := rp.PollAll(64)
			if len(got) == 0 || decode(t, got[len(got)-1]).Type != asdu.M_EI_NA_1 {
				t.Fatalf("after reset of user process: %d ASDUs", len(got))
			}
			legitimate(t, rp)
		}},
		{"peer vanishes mid-frame", nil, func(t *testing.T, s *server.Server, rp *serialline.RawPrimary, _ *counted) {
			rp.Init()
			rp.MustRaw(frame(t, link.FuncUserDataConf, true, true, 3, giReq(t))[:6])
			_ = rp.End.Close()
			select {
			case <-s.Done():
			case <-time.After(closeBound):
				t.Fatal("server did not stop when the line closed")
			}
			if s.Err() == nil {
				t.Fatal("no transport error")
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, rp, st, done := hostileServer(t, iec101.ModeUnbalanced, tc.mod)
			tc.run(t, s, rp, st)
			done()
		})
	}
}

func waitCount(t *testing.T, st *counted, ioa asdu.IOA, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for st.count(ioa) < want && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if n := st.count(ioa); n != want {
		t.Fatalf("command %d executed %d times, want %d", ioa, n, want)
	}
}

// TestHostileBalancedServer plays a balanced peer that misbehaves against
// the balanced server, which is primary and secondary at once.
func TestHostileBalancedServer(t *testing.T) {
	behave := func(rs *serialline.RawSecondary) {}
	cases := []struct {
		name string
		hook func(rs *serialline.RawSecondary)
		run  func(t *testing.T, s *server.Server, rp *serialline.RawPrimary, rs *serialline.RawSecondary, st *counted)
	}{
		{"silent peer", func(rs *serialline.RawSecondary) {
			rs.Hook = func(ft12.Frame, [][]byte) [][]byte { return nil }
		}, func(t *testing.T, s *server.Server, _ *serialline.RawPrimary, _ *serialline.RawSecondary, _ *counted) {
			time.Sleep(500 * time.Millisecond)
			if s.State() == iec101.StateUp {
				t.Fatal("up without a peer")
			}
		}},
		{"peer answers DFC forever", func(rs *serialline.RawSecondary) {
			rs.Hook = func(f ft12.Frame, a [][]byte) [][]byte {
				if serialline.Func(f) == int(link.FuncRequestStatus) {
					return [][]byte{rs.Encode(serialline.Fixed(serialline.Secondary(link.FuncStatus, false, true), 3))}
				}
				return a
			}
		}, func(t *testing.T, s *server.Server, _ *serialline.RawPrimary, _ *serialline.RawSecondary, _ *counted) {
			time.Sleep(700 * time.Millisecond)
			if s.State() == iec101.StateUp {
				t.Fatal("up while the peer holds DFC")
			}
		}},
		{"responses nobody asked for", behave, func(t *testing.T, s *server.Server, rp *serialline.RawPrimary, _ *serialline.RawSecondary, _ *counted) {
			for _, fn := range []byte{0, 1, 8, 9, 11, 14, 15} {
				rp.MustRaw(rp.Encode(serialline.Fixed(serialline.Secondary(fn, true, true), 3)))
				rp.MustRaw(rp.Encode(serialline.Fixed(serialline.Secondary(fn, false, false), 1)))
			}
			rp.MustRaw([]byte{ft12.SingleChar})
			waitUp(t, s)
		}},
		{"noise, stuck FCB and wrong functions", behave, func(t *testing.T, s *server.Server, rp *serialline.RawPrimary, _ *serialline.RawSecondary, st *counted) {
			waitUp(t, s)
			rng := rand.New(rand.NewSource(3))
			noise := make([]byte, 2048)
			rng.Read(noise)
			rp.MustRaw(noise)
			rp.Init()
			for _, fn := range []byte{5, 6, 7, 8, 10, 11, 12, 13} {
				rp.Expect(fn, nil, int(link.FuncNotImplemented))
			}
			rp.Expect(link.FuncTestLink, nil, 0, -1)
			cmd := frame(t, link.FuncUserDataConf, true, true, 3, encode(t, asdu.New(asdu.CauseActivation, ca, asdu.SingleCommand{IOA: 500, Select: true})))
			for range 5 {
				rp.MustRaw(cmd)
				time.Sleep(20 * time.Millisecond)
			}
			waitCount(t, st, 500, 1)
			time.Sleep(50 * time.Millisecond)
			if n := st.count(500); n != 1 {
				t.Fatalf("repeated command executed %d times", n)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, rp, st, done := hostileServer(t, iec101.ModeBalanced, func(c *server.Config) {
				p := c.Params
				p.Link.SenderAddr = false
				c.Params = p
			})
			// The server's primary messages carry the peer's address 1;
			// the peer answers them as a secondary, and sends its own
			// primary messages to 3.
			rp.AnyAddr = true
			rs := serialline.NewRawSecondary(rp.Peer, 1)
			tc.hook(rs)
			rs.Start()
			tc.run(t, s, rp, rs, st)
			done()
		})
	}
}

func waitUp(t *testing.T, s *server.Server) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for s.State() != iec101.StateUp && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if s.State() != iec101.StateUp {
		t.Fatal("balanced server did not come up")
	}
}

// hostileClient starts an IEC 101 client against a raw secondary that
// serves the e2e station's answers through OnUserData.
func hostileClient(t *testing.T, timeout time.Duration, hook func(rs *serialline.RawSecondary)) (*client.Client, *serialline.RawSecondary, chan *asdu.ASDU, func()) {
	t.Helper()
	check := serialline.Goroutines(t)
	a, b := serialline.Pair()
	peer := serialline.NewPeer(t, b, ft12.Params{AddrSize: 1})
	rs := serialline.NewRawSecondary(peer, 3)
	rs.OnUserData = func(d []byte) [][]byte {
		req, err := asdu.Decode(d, iec101.DefaultParams().ASDU)
		if err != nil {
			return nil
		}
		var out [][]byte
		switch req.Type {
		case asdu.C_IC_NA_1:
			out = append(out, encode(t, req.Reply(asdu.CauseActivationCon, false)),
				encode(t, asdu.New(asdu.CauseInterrogatedStation, ca, asdu.SinglePoint{IOA: 100, Value: true})),
				encode(t, req.Reply(asdu.CauseActivationTerm, false)))
		default:
			out = append(out, encode(t, req.Reply(asdu.CauseActivationCon, false)))
		}
		return out
	}
	if hook != nil {
		hook(rs)
	}
	rs.Start()
	got := make(chan *asdu.ASDU, 64)
	p := params(iec101.ModeUnbalanced)
	c, err := client.New(a, client.Config{Params: p, LinkAddr: 3, PollInterval: 10 * time.Millisecond,
		RetryInterval: 50 * time.Millisecond, RequestTimeout: timeout,
		Handler: func(a *asdu.ASDU) {
			select {
			case got <- a:
			default:
			}
		}})
	if err != nil {
		t.Fatal(err)
	}
	done := func() {
		t.Helper()
		if err := serialline.Within(t, closeBound, "client Close", c.Close); err != nil {
			t.Errorf("close: %v", err)
		}
		_ = b.Close()
		check()
	}
	return c, rs, got, done
}

// usable checks that the client runs an interrogation to its end.
func usable(t *testing.T, c *client.Client) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.WaitUp(ctx); err != nil {
		t.Fatalf("link not up again: %v", err)
	}
	for {
		data, err := c.Interrogate(ctx, ca, asdu.QOIStation)
		if err == nil && len(data) == 1 {
			return
		}
		if ctx.Err() != nil {
			t.Fatalf("interrogation: %v %v", data, err)
		}
	}
}

func TestHostileClient(t *testing.T) {
	const reqTimeout = 400 * time.Millisecond
	cases := []struct {
		name string
		hook func(rs *serialline.RawSecondary, m *sync.Mutex, bad *bool)
		// down: the link does not come up while the peer misbehaves.
		down bool
		// pause: the client polls at the poll interval, not as fast as the
		// peer answers.
		pause bool
	}{
		{"silent station", func(rs *serialline.RawSecondary, m *sync.Mutex, bad *bool) {
			rs.Hook = misbehave(m, bad, func(ft12.Frame, [][]byte) [][]byte { return nil })
		}, true, true},
		{"noise for answers", func(rs *serialline.RawSecondary, m *sync.Mutex, bad *bool) {
			rng := rand.New(rand.NewSource(4))
			rs.Hook = misbehave(m, bad, func(ft12.Frame, [][]byte) [][]byte {
				b := make([]byte, 1+rng.Intn(40))
				rng.Read(b)
				for i := range b {
					if b[i] == ft12.SingleChar {
						b[i] = 0
					}
				}
				return [][]byte{b}
			})
		}, true, true},
		{"answers from another address", func(rs *serialline.RawSecondary, m *sync.Mutex, bad *bool) {
			rs.Hook = misbehave(m, bad, func(f ft12.Frame, a [][]byte) [][]byte {
				out := [][]byte{}
				for _, b := range a {
					if len(b) > 2 && b[0] == ft12.StartFixed {
						b = rs.Encode(serialline.Fixed(b[1], 4))
					}
					out = append(out, b)
				}
				return out
			})
		}, true, true},
		{"wrong functions for answers", func(rs *serialline.RawSecondary, m *sync.Mutex, bad *bool) {
			rs.Hook = misbehave(m, bad, func(f ft12.Frame, _ [][]byte) [][]byte {
				// Status of link to everything, ACK to request status.
				fn := link.FuncStatus
				if serialline.Func(f) == int(link.FuncRequestStatus) {
					fn = link.FuncAck
				}
				return [][]byte{rs.Encode(serialline.Fixed(serialline.Secondary(fn, false, false), 3))}
			})
		}, true, true},
		{"truncated answers", func(rs *serialline.RawSecondary, m *sync.Mutex, bad *bool) {
			rs.Hook = misbehave(m, bad, func(_ ft12.Frame, a [][]byte) [][]byte {
				out := [][]byte{}
				for _, b := range a {
					out = append(out, b[:len(b)/2])
				}
				return out
			})
		}, true, true},
		{"slow drip beyond the time-out", func(rs *serialline.RawSecondary, m *sync.Mutex, bad *bool) {
			rs.Hook = misbehave(m, bad, func(_ ft12.Frame, a [][]byte) [][]byte {
				for _, b := range a {
					for _, c := range b {
						_ = rs.Raw([]byte{c})
						time.Sleep(25 * time.Millisecond)
					}
				}
				return nil
			})
		}, false, false},
		{"DFC forever", func(rs *serialline.RawSecondary, m *sync.Mutex, bad *bool) {
			rs.Hook = misbehave(m, bad, func(f ft12.Frame, a [][]byte) [][]byte {
				return [][]byte{rs.Encode(serialline.Fixed(serialline.Secondary(link.FuncStatus, false, true), 3))}
			})
		}, true, true},
		{"ACD flood without data", func(rs *serialline.RawSecondary, m *sync.Mutex, bad *bool) {
			rs.Hook = misbehave(m, bad, func(f ft12.Frame, a [][]byte) [][]byte {
				if fn := serialline.Func(f); fn == 10 || fn == 11 {
					return [][]byte{rs.Encode(serialline.Fixed(serialline.Secondary(link.FuncNoData, true, false), 3))}
				}
				return a
			})
		}, false, false},
		{"class 1 flood", func(rs *serialline.RawSecondary, m *sync.Mutex, bad *bool) {
			var n int
			rs.Hook = misbehave(m, bad, func(f ft12.Frame, a [][]byte) [][]byte {
				if fn := serialline.Func(f); fn == 10 || fn == 11 {
					n++
					d := encodeFlood(n)
					return [][]byte{rs.Encode(serialline.Variable(serialline.Secondary(link.FuncUserData, true, false), 3, d))}
				}
				return a
			})
		}, false, false},
		{"undecodable and foreign user data", func(rs *serialline.RawSecondary, m *sync.Mutex, bad *bool) {
			rng := rand.New(rand.NewSource(5))
			gi103, _ := iec103.New(iec103.CauseGI, 3, iec103.FunGlobal, iec103.InfGIEnd, iec103.GITermination{SCN: 1}).Encode()
			rs.Hook = misbehave(m, bad, func(f ft12.Frame, a [][]byte) [][]byte {
				if fn := serialline.Func(f); fn == 10 || fn == 11 {
					d := gi103
					if rng.Intn(2) == 0 {
						d = make([]byte, 1+rng.Intn(240))
						rng.Read(d)
					}
					return [][]byte{rs.Encode(serialline.Variable(serialline.Secondary(link.FuncUserData, true, false), 3, d))}
				}
				return a
			})
		}, false, false},
		{"answers to requests never sent", func(rs *serialline.RawSecondary, m *sync.Mutex, bad *bool) {
			rs.Hook = misbehave(m, bad, func(f ft12.Frame, a [][]byte) [][]byte {
				stray := [][]byte{{ft12.SingleChar}, rs.Encode(serialline.Fixed(serialline.Secondary(link.FuncAck, false, false), 3)),
					rs.Encode(serialline.Fixed(serialline.Secondary(link.FuncStatus, false, false), 3)),
					rs.Encode(serialline.Variable(serialline.Secondary(link.FuncUserData, false, false), 3,
						encode(t, asdu.New(asdu.CauseActivationCon, ca, asdu.SingleCommand{IOA: 77}))))}
				return append(stray, a...)
			})
		}, false, true},
		{"not functioning", func(rs *serialline.RawSecondary, m *sync.Mutex, bad *bool) {
			rs.Hook = misbehave(m, bad, func(f ft12.Frame, a [][]byte) [][]byte {
				if fn := serialline.Func(f); fn == 10 || fn == 11 || fn == 3 {
					return [][]byte{rs.Encode(serialline.Fixed(serialline.Secondary(link.FuncNotFunctioning, false, false), 3))}
				}
				return a
			})
		}, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			bad := false
			c, rs, _, done := hostileClient(t, reqTimeout, func(rs *serialline.RawSecondary) { tc.hook(rs, &mu, &bad) })
			usable(t, c)
			mu.Lock()
			bad = true
			mu.Unlock()
			ctx := context.Background()
			for range 3 {
				start := time.Now()
				err := serialline.Within(t, reqTimeout+closeBound, "interrogation", func() error {
					_, err := c.Interrogate(ctx, ca, asdu.QOIStation)
					return err
				})
				if err == nil && tc.down {
					t.Fatal("interrogation answered by a station that does not")
				}
				_ = serialline.Within(t, reqTimeout+closeBound, "command", func() error {
					return c.Command(ctx, ca, asdu.SingleCommand{IOA: 500, Select: true})
				})
				if d := time.Since(start); d > 2*reqTimeout+300*time.Millisecond {
					t.Errorf("two requests with a time-out of %v took %v", reqTimeout, d)
				}
			}
			if tc.down {
				time.Sleep(300 * time.Millisecond)
				if c.State() == iec101.StateUp {
					t.Fatal("link still up")
				}
				if _, err := c.Interrogate(ctx, ca, asdu.QOIStation); !errors.Is(err, iec101.ErrLinkDown) {
					t.Fatalf("request on a link that is down: %v", err)
				}
			}
			var ms runtime.MemStats
			runtime.ReadMemStats(&ms)
			if ms.HeapAlloc > 256<<20 {
				t.Fatalf("heap %d MiB", ms.HeapAlloc>>20)
			}
			n0 := rs.Polls()
			time.Sleep(200 * time.Millisecond)
			if rate := rs.Polls() - n0; tc.pause && rate > 200 {
				// PollInterval is 10 ms: a client that polls without pause
				// on a fast line hammers the station.
				t.Errorf("%d polls in 200 ms", rate)
			}
			mu.Lock()
			bad = false
			mu.Unlock()
			usable(t, c)
			done()
		})
	}
}

// misbehave returns a hook that applies bad while *on is set and answers
// as the station would otherwise.
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

func encodeFlood(n int) []byte {
	raw, _ := asdu.New(asdu.CauseSpontaneous, ca, asdu.MeasuredScaled{IOA: asdu.IOA(n % 1000), Value: int16(n)}).Encode(iec101.DefaultParams().ASDU)
	return raw
}
