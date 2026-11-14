// SPDX-License-Identifier: MIT

// Package leakcheck finds the goroutines a test leaves running. It compares
// goroutine stacks, not counts: a goroutine is left over when it was not
// running when the check started and it runs, or was started by, code of
// this module. Goroutines of the runtime, of the testing package and of the
// fuzzing engine neither hide one nor pass for one. It holds no production
// code.
package leakcheck

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Wait bounds how long a check waits for goroutines to end after the code
// under test was closed.
const Wait = 5 * time.Second

// module prefixes the import path of every package of this module.
const module = "github.com/otfabric/go-iec60870-5/"

// self is this package: the goroutine that runs a check is not counted.
const self = module + "internal/testutil/leakcheck."

type goroutine struct{ id, stack string }

func snapshot() []goroutine {
	buf := make([]byte, 1<<20)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			buf = buf[:n]
			break
		}
		buf = make([]byte, 2*len(buf))
	}
	var out []goroutine
	for _, g := range strings.Split(string(buf), "\n\n") {
		head, _, _ := strings.Cut(g, "\n")
		f := strings.Fields(head) // "goroutine 18 [chan receive]:"
		if len(f) < 2 || f[0] != "goroutine" {
			continue
		}
		out = append(out, goroutine{id: f[1], stack: g})
	}
	return out
}

// ours returns the stacks of the goroutines that run code of this module
// and are not in before, other than the caller.
func ours(before map[string]bool) []string {
	var left []string
	for _, g := range snapshot() {
		if before[g.id] || strings.Contains(g.stack, self) || !strings.Contains(g.stack, module) {
			continue
		}
		left = append(left, g.stack)
	}
	return left
}

// wait returns the goroutines of ours that are still running after up to
// Wait.
func wait(before map[string]bool) []string {
	deadline := time.Now().Add(Wait)
	for {
		left := ours(before)
		if len(left) == 0 || time.Now().After(deadline) {
			return left
		}
		runtime.Gosched()
		time.Sleep(5 * time.Millisecond) // polling a condition with a deadline, not synchronising
	}
}

// Check records the goroutines running now and returns a function that
// fails t, with their stacks, when goroutines of this module started since
// are still running after up to [Wait]. Call the function after closing
// what the test started: defer it, or pass it to t.Cleanup before
// registering the cleanups that close.
func Check(t testing.TB) func() {
	t.Helper()
	before := map[string]bool{}
	for _, g := range snapshot() {
		before[g.id] = true
	}
	return func() {
		t.Helper()
		if left := wait(before); len(left) > 0 {
			t.Errorf("%d goroutines left running:\n\n%s", len(left), strings.Join(left, "\n\n"))
		}
	}
}

// Main runs the tests of a package and fails the run when a goroutine of
// this module is still running [Wait] after the last test: the check of a
// package whose tests do not each check. Use it as
//
//	func TestMain(m *testing.M) { leakcheck.Main(m) }
func Main(m *testing.M) {
	code := m.Run()
	if code == 0 {
		if left := wait(nil); len(left) > 0 {
			fmt.Fprintf(os.Stderr, "leak: %d goroutines left running after the tests:\n\n%s\n", len(left), strings.Join(left, "\n\n"))
			code = 1
		}
	}
	os.Exit(code)
}
