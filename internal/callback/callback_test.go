// SPDX-License-Identifier: MIT

package callback

import (
	"bytes"
	"log"
	"runtime"
	"strings"
	"sync"
	"testing"

	iec60870 "github.com/otfabric/go-iec60870-5"
	"github.com/otfabric/go-iec60870-5/internal/logx"
)

// A callback that returns normally gives nil and logs nothing.
func TestCallReturnsNormally(t *testing.T) {
	var buf bytes.Buffer
	ran := false
	if err := Call(logx.New(iec60870.NewStdLogger(log.New(&buf, "", 0)), "test"), "Handler", func() { ran = true }); err != nil || !ran {
		t.Fatalf("Call: %v, ran %v", err, ran)
	}
	if buf.Len() != 0 {
		t.Errorf("logged %q", buf.String())
	}
}

// A callback that does not return normally gives an error naming it and
// the value it ended with, and is logged with the stack.
func TestCallDoesNotReturnNormally(t *testing.T) {
	var buf bytes.Buffer
	err := Call(logx.New(iec60870.NewStdLogger(log.New(&buf, "", 0)), "test"), "OnState", func() { panic("stopped") })
	if err == nil || !strings.Contains(err.Error(), "OnState") || !strings.Contains(err.Error(), "stopped") {
		t.Fatalf("Call: %v", err)
	}
	if s := buf.String(); !strings.Contains(s, "did not return normally") || !strings.Contains(s, "goroutine ") {
		t.Errorf("logged %q", s)
	}
	// A nil logger is silent.
	if err := Call(nil, "Handler", func() { panic("stopped") }); err == nil {
		t.Fatal("Call with a nil logger: nil")
	}
}

// A callback that ends its goroutine with runtime.Goexit still ends it:
// Call does not return to its caller.
func TestCallGoexit(t *testing.T) {
	done := make(chan bool, 1)
	go func() {
		returned := false
		defer func() { done <- returned }()
		_ = Call(nil, "Handler", runtime.Goexit)
		returned = true
	}()
	if <-done {
		t.Fatal("Call returned after runtime.Goexit")
	}
}

// Goroutine tells goroutines apart and is never 0.
func TestGoroutine(t *testing.T) {
	me := Goroutine()
	if me == 0 || Goroutine() != me {
		t.Fatalf("Goroutine: %d, then %d", me, Goroutine())
	}
	var other uint64
	var wg sync.WaitGroup
	wg.Go(func() { other = Goroutine() })
	wg.Wait()
	if other == 0 || other == me {
		t.Fatalf("another goroutine: %d, this one %d", other, me)
	}
}
