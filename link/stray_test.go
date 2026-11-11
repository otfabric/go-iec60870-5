// SPDX-License-Identifier: MIT

package link

import (
	"context"
	"errors"
	"testing"

	"github.com/otfabric/go-iec60870-5/ft12"
)

// TestStrayAnswersAreATimeOut is the regression test of a secondary that
// answers every message with a function that cannot answer it: the
// exchange failed with ErrUnexpected alone, the stations treated that as a
// link that is up, and a client polled such a station without end. Only
// stray answers until the last time-out is no answer: the error wraps
// ErrTimeout too.
func TestStrayAnswersAreATimeOut(t *testing.T) {
	conn := scripted(t, func(f ft12.Frame) []ft12.Frame {
		if Control(f.Control).Func() == FuncRequestStatus {
			return []ft12.Frame{fixed(FuncAck, 1)}
		}
		return []ft12.Frame{fixed(FuncStatus, 1)}
	})
	pr, err := NewPrimary(conn, fast)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pr.Close() }()
	ctx := context.Background()
	for name, call := range map[string]func() error{
		"request status": func() error { _, err := pr.RequestStatus(ctx, 1); return err },
		"send/confirm":   func() error { _, err := pr.SendConfirm(ctx, 1, []byte{1}); return err },
		"class 2":        func() error { _, _, err := pr.RequestData(ctx, 1, Class2); return err },
	} {
		if err := call(); !errors.Is(err, ErrUnexpected) || !errors.Is(err, ErrTimeout) {
			t.Errorf("%s: %v, want ErrUnexpected and ErrTimeout", name, err)
		}
	}
}
