// SPDX-License-Identifier: MIT

package client_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"log"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	iec60870 "github.com/otfabric/go-iec60870-5"
	"github.com/otfabric/go-iec60870-5/asdu"
	iec104 "github.com/otfabric/go-iec60870-5/iec104"
	"github.com/otfabric/go-iec60870-5/iec104/client"
	"github.com/otfabric/go-iec60870-5/iec104/internal/testutil"
	"github.com/otfabric/go-iec60870-5/iec104/server"
	"github.com/otfabric/go-iec60870-5/internal/testutil/leakcheck"
)

// tlsStation serves TLS connections with cfg, which asks for a client
// certificate, and confirms every request. close ends the connections it
// serves and keeps listening.
type tlsStation struct {
	srv  *server.Server
	addr string
}

func newTLSStation(t *testing.T, cfg *tls.Config) *tlsStation {
	t.Helper()
	srv, err := server.New(server.HandlerFunc(func(s *server.Session, req *asdu.ASDU) { _ = s.Confirm(req) }),
		server.WithParams(testutil.Params()), server.WithLogger(iec60870.NopLogger()))
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(tls.NewListener(ln, cfg)) }()
	t.Cleanup(func() { _ = srv.Close() })
	return &tlsStation{srv: srv, addr: ln.Addr().String()}
}

// dropSessions ends the station's connections.
func (st *tlsStation) dropSessions() {
	for _, s := range st.srv.Sessions() {
		_ = s.Close()
	}
}

// A callback of the tls.Config given to WithTLS that does not return
// normally is logged with its stack and closes the client, at Connect as
// at a reconnect: Connect fails with ErrClosed, the state handler is told
// "disconnected" with an error that wraps ErrClosed and names the
// callbacks of the tls.Config, Close returns, no goroutine is left, and
// the test process goes on.
func TestTLSCallbackThatDoesNotReturnNormally(t *testing.T) {
	stop := func(n *atomic.Int32, from int32) {
		if n.Add(1) >= from {
			panic(stopValue)
		}
	}
	for _, tc := range []struct {
		name      string
		reconnect bool
		set       func(cfg *tls.Config, n *atomic.Int32, from int32)
	}{
		{"GetClientCertificate", false, func(cfg *tls.Config, n *atomic.Int32, from int32) {
			certs := cfg.Certificates
			cfg.Certificates = nil
			cfg.GetClientCertificate = func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
				stop(n, from)
				return &certs[0], nil
			}
		}},
		{"VerifyPeerCertificate", false, func(cfg *tls.Config, n *atomic.Int32, from int32) {
			cfg.VerifyPeerCertificate = func([][]byte, [][]*x509.Certificate) error { stop(n, from); return nil }
		}},
		{"VerifyConnection", false, func(cfg *tls.Config, n *atomic.Int32, from int32) {
			cfg.VerifyConnection = func(tls.ConnectionState) error { stop(n, from); return nil }
		}},
		{"VerifyConnection at a reconnect", true, func(cfg *tls.Config, n *atomic.Int32, from int32) {
			cfg.VerifyConnection = func(tls.ConnectionState) error { stop(n, from); return nil }
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Cleanup(leakcheck.Check(t))
			serverTLS, clientTLS := testutil.TLSConfigs(t)
			serverTLS.ClientAuth = tls.RequireAnyClientCert
			clientTLS.Certificates = serverTLS.Certificates
			st := newTLSStation(t, serverTLS)
			var n atomic.Int32
			from := int32(1)
			if tc.reconnect {
				from = 2
			}
			tc.set(clientTLS, &n, from)

			var mu sync.Mutex
			var logs bytes.Buffer
			notices := make(chan notice, 64)
			opts := []client.Option{
				client.WithParams(testutil.Params()), client.WithRequestTimeout(3 * time.Second),
				client.WithTLS(clientTLS),
				client.WithStateHandler(func(s iec104.State, err error) { notices <- notice{s, err} }),
				client.WithLogger(iec60870.NewStdLogger(log.New(writerFunc(func(p []byte) (int, error) {
					mu.Lock()
					defer mu.Unlock()
					return logs.Write(p)
				}), "", 0))),
			}
			if tc.reconnect {
				opts = append(opts, client.WithReconnect(client.Reconnect{MinDelay: time.Millisecond}))
			}
			c, err := client.New(st.addr, opts...)
			if err != nil {
				t.Fatal(err)
			}
			connected := make(chan error, 1)
			go func() { connected <- c.Connect(context.Background()) }()
			var cerr error
			select {
			case cerr = <-connected:
			case <-time.After(10 * time.Second):
				t.Fatal("Connect did not return")
			}
			if tc.reconnect {
				if cerr != nil {
					t.Fatalf("Connect: %v", cerr)
				}
				st.dropSessions()
			} else if !errors.Is(cerr, iec104.ErrClosed) {
				t.Errorf("Connect: %v, want ErrClosed", cerr)
			}
			for {
				var nt notice
				select {
				case nt = <-notices:
				case <-time.After(10 * time.Second):
					t.Fatal("the client was not closed")
				}
				if nt.state != iec104.StateDisconnected || (tc.reconnect && !errors.Is(nt.err, iec104.ErrClosed)) {
					continue // the connection the station ended, before the reconnect
				}
				if !errors.Is(nt.err, iec104.ErrClosed) || !strings.Contains(nt.err.Error(), "tls.Config callback") {
					t.Errorf("disconnected with %v, want ErrClosed naming the tls.Config callback", nt.err)
				}
				break
			}
			within(t, "Close", func() { _ = c.Close() })
			if s := c.State(); s != iec104.StateDisconnected {
				t.Errorf("state %s", s)
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
