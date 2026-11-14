// SPDX-License-Identifier: MIT

package client_test

import (
	"bytes"
	"context"
	"errors"
	"log"
	"strings"
	"sync"
	"testing"
	"time"

	iec60870 "github.com/otfabric/go-iec60870-5"
	"github.com/otfabric/go-iec60870-5/asdu"
	iec104 "github.com/otfabric/go-iec60870-5/iec104"
	"github.com/otfabric/go-iec60870-5/iec104/client"
	"github.com/otfabric/go-iec60870-5/iec104/internal/testutil"
	"github.com/otfabric/go-iec60870-5/iec104/server"
)

// stopValue is what a test callback ends with when it does not return
// normally.
const stopValue = "test callback stopped"

type notice struct {
	state iec104.State
	err   error
}

// A Handler, state handler or start handler that does not return normally
// is logged with its stack and closes the client: the state handler is
// told "disconnected" with an error that wraps ErrClosed and names the
// callback, and the test process goes on.
func TestCallbackThatDoesNotReturnNormally(t *testing.T) {
	spontaneous := server.WithStateHandler(func(s *server.Session, state iec104.State, _ error) {
		if state == iec104.StateStarted {
			_ = s.Send(context.Background(), asdu.New(asdu.CauseSpontaneous, 1, asdu.SinglePoint{IOA: 1, Value: true}))
		}
	})
	for _, tc := range []struct {
		name string
		opts func(notices chan<- notice) []client.Option
	}{
		{"Handler", func(notices chan<- notice) []client.Option {
			return []client.Option{
				client.WithHandler(client.HandlerFunc(func(*asdu.ASDU) { panic(stopValue) })),
				client.WithStateHandler(func(s iec104.State, err error) { notices <- notice{s, err} }),
			}
		}},
		{"state handler", func(notices chan<- notice) []client.Option {
			return []client.Option{client.WithStateHandler(func(s iec104.State, err error) {
				notices <- notice{s, err}
				if s == iec104.StateStarted {
					panic(stopValue)
				}
			})}
		}},
		{"start handler", func(notices chan<- notice) []client.Option {
			return []client.Option{
				client.WithStartHandler(func(context.Context, *client.Client) { panic(stopValue) }),
				client.WithStateHandler(func(s iec104.State, err error) { notices <- notice{s, err} }),
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newStation(t, spontaneous)
			var mu sync.Mutex
			var logs bytes.Buffer
			notices := make(chan notice, 64)
			opts := append(tc.opts(notices), client.WithLogger(iec60870.NewStdLogger(log.New(writerFunc(func(p []byte) (int, error) {
				mu.Lock()
				defer mu.Unlock()
				return logs.Write(p)
			}), "", 0))))
			// The state handler may stop the client before Dial returns, so
			// Dial may end with ErrClosed; the notices below still apply.
			opts = append([]client.Option{client.WithParams(testutil.Params()), client.WithRequestTimeout(3 * time.Second)}, opts...)
			c, err := client.Dial(context.Background(), st.addr, opts...)
			if err != nil && !errors.Is(err, iec104.ErrClosed) {
				t.Fatalf("Dial: %v", err)
			}
			for {
				var n notice
				select {
				case n = <-notices:
				case <-time.After(10 * time.Second):
					t.Fatal("the client was not closed")
				}
				if n.state != iec104.StateDisconnected {
					continue
				}
				if !errors.Is(n.err, iec104.ErrClosed) || !strings.Contains(n.err.Error(), tc.name) {
					t.Errorf("disconnected with %v, want ErrClosed naming the %s", n.err, tc.name)
				}
				break
			}
			if c != nil {
				within(t, "Close", func() { _ = c.Close() })
				if s := c.State(); s != iec104.StateDisconnected {
					t.Errorf("state %s", s)
				}
			}
			mu.Lock()
			text := logs.String()
			mu.Unlock()
			if !strings.Contains(text, "did not return normally") || !strings.Contains(text, "goroutine ") {
				t.Errorf("no log entry with the stack:\n%s", text)
			}
		})
	}
}

type writerFunc func(p []byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

// A switch handler that does not return normally closes its group: every
// connection ends, and the group refuses what follows with ErrClosed.
func TestSwitchHandlerThatDoesNotReturnNormally(t *testing.T) {
	a := orderingServe(t, server.HandlerFunc(confirming))
	b := orderingServe(t, server.HandlerFunc(confirming))
	disconnected := make(chan struct{}, 2)
	g, err := client.NewGroup([]string{a, b}, client.WithParams(testutil.Params()),
		client.WithReconnect(client.Reconnect{MinDelay: 20 * time.Millisecond, MaxDelay: 50 * time.Millisecond}),
		client.WithSwitchHandler(func(active *client.Client) {
			if active != nil {
				panic(stopValue)
			}
		}),
		client.WithStateHandler(func(s iec104.State, _ error) {
			if s == iec104.StateDisconnected {
				disconnected <- struct{}{}
			}
		}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = g.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// The switch handler runs before Connect returns, and closes the group.
	if err := g.Connect(ctx); err != nil && !errors.Is(err, iec104.ErrClosed) {
		t.Fatal(err)
	}
	for range 2 {
		select {
		case <-disconnected:
		case <-time.After(10 * time.Second):
			t.Fatal("the group did not close its connections")
		}
	}
	for _, c := range g.Clients() {
		if c.State() != iec104.StateDisconnected {
			t.Errorf("%s is %s after the switch handler stopped the group", c.Addr(), c.State())
		}
	}
	if err := g.Switchover(context.Background()); !errors.Is(err, iec104.ErrClosed) {
		t.Errorf("Switchover: %v, want ErrClosed", err)
	}
	within(t, "Group.Close", func() { _ = g.Close() })
}
