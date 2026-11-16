// SPDX-License-Identifier: MIT

package logx

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	iec60870 "github.com/otfabric/go-iec60870-5"
)

// stopValue is what a test logger ends with when it does not return
// normally.
const stopValue = "test logger stopped"

// stopping is an application logger that records its formatted lines; its
// methods named in stop do not return normally.
type stopping struct {
	mu    sync.Mutex
	lines []string
	stop  map[string]bool
}

func (s *stopping) stops(method string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stop[method]
}

func (s *stopping) write(method, line string) {
	if s.stops(method) {
		panic(stopValue)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lines = append(s.lines, line)
}

func (s *stopping) set(method string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stop[method] = true
}

func (s *stopping) text() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.Join(s.lines, "\n")
}

func (s *stopping) Debugf(f string, a ...any) { s.write("Debugf", "DEBUG "+fmt.Sprintf(f, a...)) }
func (s *stopping) Infof(f string, a ...any)  { s.write("Infof", "INFO "+fmt.Sprintf(f, a...)) }
func (s *stopping) Warnf(f string, a ...any)  { s.write("Warnf", "WARN "+fmt.Sprintf(f, a...)) }
func (s *stopping) Errorf(f string, a ...any) { s.write("Errorf", "ERROR "+fmt.Sprintf(f, a...)) }

// stoppingFields adds the FieldLogger methods.
type stoppingFields struct{ *stopping }

func (s stoppingFields) With(...any) iec60870.FieldLogger {
	if s.stops("With") {
		panic(stopValue)
	}
	return s
}
func (s stoppingFields) DebugKV(msg string, _ ...any) { s.write("DebugKV", "DEBUG "+msg) }
func (s stoppingFields) InfoKV(msg string, _ ...any)  { s.write("InfoKV", "INFO "+msg) }
func (s stoppingFields) WarnKV(msg string, _ ...any)  { s.write("WarnKV", "WARN "+msg) }
func (s stoppingFields) ErrorKV(msg string, _ ...any) { s.write("ErrorKV", "ERROR "+msg) }

// stoppingAll adds the ContextLogger methods and DebugEnabled.
type stoppingAll struct{ stoppingFields }

func (s stoppingAll) DebugContext(_ context.Context, msg string, _ ...any) {
	s.write("DebugContext", "DEBUG "+msg)
}
func (s stoppingAll) InfoContext(_ context.Context, msg string, _ ...any) {
	s.write("InfoContext", "INFO "+msg)
}
func (s stoppingAll) WarnContext(_ context.Context, msg string, _ ...any) {
	s.write("WarnContext", "WARN "+msg)
}
func (s stoppingAll) ErrorContext(_ context.Context, msg string, _ ...any) {
	s.write("ErrorContext", "ERROR "+msg)
}
func (s stoppingAll) DebugEnabled() bool {
	if s.stops("DebugEnabled") {
		panic(stopValue)
	}
	return true
}

// returns runs f and reports whether it returned normally.
func returns(f func()) (normal bool) {
	defer func() { _ = recover() }()
	f()
	return true
}

// A call of the application's logger that does not return normally loses
// its entry and is written once more, with the value and the stack,
// through the logger's Errorf; the call returns normally. When Errorf
// fails too, the failure is dropped. A With that does not return normally
// leaves the formatted lines, a DebugEnabled that does not says false.
func TestLoggerThatDoesNotReturnNormally(t *testing.T) {
	ctx := context.Background()
	plain := func(s *stopping) iec60870.Logger { return s }
	fields := func(s *stopping) iec60870.Logger { return stoppingFields{s} }
	all := func(s *stopping) iec60870.Logger { return stoppingAll{stoppingFields{s}} }
	for _, tc := range []struct {
		method string
		base   func(*stopping) iec60870.Logger
		late   bool // the method stops only after New
		do     func(t *testing.T, l *Logger)
		want   string // a line still written, besides the report
	}{
		{"Debugf", plain, false, func(_ *testing.T, l *Logger) { l.Debug("entry") }, ""},
		{"Infof", plain, false, func(_ *testing.T, l *Logger) { l.Info("entry") }, ""},
		{"Warnf", plain, false, func(_ *testing.T, l *Logger) { l.Warn("entry") }, ""},
		{"Errorf", plain, false, func(_ *testing.T, l *Logger) { l.Error("entry") }, ""},
		{"DebugKV", fields, false, func(_ *testing.T, l *Logger) { l.Debug("entry") }, ""},
		{"InfoKV", fields, false, func(_ *testing.T, l *Logger) { l.Info("entry") }, ""},
		{"WarnKV", fields, false, func(_ *testing.T, l *Logger) { l.Warn("entry") }, ""},
		{"ErrorKV", fields, false, func(_ *testing.T, l *Logger) { l.Error("entry") }, ""},
		{"With", fields, false, func(_ *testing.T, l *Logger) { l.Info("entry") }, "INFO test component: entry"},
		{"With", fields, true, func(_ *testing.T, l *Logger) { l.With("k", 1).Info("entry") }, "INFO test component k=1: entry"},
		{"DebugContext", all, false, func(_ *testing.T, l *Logger) { l.DebugContext(ctx, "entry") }, ""},
		{"WarnContext", all, false, func(_ *testing.T, l *Logger) { l.WarnContext(ctx, "entry") }, ""},
		{"DebugEnabled", all, false, func(t *testing.T, l *Logger) {
			if l.DebugEnabled() {
				t.Error("DebugEnabled true after the logger's DebugEnabled did not return normally")
			}
		}, ""},
	} {
		name := tc.method
		if tc.late {
			name += " after New"
		}
		t.Run(name, func(t *testing.T) {
			s := &stopping{stop: map[string]bool{}}
			if !tc.late {
				s.set(tc.method)
			}
			var l *Logger
			if !returns(func() { l = New(tc.base(s), "test.component") }) {
				t.Fatalf("New: the logger's %s did not return normally through it", tc.method)
			}
			s.set(tc.method)
			if !returns(func() { tc.do(t, l) }) {
				t.Fatalf("the logger's %s did not return normally through the call", tc.method)
			}
			text := s.text()
			if strings.Contains(text, "entry") != (tc.want != "") || !strings.Contains(text, tc.want) {
				t.Errorf("lines:\n%s\nwant the entry only as %q", text, tc.want)
			}
			if tc.method == "Errorf" {
				if text != "" {
					t.Errorf("written although Errorf does not return normally:\n%s", text)
				}
				return
			}
			for _, want := range []string{"ERROR test component", "logger did not return normally", "method=" + tc.method, "value=" + stopValue, "goroutine "} {
				if !strings.Contains(text, want) {
					t.Errorf("misses %q in:\n%s", want, text)
				}
			}
			if n := strings.Count(text, "did not return normally"); n != 1 {
				t.Errorf("the failure written %d times, want once:\n%s", n, text)
			}
		})
	}
}
