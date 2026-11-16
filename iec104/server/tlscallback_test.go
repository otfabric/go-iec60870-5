// SPDX-License-Identifier: MIT

package server_test

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
	"github.com/otfabric/go-iec60870-5/iec104/client"
	"github.com/otfabric/go-iec60870-5/iec104/internal/testutil"
	"github.com/otfabric/go-iec60870-5/iec104/server"
)

// syncBuffer is a log the server writes from several goroutines.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

// A callback of the server's tls.Config that does not return normally
// during the handshake of a connection is logged with its stack and
// rejects only that connection, as an accept filter does: the sessions
// before it go on, and the next connection is served. It holds for the
// configuration given to WithTLS (ListenAndServe) and for that of a TLS
// listener given to Serve.
func TestTLSCallbackRejectsOnlyThatConnection(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  func(cfg *tls.Config, second func())
	}{
		{"GetCertificate", func(cfg *tls.Config, second func()) {
			certs := cfg.Certificates
			cfg.Certificates = nil
			cfg.GetCertificate = func(*tls.ClientHelloInfo) (*tls.Certificate, error) { second(); return &certs[0], nil }
		}},
		{"GetConfigForClient", func(cfg *tls.Config, second func()) {
			cfg.GetConfigForClient = func(*tls.ClientHelloInfo) (*tls.Config, error) { second(); return nil, nil }
		}},
		{"VerifyPeerCertificate", func(cfg *tls.Config, second func()) {
			cfg.VerifyPeerCertificate = func([][]byte, [][]*x509.Certificate) error { second(); return nil }
		}},
		{"VerifyConnection", func(cfg *tls.Config, second func()) {
			cfg.VerifyConnection = func(tls.ConnectionState) error { second(); return nil }
		}},
	} {
		for _, listen := range []string{"WithTLS", "Serve"} {
			t.Run(tc.name+"/"+listen, func(t *testing.T) {
				serverTLS, clientTLS := testutil.TLSConfigs(t)
				serverTLS.ClientAuth = tls.RequireAnyClientCert
				clientTLS.Certificates = serverTLS.Certificates
				var n atomic.Int32
				tc.set(serverTLS, func() {
					if n.Add(1) == 2 {
						panic("test callback stopped")
					}
				})
				var logs syncBuffer
				opts := []server.Option{server.WithParams(testutil.Params()),
					server.WithLogger(iec60870.NewStdLogger(log.New(&logs, "", 0)))}
				if listen == "WithTLS" {
					srv := newServer(t, append(opts, server.WithTLS(serverTLS))...)
					go func() { _ = srv.ListenAndServe("127.0.0.1:0") }()
					testutil.Eventually(t, "listener", func() bool { return srv.Addr() != nil })
					checkTLSRejection(t, srv, srv.Addr().String(), clientTLS, &logs)
					return
				}
				ln, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				srv := newServer(t, opts...)
				go func() { _ = srv.Serve(tls.NewListener(ln, serverTLS)) }()
				checkTLSRejection(t, srv, ln.Addr().String(), clientTLS, &logs)
			})
		}
	}
}

func newServer(t *testing.T, opts ...server.Option) *server.Server {
	t.Helper()
	srv, err := server.New(server.HandlerFunc(confirmAll), opts...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	return srv
}

// checkTLSRejection connects three TLS clients to srv at addr, whose
// tls.Config callback does not return normally for the second connection.
func checkTLSRejection(t *testing.T, srv *server.Server, addr string, clientTLS *tls.Config, logs *syncBuffer) {
	t.Helper()
	c1 := connect(t, addr, client.WithTLS(clientTLS))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if c, err := client.Dial(ctx, addr, client.WithParams(testutil.Params()), client.WithTLS(clientTLS)); err == nil {
		_ = c.Close()
		t.Fatal("connection accepted whose tls.Config callback did not return normally")
	} else if errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Dial: %v, want the connection closed at once", err)
	}
	stillServes(t, srv, c1, 1)
	c3 := connect(t, addr, client.WithTLS(clientTLS))
	stillServes(t, srv, c3, 2)
	text := logs.String()
	if !strings.Contains(text, "connection rejected") || !strings.Contains(text, "tls.Config callback did not return normally") ||
		!strings.Contains(text, "goroutine ") {
		t.Errorf("no rejection logged with the stack:\n%s", text)
	}
}
