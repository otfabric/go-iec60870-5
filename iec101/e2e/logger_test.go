// SPDX-License-Identifier: MIT

package e2e

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/otfabric/go-iec60870-5/asdu"
	"github.com/otfabric/go-iec60870-5/iec101"
	"github.com/otfabric/go-iec60870-5/iec101/client"
	"github.com/otfabric/go-iec60870-5/iec101/server"
)

// stopLogger is an application logger none of whose calls returns
// normally.
type stopLogger struct{ calls atomic.Int64 }

func (l *stopLogger) stop() { l.calls.Add(1); panic(stopValue) }

func (l *stopLogger) Debugf(string, ...any) { l.stop() }
func (l *stopLogger) Infof(string, ...any)  { l.stop() }
func (l *stopLogger) Warnf(string, ...any)  { l.stop() }
func (l *stopLogger) Errorf(string, ...any) { l.stop() }

// A logger none of whose calls returns normally, Errorf included, loses
// every entry and nothing else: in both modes the client and the station,
// their link layers included, bring the link up, answer an interrogation
// and close as without it.
func TestLoggerThatDoesNotReturnNormally(t *testing.T) {
	for _, mode := range []iec101.Mode{iec101.ModeUnbalanced, iec101.ModeBalanced} {
		t.Run(mode.String(), func(t *testing.T) {
			p := params(mode)
			a, b := net.Pipe()
			var cl, sl stopLogger
			s, err := server.New(b, server.Config{Params: p, LinkAddr: 3, Remote: 1, CommonAddr: ca, Handler: &station{},
				RetryInterval: 50 * time.Millisecond, Logger: &sl})
			if err != nil {
				t.Fatal(err)
			}
			c, err := client.New(a, client.Config{Params: p, LinkAddr: 3, OwnAddr: 1,
				PollInterval: 10 * time.Millisecond, RetryInterval: 50 * time.Millisecond, Logger: &cl})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := c.WaitUp(ctx); err != nil {
				t.Fatal(err)
			}
			if _, err := c.Interrogate(ctx, ca, asdu.QOIStation); err != nil {
				t.Errorf("Interrogate: %v", err)
			}
			_ = c.Close()
			_ = s.Close()
			if cl.calls.Load() == 0 || sl.calls.Load() == 0 {
				t.Errorf("the loggers were called %d (client) and %d (station) times, want both", cl.calls.Load(), sl.calls.Load())
			}
		})
	}
}
