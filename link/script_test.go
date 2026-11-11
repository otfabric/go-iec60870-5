// SPDX-License-Identifier: MIT

package link

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/otfabric/go-iec60870-5/ft12"
)

// scriptedPeer answers each frame received on conn with the frames
// answer returns.
func scriptedPeer(t *testing.T, conn net.Conn, answer func(f ft12.Frame) []ft12.Frame) {
	t.Helper()
	go func() {
		d := ft12.NewDecoder(conn, fast.FT12)
		for {
			f, err := d.Next()
			if err != nil {
				return
			}
			for _, r := range answer(f) {
				b, _ := r.Encode(fast.FT12)
				if _, err := conn.Write(b); err != nil {
					return
				}
			}
		}
	}()
}

func fixed(fn byte, addr uint16) ft12.Frame {
	return ft12.Frame{Kind: ft12.KindFixed, Addr: addr, Control: byte(SecondaryControl(fn, false, false, false))}
}

func TestPrimaryNegativeAnswers(t *testing.T) {
	a, b := net.Pipe()
	answers := map[byte]ft12.Frame{
		FuncRequestStatus: fixed(FuncAck, 1),            // wrong function
		FuncResetLink:     fixed(FuncNotFunctioning, 1), // 14
		FuncResetUser:     fixed(FuncNotImplemented, 1), // 15
		FuncUserDataConf:  fixed(FuncStatus, 1),         // wrong function
		FuncRequestClass1: fixed(FuncUserData, 1),       // user data without data
		FuncRequestClass2: fixed(FuncAck, 1),            // wrong function
		FuncResetFCB:      fixed(FuncNack, 1),
	}
	scriptedPeer(t, b, func(f ft12.Frame) []ft12.Frame {
		return []ft12.Frame{fixed(FuncAck, 2), answers[Control(f.Control).Func()]} // another station first
	})
	pr, err := NewPrimary(a, fast)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pr.Close() }()
	ctx := context.Background()
	check := func(err, want error) {
		t.Helper()
		if !errors.Is(err, want) {
			t.Errorf("%v, want %v", err, want)
		}
	}
	_, err = pr.RequestStatus(ctx, 1)
	check(err, ErrUnexpected)
	_, err = pr.ResetLink(ctx, 1)
	check(err, ErrNotFunctioning)
	_, err = pr.ResetUser(ctx, 1)
	check(err, ErrNotImplemented)
	_, err = pr.SendConfirm(ctx, 1, []byte{1})
	check(err, ErrUnexpected)
	_, _, err = pr.RequestData(ctx, 1, Class1)
	check(err, ErrUnexpected)
	_, _, err = pr.RequestData(ctx, 1, Class2)
	check(err, ErrUnexpected)
	_, err = pr.ResetFCB(ctx, 1)
	check(err, ErrNack)
	_, err = pr.Init(ctx, 1)
	check(err, ErrUnexpected)
	if _, err := pr.RequestStatus(ctx, 1); err == nil {
		t.Error("RequestStatus accepted ACK")
	}
}

func TestBalancedNegativeAnswers(t *testing.T) {
	a, b := net.Pipe()
	answers := map[byte]ft12.Frame{
		FuncRequestStatus: fixed(FuncNack, 2),
		FuncResetLink:     fixed(FuncStatus, 2),
		FuncTestLink:      {Kind: ft12.KindSingle},
		FuncUserDataConf:  fixed(FuncNotImplemented, 2),
	}
	scriptedPeer(t, b, func(f ft12.Frame) []ft12.Frame {
		return []ft12.Frame{answers[Control(f.Control).Func()]}
	})
	bal, err := NewBalanced(a, fast, BalancedConfig{Addr: 1, Remote: 2}, &peer{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = bal.Close() }()
	ctx := context.Background()
	// NACK does not answer request status of link (IEC 60870-5-101:2003 table 4).
	if _, err := bal.RequestStatus(ctx); !errors.Is(err, ErrUnexpected) {
		t.Error(err)
	}
	if err := bal.Init(ctx); !errors.Is(err, ErrUnexpected) {
		t.Error(err)
	}
	if err := bal.ResetLink(ctx); !errors.Is(err, ErrUnexpected) {
		t.Error(err)
	}
	if err := bal.Test(ctx); err != nil {
		t.Error(err)
	}
	if err := bal.Send(ctx, []byte{1}); !errors.Is(err, ErrNotImplemented) {
		t.Error(err)
	}
	answers[FuncRequestStatus] = fixed(FuncAck, 2)
	if _, err := bal.RequestStatus(ctx); !errors.Is(err, ErrUnexpected) {
		t.Error(err)
	}
}

// A scripted primary drives a secondary and a balanced station through
// the messages they must refuse or ignore.
func TestSecondaryOddMessages(t *testing.T) {
	for _, balanced := range []bool{false, true} {
		a, b := net.Pipe()
		h := &station{}
		if balanced {
			s, _ := NewBalanced(b, fast, BalancedConfig{Addr: 1, Remote: 2}, &peer{})
			defer func() { _ = s.Close() }()
		} else {
			s, _ := NewSecondary(b, fast, 1, h)
			defer func() { _ = s.Close() }()
		}
		d := ft12.NewDecoder(a, fast.FT12)
		send := func(f ft12.Frame) {
			enc, _ := f.Encode(fast.FT12)
			if _, err := a.Write(enc); err != nil {
				t.Fatal(err)
			}
		}
		expect := func(fn byte) {
			t.Helper()
			f, err := d.Next()
			if err != nil || f.Kind != ft12.KindFixed || Control(f.Control).Func() != fn {
				t.Fatalf("balanced %v: got %v %v, want function %d", balanced, f, err, fn)
			}
		}
		prim := func(fn byte, fcb bool, kind ft12.Kind) ft12.Frame {
			return ft12.Frame{Kind: kind, Addr: 1, Control: byte(PrimaryControl(fn, fcb, fcvRequired(fn), false)), Data: []byte{}}
		}
		send(ft12.Frame{Kind: ft12.KindSingle})              // ignored
		send(fixed(FuncAck, 1))                              // a secondary frame: ignored (or an orphan response)
		send(prim(13, false, ft12.KindFixed))                // before reset: no answer
		send(prim(FuncRequestStatus, false, ft12.KindFixed)) // to address 1
		expect(FuncStatus)
		send(prim(FuncResetLink, false, ft12.KindFixed))
		expect(FuncAck)
		send(prim(13, false, ft12.KindFixed)) // undefined function
		expect(FuncNotImplemented)
		send(prim(FuncUserDataConf, true, ft12.KindFixed)) // user data without data
		expect(FuncNotImplemented)
		send(prim(FuncUserDataNoRepl, false, ft12.KindVariable)) // no answer
		send(prim(FuncRequestStatus, false, ft12.KindFixed))
		expect(FuncStatus)
		_ = a.Close()
	}
}
