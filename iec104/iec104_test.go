// SPDX-License-Identifier: MIT

package iec104

import (
	"errors"
	"testing"

	"github.com/otfabric/go-iec60870-5/asdu"
	"github.com/otfabric/go-iec60870-5/iec104/apci"
)

func TestState(t *testing.T) {
	for s, want := range map[State]string{
		StateDisconnected: "disconnected", StateConnecting: "connecting",
		StateStopped: "stopped", StateStarted: "started", 9: "unknown",
	} {
		if s.String() != want {
			t.Errorf("State(%d) = %q, want %q", s, s.String(), want)
		}
	}
}

func TestNegativeError(t *testing.T) {
	var err error = &NegativeError{ASDU: &asdu.ASDU{Type: asdu.C_SC_NA_1, Cause: asdu.CauseUnknownIOA, CommonAddr: 7, Negative: true}}
	var neg *NegativeError
	if !errors.As(err, &neg) || neg.Cause() != asdu.CauseUnknownIOA {
		t.Fatalf("errors.As / Cause: %v", err)
	}
	if got, want := err.Error(), "iec104: C_SC_NA_1 rejected by station 7: unknown-ioa"; got != want {
		t.Errorf("Error = %q, want %q", got, want)
	}
	empty := &NegativeError{}
	if empty.Cause() != 0 || empty.Error() == "" {
		t.Error("NegativeError without ASDU")
	}
}

func TestNopMetrics(t *testing.T) {
	var m Metrics = NopMetrics{}
	m.OnConnect(nil)
	m.OnDisconnect(nil, nil)
	m.OnFrameSent(nil, apci.Frame{}, 0)
	m.OnFrameReceived(nil, apci.Frame{}, 0)
	m.OnDecodeError(nil, nil)
}
