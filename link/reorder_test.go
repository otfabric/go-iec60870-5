// SPDX-License-Identifier: MIT

package link

import (
	"testing"

	"github.com/otfabric/go-iec60870-5/ft12"
)

// TestRepetitionAfterTheNextMessage documents the limit of the one-bit FCB
// that the chaos suites ran into (iec101 TestChaos, a 60 s soak, with frames
// reordered in the primary's direction): a repetition of a message that a
// transport delivers after the primary's next message carries the FCB the
// secondary now expects, and is a new message (IEC 60870-5-2 5.1: the FCB
// is toggled for each new message, so only an immediate repetition can be
// recognised). A serial line delivers in order; transports that may not
// need a link time-out above their worst delay.
func TestRepetitionAfterTheNextMessage(t *testing.T) {
	st := &station{}
	s, err := NewSecondary(newCapture(), fast, 1, st)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	frame := func(fn byte, fcb bool, data []byte) ft12.Frame {
		f := ft12.Frame{Kind: ft12.KindFixed, Addr: 1, Control: byte(PrimaryControl(fn, fcb, fcvRequired(fn), false))}
		if data != nil {
			f.Kind, f.Data = ft12.KindVariable, data
		}
		return f
	}
	cmd := []byte{45, 1, 6, 5, 1, 0, 0, 1}
	s.receive(frame(FuncResetLink, false, nil))
	s.receive(frame(FuncUserDataConf, true, cmd)) // executed
	s.receive(frame(FuncUserDataConf, true, cmd)) // repetition in order: answered, not executed
	if n := st.received(); n != 1 {
		t.Fatalf("in order: executed %d times", n)
	}
	s.receive(frame(FuncRequestClass2, false, nil)) // the next message
	s.receive(frame(FuncUserDataConf, true, cmd))   // the repetition, late
	if n := st.received(); n != 2 {
		t.Fatalf("late repetition: executed %d times; the one-bit FCB makes it a new message", n)
	}
}
