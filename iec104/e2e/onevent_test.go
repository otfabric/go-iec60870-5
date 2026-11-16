// SPDX-License-Identifier: MIT

package e2e_test

import (
	"bytes"
	"context"
	"crypto/x509/pkix"
	"errors"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	iec60870 "github.com/otfabric/go-iec60870-5"
	"github.com/otfabric/go-iec60870-5/asdu"
	iec104 "github.com/otfabric/go-iec60870-5/iec104"
	"github.com/otfabric/go-iec60870-5/iec104/client"
	"github.com/otfabric/go-iec60870-5/iec104/internal/station"
	"github.com/otfabric/go-iec60870-5/iec104/internal/testutil"
	"github.com/otfabric/go-iec60870-5/iec104/server"
	"github.com/otfabric/go-iec60870-5/internal/testutil/leakcheck"
	"github.com/otfabric/go-iec60870-5/sec"
)

// stopEvent is a sec.Config.OnEvent that does not return normally for the
// event mnemonic, the first n times.
func stopEvent(mnemonic string, n int32) func(sec.Event) {
	var left atomic.Int32
	left.Store(n)
	return func(e sec.Event) {
		if e.Mnemonic == mnemonic && left.Add(-1) >= 0 {
			panic("test callback stopped")
		}
	}
}

// eventRig is a station with IEC 62351-5 and the endpoints of its
// controlling stations, each with an AIM of its own.
type eventRig struct {
	st   *station.Station
	ca   asdu.CommonAddr
	srv  *sec.Identity
	cli  *sec.Identity
	next atomic.Uint32

	mu   sync.Mutex
	logs bytes.Buffer // the station's
}

func (r *eventRig) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.logs.Write(p)
}

// logged checks that the station logged an OnEvent that did not return
// normally, with its stack.
func (r *eventRig) logged(t *testing.T) {
	t.Helper()
	r.mu.Lock()
	text := r.logs.String()
	r.mu.Unlock()
	if !strings.Contains(text, "callback did not return normally") || !strings.Contains(text, "sec.Config.OnEvent") ||
		!strings.Contains(text, "goroutine ") {
		t.Errorf("no log entry of OnEvent with the stack:\n%s", text)
	}
}

func newEventRig(t *testing.T, stationEvents func(sec.Event), roles []sec.AccessRole) *eventRig {
	t.Helper()
	srvID, err := sec.NewIdentity(sec.IdentityOptions{Subject: pkix.Name{CommonName: "station"}})
	if err != nil {
		t.Fatal(err)
	}
	cliID, err := sec.NewIdentity(sec.IdentityOptions{Subject: pkix.Name{CommonName: "scada"}})
	if err != nil {
		t.Fatal(err)
	}
	fx := station.Baseline(t)
	r := &eventRig{ca: fx.Station.CommonAddr, srv: srvID, cli: cliID}
	r.next.Store(6)
	r.st = station.Start(t, fx, server.WithParams(testutil.Params()), server.WithLogger(iec60870.NewStdLogger(log.New(r, "", 0))),
		server.WithSecurity(func(string, uint16) (*sec.Endpoint, error) {
			return sec.New(sec.Config{Role: sec.Controlled, AssociationID: 9, Identity: srvID, Trust: cliID.Pin(),
				CommonAddr: r.ca, Settings: eventSettings, Roles: roles, OnEvent: stationEvents})
		}))
	return r
}

var eventSettings = sec.Settings{ReplyTimeout: time.Second, RequestTimeout: 3 * time.Second}

// controlling returns an endpoint of the next controlling station.
func (r *eventRig) controlling(t *testing.T, onEvent func(sec.Event)) *sec.Endpoint {
	t.Helper()
	ep, err := sec.New(sec.Config{Role: sec.Controlling, AssociationID: uint16(r.next.Add(1)), Identity: r.cli,
		Trust: r.srv.Pin(), CommonAddr: r.ca, DataProtection: sec.HMACSHA256.Code, Settings: eventSettings, OnEvent: onEvent})
	if err != nil {
		t.Fatal(err)
	}
	return ep
}

func (r *eventRig) dial(t *testing.T, ep *sec.Endpoint, opts ...client.Option) (*client.Client, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := client.Dial(ctx, r.st.Loopback(), append([]client.Option{client.WithParams(testutil.Params()),
		client.WithRequestTimeout(3 * time.Second), client.WithSecurity(ep)}, opts...)...)
	if err == nil {
		t.Cleanup(func() { _ = c.Close() })
	}
	return c, err
}

// served checks that a further controlling station is served.
func (r *eventRig) served(t *testing.T) {
	t.Helper()
	c, err := r.dial(t, r.controlling(t, nil))
	if err != nil {
		t.Fatalf("Dial of a further controlling station: %v", err)
	}
	if _, err := c.Interrogate(context.Background(), r.ca, asdu.QOIStation); err != nil {
		t.Fatalf("Interrogate: %v", err)
	}
}

// A sec.Config.OnEvent that does not return normally on the goroutine of
// the controlling station's connection closes that client: Connect fails
// with ErrClosed, the state handler is told "disconnected" with an error
// that names OnEvent, and the station goes on serving.
func TestSecurityEventHandlerOfTheClient(t *testing.T) {
	t.Cleanup(leakcheck.Check(t))
	r := newEventRig(t, nil, nil)
	disconnected := make(chan error, 8)
	c, err := r.dial(t, r.controlling(t, stopEvent("STAS_PROC_SUCC", 1)),
		client.WithStateHandler(func(s iec104.State, err error) {
			if s == iec104.StateDisconnected {
				disconnected <- err
			}
		}))
	if err == nil {
		_ = c.Close()
		t.Fatal("Dial succeeded although OnEvent did not return normally")
	}
	if !errors.Is(err, iec104.ErrClosed) {
		t.Errorf("Dial: %v, want ErrClosed", err)
	}
	select {
	case err := <-disconnected:
		if !errors.Is(err, iec104.ErrClosed) || !strings.Contains(err.Error(), "sec.Config.OnEvent") {
			t.Errorf("disconnected with %v, want ErrClosed naming sec.Config.OnEvent", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no disconnected notice")
	}
	r.served(t)
}

// A sec.Config.OnEvent of the station that does not return normally on the
// goroutine of a session ends that session only, whether it runs for the
// key management of the session or for a request the roles refuse: the
// controlling station is not served on it, and the station goes on serving
// the others.
func TestSecurityEventHandlerOfTheStation(t *testing.T) {
	t.Run("association", func(t *testing.T) {
		r := newEventRig(t, stopEvent("STAS_PROC_SUCC", 1), nil)
		if c, err := r.dial(t, r.controlling(t, nil)); err == nil {
			// The session may end after the controlling station saw the
			// association complete: then the connection is lost.
			testutil.Eventually(t, "the session ended", func() bool {
				_, err := c.Interrogate(context.Background(), r.ca, asdu.QOIStation)
				return err != nil
			})
		}
		testutil.Eventually(t, "no session left", func() bool { return len(r.st.Server.Sessions()) == 0 })
		r.logged(t)
		r.served(t)
	})
	t.Run("refused request", func(t *testing.T) {
		r := newEventRig(t, stopEvent("OPER_NOT_AUTR", 1), []sec.AccessRole{sec.RoleViewer})
		c, err := r.dial(t, r.controlling(t, nil))
		if err != nil {
			t.Fatal(err)
		}
		err = c.Command(context.Background(), r.ca, asdu.SingleCommand{IOA: 500, Value: true})
		var neg *iec104.NegativeError
		if err == nil || errors.As(err, &neg) {
			t.Fatalf("Command: %v, want the session ended without an answer", err)
		}
		testutil.Eventually(t, "no session left", func() bool { return len(r.st.Server.Sessions()) == 0 })
		r.logged(t)
		r.served(t)
		// The next refusal is answered: the OnEvent returns normally now.
		c2, err := r.dial(t, r.controlling(t, nil))
		if err != nil {
			t.Fatal(err)
		}
		negative(t, c2.Command(context.Background(), r.ca, asdu.SingleCommand{IOA: 500, Value: true}), asdu.CauseNotAuthorized)
	})
}
