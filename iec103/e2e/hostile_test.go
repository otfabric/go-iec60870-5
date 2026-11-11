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
	"github.com/otfabric/go-iec60870-5/cp"
	"github.com/otfabric/go-iec60870-5/ft12"
	"github.com/otfabric/go-iec60870-5/iec102"
	"github.com/otfabric/go-iec60870-5/iec103"
	"github.com/otfabric/go-iec60870-5/iec103/client"
	"github.com/otfabric/go-iec60870-5/iec103/server"
	"github.com/otfabric/go-iec60870-5/internal/testutil/serialline"
	"github.com/otfabric/go-iec60870-5/link"
)

// The hostile tests put a peer that does not follow IEC 60870-5-2 and
// clause 6 of IEC 60870-5-103 on the other end of an in-memory line and
// write its frames as raw octets, as the IEC 101 suite does: whatever it
// sends, the station under test must not panic, must stay silent or
// answer as the procedures say, keep its queues bounded, serve a peer that
// behaves afterwards, close within a bound and leave no goroutine behind.

const closeBound = 2 * time.Second

var p1 = ft12.Params{AddrSize: 1}

// commands counts the general commands the handler executed, per RII, and
// answers general interrogations with one indication.
type commands struct {
	mu   sync.Mutex
	runs map[uint8]int
	slow time.Duration
}

func (h *commands) HandleASDU(s *server.Server, a *iec103.ASDU) {
	if h.slow > 0 {
		time.Sleep(h.slow)
	}
	switch b := a.Body.(type) {
	case iec103.GeneralCommand:
		h.mu.Lock()
		h.runs[b.RII]++
		h.mu.Unlock()
		_ = s.AckCommand(a, true)
	case iec103.GIInit:
		_ = s.AnswerGI(a, iec103.New(iec103.CauseGI, s.CommonAddr(), fun, 16, iec103.TimeTagged{DPI: iec103.DPIOn, Time: s.Now()}))
	}
}

func (h *commands) count(rii uint8) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.runs[rii]
}

func waitRuns(t *testing.T, h *commands, rii uint8, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for h.count(rii) < want && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(30 * time.Millisecond)
	if n := h.count(rii); n != want {
		t.Fatalf("command RII %d executed %d times, want %d", rii, n, want)
	}
}

func enc103(t testing.TB, a *iec103.ASDU) []byte {
	t.Helper()
	b, err := a.Encode()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func command(t testing.TB, rii uint8) []byte {
	return enc103(t, iec103.New(iec103.CauseGeneralCommand, 3, fun, 16, iec103.GeneralCommand{DCO: iec103.DPIOn, RII: rii}))
}

func gi(t testing.TB, scn uint8) []byte {
	return enc103(t, iec103.New(iec103.CauseGI, 3, iec103.FunGlobal, iec103.InfGIEnd, iec103.GIInit{SCN: scn}))
}

func frame(t testing.TB, fn byte, fcb, fcv bool, addr uint16, data []byte) []byte {
	t.Helper()
	f := serialline.Fixed(serialline.Primary(fn, fcb, fcv), addr)
	if data != nil {
		f = serialline.Variable(f.Control, addr, data)
	}
	b, err := f.Encode(p1)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func hostileServer(t *testing.T, h *commands, mod func(*server.Config)) (*server.Server, *serialline.RawPrimary, func()) {
	t.Helper()
	check := serialline.Goroutines(t)
	a, b := serialline.Pair()
	cfg := server.Config{Link: link.Config{Timeout: 100 * time.Millisecond}, LinkAddr: 3, Fun: fun, Handler: h,
		Identification: iec103.IdentificationOf(iec103.COLWithGeneric, "OTFABRIC", [4]byte{1, 0, 0, 0})}
	if mod != nil {
		mod(&cfg)
	}
	s, err := server.New(a, cfg)
	if err != nil {
		t.Fatal(err)
	}
	rp := serialline.NewRawPrimary(serialline.NewPeer(t, b, p1), 3)
	rp.Timeout = 200 * time.Millisecond
	return s, rp, func() {
		t.Helper()
		if err := serialline.Within(t, closeBound, "server Close", s.Close); err != nil {
			t.Errorf("close: %v", err)
		}
		_ = b.Close()
		check()
	}
}

// legitimate checks that the server serves a controlling system that
// behaves: link initialization and a general interrogation to its end.
func legitimate(t *testing.T, rp *serialline.RawPrimary) {
	t.Helper()
	time.Sleep(50 * time.Millisecond)
	rp.Drain()
	rp.Init()
	rp.PollAll(4096)
	rp.Expect(link.FuncUserDataConf, gi(t, 77), 0, -1)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		d, _ := rp.Poll(1)
		if d == nil {
			time.Sleep(5 * time.Millisecond)
			continue
		}
		a, err := iec103.Decode(d)
		if err != nil {
			t.Fatalf("station sent an undecodable ASDU % X: %v", d, err)
		}
		if e, ok := a.Body.(iec103.GITermination); ok && e.SCN == 77 {
			return
		}
	}
	t.Fatal("no GI termination")
}

func TestHostileServer(t *testing.T) {
	cases := []struct {
		name string
		slow time.Duration
		mod  func(*server.Config)
		run  func(t *testing.T, s *server.Server, rp *serialline.RawPrimary, h *commands)
	}{
		{"line noise between frames", 0, nil, func(t *testing.T, _ *server.Server, rp *serialline.RawPrimary, _ *commands) {
			rng := rand.New(rand.NewSource(1))
			for range 16 {
				noise := make([]byte, 256)
				rng.Read(noise)
				rp.MustRaw(noise)
				rp.MustRaw(frame(t, link.FuncRequestStatus, false, false, 3, nil))
			}
			legitimate(t, rp)
		}},
		{"truncated, oversized and bad checksum", 0, nil, func(t *testing.T, _ *server.Server, rp *serialline.RawPrimary, h *commands) {
			full := frame(t, link.FuncUserDataConf, true, true, 3, command(t, 1))
			for n := 1; n < len(full); n++ {
				rp.MustRaw(full[:n])
			}
			bad := append([]byte(nil), full...)
			bad[len(bad)-2]++
			rp.MustRaw(bad)
			rp.MustRaw([]byte{0x68, 0x10, 0x11, 0x68, 0x73, 0x03, 0x00, 0x16})
			rp.Silent(150*time.Millisecond, "broken frames")
			legitimate(t, rp)
			rp.Expect(link.FuncUserDataConf, make([]byte, p1.MaxData()), 0, -1)
			if n := h.count(1); n != 0 {
				t.Fatalf("broken command executed %d times", n)
			}
		}},
		{"other addresses and the broadcast address", 0, nil, func(t *testing.T, _ *server.Server, rp *serialline.RawPrimary, h *commands) {
			for _, addr := range []uint16{0, 2, 4, 254} {
				rp.MustRaw(frame(t, link.FuncRequestStatus, false, false, addr, nil))
				rp.MustRaw(frame(t, link.FuncUserDataConf, false, true, addr, command(t, 2)))
			}
			for _, fn := range []byte{0, 3, 7, 9, 10, 11} {
				rp.MustRaw(frame(t, fn, false, fn == 3 || fn == 10 || fn == 11, 255, nil))
			}
			rp.MustRaw(frame(t, link.FuncUserDataConf, false, true, 255, command(t, 2)))
			rp.Silent(150*time.Millisecond, "another address")
			legitimate(t, rp)
			if n := h.count(2); n != 0 {
				t.Fatalf("command to another address executed %d times", n)
			}
			// Broadcast user data without reply, also garbage.
			rp.MustRaw(frame(t, link.FuncUserDataNoRepl, false, false, 255, []byte{0xFF, 0x81}))
			rp.MustRaw(frame(t, link.FuncUserDataNoRepl, false, false, 255,
				enc103(t, iec103.New(iec103.CauseTimeSync, iec103.CommonAddrGlobal, iec103.FunGlobal, iec103.InfTimeSync, iec103.TimeSync{Time: cp.At(time.Now())}))))
			rp.Silent(150*time.Millisecond, "broadcast")
			legitimate(t, rp)
		}},
		{"secondary frames and wrong function codes", 0, nil, func(t *testing.T, _ *server.Server, rp *serialline.RawPrimary, h *commands) {
			for _, fn := range []byte{0, 1, 8, 9, 11, 14, 15} {
				rp.MustRaw(rp.Encode(serialline.Fixed(serialline.Secondary(fn, true, true), 3)))
			}
			rp.MustRaw([]byte{ft12.SingleChar})
			rp.MustRaw(frame(t, link.FuncUserDataConf, true, true, 3, command(t, 3)))
			rp.Silent(150*time.Millisecond, "secondary frames, user data before the reset")
			// Clause 6.2.3: the functions IEC 60870-5-103 does not use are
			// answered with function 15, before the reset too.
			for _, fn := range []byte{1, 2, 5, 6, 8, 12, 13, 14, 15} {
				rp.Expect(fn, nil, int(link.FuncNotImplemented))
			}
			rp.Init()
			rp.Expect(link.FuncResetFCB, nil, 0, -1)
			rp.SetFCB(false)
			rp.Expect(link.FuncUserDataConf, nil, int(link.FuncNotImplemented))
			if n := h.count(3); n != 0 {
				t.Fatalf("command before the reset executed %d times", n)
			}
			legitimate(t, rp)
		}},
		{"stuck FCB and repetitions", 0, nil, func(t *testing.T, _ *server.Server, rp *serialline.RawPrimary, h *commands) {
			rp.Init()
			cmd := frame(t, link.FuncUserDataConf, true, true, 3, command(t, 4))
			for range 8 {
				rp.MustRaw(cmd)
				if f, err := rp.Next(time.Second); err != nil || serialline.Func(f) > 0 {
					t.Fatalf("repetition answered %v %v", f, err)
				}
			}
			waitRuns(t, h, 4, 1)
			rp.PollAll(64)
			rp.MustRaw(frame(t, link.FuncUserDataConf, true, true, 3, command(t, 5)))
			if _, err := rp.Next(time.Second); err != nil {
				t.Fatal(err)
			}
			waitRuns(t, h, 5, 1)
			legitimate(t, rp)
		}},
		{"foreign and malformed ASDUs at every length", 0, nil, func(t *testing.T, _ *server.Server, rp *serialline.RawPrimary, _ *commands) {
			rp.Init()
			gi101, _ := asdu.New(asdu.CauseActivation, 3, asdu.Interrogation{Qualifier: asdu.QOIStation}).Encode(asdu.Params{CauseSize: 1, CommonAddrSize: 1, IOASize: 2})
			ti102, _ := iec102.New(iec102.CauseRequest, 3, 0, iec102.Read{T: iec102.C_TI_NA_2}).Encode(iec102.Params{})
			for _, d := range [][]byte{gi101, ti102} {
				rp.Expect(link.FuncUserDataConf, d, 0, 1, -1)
			}
			rng := rand.New(rand.NewSource(2))
			templates := [][]byte{command(t, 9), gi(t, 9),
				enc103(t, iec103.New(iec103.CauseTimeSync, 3, iec103.FunGlobal, iec103.InfTimeSync, iec103.TimeSync{Time: cp.At(time.Now())})),
				enc103(t, iec103.New(iec103.CauseGenericRead, 3, iec103.FunGeneric, iec103.InfReadEntry, iec103.GenericCommand{RII: 1}))}
			for _, too := range []iec103.TOO{iec103.TOOSelectFault, iec103.TOORequestData, iec103.TOORequestTags, iec103.TOORequestChannel, iec103.TOORequestList} {
				templates = append(templates, enc103(t, iec103.New(iec103.CauseDisturbanceData, 3, fun, 0, iec103.DisturbanceOrder{TOO: too, FAN: 1, ACC: 1})))
			}
			for n := 1; n <= p1.MaxData(); n++ {
				for _, tmpl := range templates {
					d := make([]byte, n)
					rng.Read(d)
					copy(d, tmpl[:min(len(tmpl), n)])
					rp.Expect(link.FuncUserDataConf, d, 0, 1, -1)
				}
				if n%8 == 0 {
					rp.PollAll(4096)
				}
			}
			legitimate(t, rp)
		}},
		{"command flood with a slow handler", 5 * time.Millisecond, func(c *server.Config) { c.QueueSize = 32 }, func(t *testing.T, s *server.Server, rp *serialline.RawPrimary, _ *commands) {
			rp.Init()
			nacks := 0
			for i := range 400 {
				a := rp.Expect(link.FuncUserDataConf, command(t, uint8(i)), 0, 1, -1)
				if serialline.Func(a) == 1 {
					nacks++
				}
				if p := s.Pending(server.Class1); p > 32 {
					t.Fatalf("class 1 queue %d beyond its bound", p)
				}
			}
			if nacks == 0 {
				t.Error("a flood of commands to a slow handler was never refused with DFC")
			}
			legitimate(t, rp)
		}},
		{"half-open line with the longest length", 0, nil, func(t *testing.T, _ *server.Server, rp *serialline.RawPrimary, _ *commands) {
			rp.MustRaw([]byte{0x68, 0xFF, 0xFF, 0x68, 0x73, 0x03})
			time.Sleep(250 * time.Millisecond)
			legitimate(t, rp)
		}},
		{"slow drip", 0, nil, func(t *testing.T, _ *server.Server, rp *serialline.RawPrimary, _ *commands) {
			for _, c := range frame(t, link.FuncRequestStatus, false, false, 3, nil) {
				rp.MustRaw([]byte{c})
				time.Sleep(30 * time.Millisecond)
			}
			if f, err := rp.Next(time.Second); err != nil || serialline.Func(f) != int(link.FuncStatus) {
				t.Fatalf("dripped request status answered %v %v", f, err)
			}
			legitimate(t, rp)
		}},
		{"reset in the middle of a general interrogation", 0, nil, func(t *testing.T, _ *server.Server, rp *serialline.RawPrimary, _ *commands) {
			rp.Init()
			rp.PollAll(64)
			rp.Expect(link.FuncUserDataConf, gi(t, 5), 0, -1)
			time.Sleep(50 * time.Millisecond)
			rp.Poll(1)
			// Reset CU clears the queues; the identification comes first.
			rp.Init()
			got := rp.PollAll(64)
			if len(got) != 1 {
				t.Fatalf("%d ASDUs after reset CU, want the identification", len(got))
			}
			if a, err := iec103.Decode(got[0]); err != nil || a.Type != iec103.TypeIdentification {
				t.Fatalf("after reset CU: %v %v", a, err)
			}
			legitimate(t, rp)
		}},
		{"peer vanishes mid-frame", 0, nil, func(t *testing.T, s *server.Server, rp *serialline.RawPrimary, _ *commands) {
			rp.Init()
			rp.MustRaw(frame(t, link.FuncUserDataConf, true, true, 3, gi(t, 1))[:6])
			_ = rp.End.Close()
			time.Sleep(50 * time.Millisecond)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := &commands{runs: map[uint8]int{}, slow: tc.slow}
			s, rp, done := hostileServer(t, h, tc.mod)
			tc.run(t, s, rp, h)
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

// rawEquipment is a raw secondary that answers time synchronization,
// general interrogation and general commands the way protection equipment
// does.
func rawEquipment(t *testing.T, b *serialline.End) *serialline.RawSecondary {
	rs := serialline.NewRawSecondary(serialline.NewPeer(t, b, p1), 3)
	rs.OnUserData = func(d []byte) [][]byte {
		a, err := iec103.Decode(d)
		if err != nil {
			return nil
		}
		var out *iec103.ASDU
		switch x := a.Body.(type) {
		case iec103.TimeSync:
			out = iec103.New(iec103.CauseTimeSync, 3, iec103.FunGlobal, iec103.InfTimeSync, iec103.TimeSync{Time: x.Time})
		case iec103.GIInit:
			out = iec103.New(iec103.CauseGITermination, 3, iec103.FunGlobal, iec103.InfGIEnd, iec103.GITermination(x))
		case iec103.GeneralCommand:
			out = iec103.New(iec103.CauseCommandAck, 3, a.Fun, a.Inf, iec103.TimeTagged{DPI: x.DCO, SIN: x.RII, Time: cp.At(time.Now())})
		default:
			return nil
		}
		b, err := out.Encode()
		if err != nil {
			return nil
		}
		return [][]byte{b}
	}
	return rs
}

func TestHostileClient(t *testing.T) {
	const reqTimeout = 400 * time.Millisecond
	user := func(fn int) bool { return fn == 10 || fn == 11 }
	data := func(rs *serialline.RawSecondary, acd bool, a *iec103.ASDU) [][]byte {
		b, _ := a.Encode()
		return [][]byte{rs.Encode(serialline.Variable(serialline.Secondary(link.FuncUserData, acd, false), 3, b))}
	}
	cases := []struct {
		name string
		bad  func(rs *serialline.RawSecondary, rng *rand.Rand) func(ft12.Frame, [][]byte) [][]byte
		down bool
	}{
		{"silent equipment", func(*serialline.RawSecondary, *rand.Rand) func(ft12.Frame, [][]byte) [][]byte {
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
				return [][]byte{rs.Encode(serialline.Fixed(serialline.Secondary(fn, false, false), 3))}
			}
		}, true},
		{"DFC forever", func(rs *serialline.RawSecondary, _ *rand.Rand) func(ft12.Frame, [][]byte) [][]byte {
			return func(f ft12.Frame, a [][]byte) [][]byte {
				if serialline.Func(f) == int(link.FuncUserDataConf) {
					return [][]byte{rs.Encode(serialline.Fixed(serialline.Secondary(link.FuncNack, false, true), 3))}
				}
				if user(serialline.Func(f)) {
					return [][]byte{rs.Encode(serialline.Fixed(serialline.Secondary(link.FuncNoData, false, true), 3))}
				}
				return a
			}
		}, false},
		{"ACD flood without data", func(rs *serialline.RawSecondary, _ *rand.Rand) func(ft12.Frame, [][]byte) [][]byte {
			return func(f ft12.Frame, a [][]byte) [][]byte {
				if user(serialline.Func(f)) {
					return [][]byte{rs.Encode(serialline.Fixed(serialline.Secondary(link.FuncNoData, true, false), 3))}
				}
				return a
			}
		}, false},
		{"GI answers without end", func(rs *serialline.RawSecondary, _ *rand.Rand) func(ft12.Frame, [][]byte) [][]byte {
			var n int
			return func(f ft12.Frame, a [][]byte) [][]byte {
				if user(serialline.Func(f)) {
					n++
					return data(rs, true, iec103.New(iec103.CauseGI, 3, fun, iec103.InfoNumber(n), iec103.TimeTagged{DPI: iec103.DPIOn, SIN: uint8(n)}))
				}
				return a
			}
		}, false},
		{"acknowledgements with other RII and FUN", func(rs *serialline.RawSecondary, _ *rand.Rand) func(ft12.Frame, [][]byte) [][]byte {
			var n int
			return func(f ft12.Frame, a [][]byte) [][]byte {
				if user(serialline.Func(f)) {
					n++
					return data(rs, false, iec103.New(iec103.Cause(20+n%2), 3, iec103.FunctionType(n), 16, iec103.TimeTagged{DPI: iec103.DPIOn, SIN: uint8(n * 7)}))
				}
				return a
			}
		}, false},
		{"disturbance data out of order", func(rs *serialline.RawSecondary, rng *rand.Rand) func(ft12.Frame, [][]byte) [][]byte {
			bodies := []iec103.Body{
				iec103.EndOfTransmission{TOO: iec103.TOOEndTags, FAN: 1},
				iec103.EndOfTransmission{TOO: iec103.TOOEndDataAbortProt, FAN: 1},
				iec103.EndOfTransmission{TOO: iec103.TOOEndChannel, FAN: 1, ACC: 1},
				iec103.DisturbanceReady{FAN: 1, NOF: 1, NOC: 3, NOE: 2},
				iec103.ChannelReady{FAN: 1, ACC: 1},
				iec103.Tags{FAN: 1, TAP: 9},
				iec103.DisturbanceValues{FAN: 1, ACC: 1, NFE: 7, Values: []int16{1, 2}},
			}
			return func(f ft12.Frame, a [][]byte) [][]byte {
				if user(serialline.Func(f)) {
					return data(rs, rng.Intn(2) == 0, iec103.New(iec103.CauseDisturbanceData, 3, fun, 0, bodies[rng.Intn(len(bodies))]))
				}
				return a
			}
		}, false},
		{"undecodable and foreign user data", func(rs *serialline.RawSecondary, rng *rand.Rand) func(ft12.Frame, [][]byte) [][]byte {
			gi101, _ := asdu.New(asdu.CauseActivationCon, 3, asdu.Interrogation{Qualifier: asdu.QOIStation}).Encode(asdu.Params{CauseSize: 1, CommonAddrSize: 1, IOASize: 2})
			return func(f ft12.Frame, a [][]byte) [][]byte {
				if user(serialline.Func(f)) {
					d := gi101
					if rng.Intn(2) == 0 {
						d = make([]byte, 1+rng.Intn(240))
						rng.Read(d)
					}
					return [][]byte{rs.Encode(serialline.Variable(serialline.Secondary(link.FuncUserData, false, false), 3, d))}
				}
				return a
			}
		}, false},
		{"not functioning", func(rs *serialline.RawSecondary, _ *rand.Rand) func(ft12.Frame, [][]byte) [][]byte {
			return func(f ft12.Frame, a [][]byte) [][]byte {
				if fn := serialline.Func(f); user(fn) || fn == 3 {
					return [][]byte{rs.Encode(serialline.Fixed(serialline.Secondary(link.FuncNotFunctioning, false, false), 3))}
				}
				return a
			}
		}, false},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			check := serialline.Goroutines(t)
			a, b := serialline.Pair()
			rs := rawEquipment(t, b)
			var mu sync.Mutex
			bad := false
			rs.Hook = misbehave(&mu, &bad, tc.bad(rs, rand.New(rand.NewSource(int64(i)))))
			rs.Start()
			c, err := client.New(a, client.Config{Link: link.Config{Timeout: 50 * time.Millisecond, Retries: 2}, LinkAddr: 3,
				PollInterval: 5 * time.Millisecond, RetryInterval: 20 * time.Millisecond, RequestTimeout: reqTimeout,
				Handler: func(*iec103.ASDU) {}})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := c.WaitUp(ctx); err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			bad = true
			mu.Unlock()
			for range 2 {
				for _, req := range []func() error{
					func() error { _, err := c.Interrogate(context.Background()); return err },
					func() error { return c.Command(context.Background(), fun, 16, iec103.DPIOn) },
					func() error { _, err := c.ClockSync(context.Background(), time.Now()); return err },
					func() error { _, err := c.Disturbance(context.Background(), fun, 1); return err },
					func() error { _, err := c.DisturbanceList(context.Background(), fun); return err },
				} {
					start := time.Now()
					err := serialline.Within(t, reqTimeout+closeBound, "request", req)
					if d := time.Since(start); d > reqTimeout+300*time.Millisecond {
						t.Errorf("request with a time-out of %v took %v: %v", reqTimeout, d, err)
					}
				}
				_ = serialline.Within(t, reqTimeout+closeBound, "broadcast", func() error { return c.BroadcastClockSync(time.Now()) })
			}
			if tc.down {
				time.Sleep(200 * time.Millisecond)
				if c.State() == iec103.StateUp {
					t.Error("link still up")
				}
				if _, err := c.Interrogate(context.Background()); !errors.Is(err, iec103.ErrLinkDown) {
					t.Errorf("request on a link that is down: %v", err)
				}
			}
			var ms runtime.MemStats
			runtime.ReadMemStats(&ms)
			if ms.HeapAlloc > 256<<20 {
				t.Fatalf("heap %d MiB", ms.HeapAlloc>>20)
			}
			mu.Lock()
			bad = false
			mu.Unlock()
			for {
				if err := c.WaitUp(ctx); err != nil {
					t.Fatal(err)
				}
				if _, err := c.Interrogate(ctx); err == nil {
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

// TestBroadcastWhenTheLinkGoesDown is the regression test of a broadcast
// that waited for the link loop forever: it has no context, and the loop
// takes broadcasts only while the link is up.
func TestBroadcastWhenTheLinkGoesDown(t *testing.T) {
	check := serialline.Goroutines(t)
	a, b := serialline.Pair()
	rs := rawEquipment(t, b)
	var mu sync.Mutex
	mute := false
	rs.Hook = misbehave(&mu, &mute, func(ft12.Frame, [][]byte) [][]byte { return nil })
	rs.Start()
	c, err := client.New(a, client.Config{Link: link.Config{Timeout: 50 * time.Millisecond, Retries: 1}, LinkAddr: 3,
		PollInterval: time.Millisecond, RetryInterval: 10 * time.Second, NoInitGI: true, NoInitTimeSync: true})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.WaitUp(ctx); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	mute = true
	mu.Unlock()
	// The loop is in a poll that will time out: the broadcast waits for
	// it, and the link goes down meanwhile.
	start := time.Now()
	err = serialline.Within(t, 2*time.Second, "BroadcastClockSync", func() error {
		var err error
		for range 50 {
			if err = c.BroadcastClockSync(time.Now()); err != nil {
				return err
			}
		}
		return err
	})
	if !errors.Is(err, iec103.ErrLinkDown) {
		t.Errorf("broadcast while the link went down: %v after %v", err, time.Since(start))
	}
	if err := serialline.Within(t, closeBound, "client Close", c.Close); err != nil {
		t.Error(err)
	}
	_ = b.Close()
	check()
}
