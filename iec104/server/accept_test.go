// SPDX-License-Identifier: MIT

package server_test

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"syscall"
	"testing"
	"testing/synctest"

	iec60870 "github.com/otfabric/go-iec60870-5"
	"github.com/otfabric/go-iec60870-5/iec104/apci"
	"github.com/otfabric/go-iec60870-5/iec104/internal/testutil"
	"github.com/otfabric/go-iec60870-5/iec104/server"
)

// stubListener hands out the connections and errors queued on it, in
// order, and then blocks until it is closed.
type stubListener struct {
	next      chan func() (net.Conn, error)
	closed    chan struct{}
	closeOnce sync.Once
}

func newStubListener(steps ...func() (net.Conn, error)) *stubListener {
	l := &stubListener{next: make(chan func() (net.Conn, error), len(steps)), closed: make(chan struct{})}
	for _, s := range steps {
		l.next <- s
	}
	return l
}

func (l *stubListener) Accept() (net.Conn, error) {
	select {
	case step := <-l.next:
		return step()
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *stubListener) Close() error {
	l.closeOnce.Do(func() { close(l.closed) })
	return nil
}

func (l *stubListener) Addr() net.Addr { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 2404} }

// pipeConn is the station's end of a net.Pipe, with TCP addresses, as
// Serve logs and checks them.
type pipeConn struct{ net.Conn }

func (pipeConn) RemoteAddr() net.Addr { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 2), Port: 50000} }
func (pipeConn) LocalAddr() net.Addr  { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 2404} }

func acceptError(errno syscall.Errno) func() (net.Conn, error) {
	return func() (net.Conn, error) {
		return nil, &net.OpError{Op: "accept", Net: "tcp", Err: os.NewSyscallError("accept", errno)}
	}
}

// An accept error other than a timeout leaves the listener working: Serve
// retries after a pause and serves the connection that comes next.
func TestServeRetriesAcceptErrors(t *testing.T) {
	for _, errno := range []syscall.Errno{syscall.EMFILE, syscall.ENFILE, syscall.ECONNABORTED} {
		t.Run(errno.Error(), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				station, peer := net.Pipe()
				ln := newStubListener(acceptError(errno),
					func() (net.Conn, error) { return pipeConn{station}, nil })
				srv, err := server.New(nil, server.WithParams(testutil.Params()), server.WithLogger(iec60870.NopLogger()))
				if err != nil {
					t.Fatal(err)
				}
				served := make(chan error, 1)
				go func() { served <- srv.Serve(ln) }()

				confirmed := make(chan error, 1)
				go func() {
					act, _ := apci.NewU(apci.StartDTAct).MarshalBinary()
					if _, err := peer.Write(act); err != nil {
						confirmed <- err
						return
					}
					f, err := apci.ReadFrame(peer)
					if err == nil && (f.Format != apci.FormatU || f.Function != apci.StartDTCon) {
						err = errors.New("not STARTDT con: " + f.String())
					}
					confirmed <- err
				}()
				select {
				case err := <-served:
					_ = peer.Close()
					_ = station.Close()
					// The peer's read ends with the connection.
					t.Fatalf("Serve returned after one accept error: %v (peer: %v)", err, <-confirmed)
				case err := <-confirmed:
					if err != nil {
						t.Fatalf("the connection accepted after the error: %v", err)
					}
				}
				_ = peer.Close()
				if err := srv.Close(); err != nil {
					t.Errorf("Close: %v", err)
				}
				if err := <-served; !errors.Is(err, server.ErrServerClosed) {
					t.Errorf("Serve: %v, want ErrServerClosed", err)
				}
			})
		})
	}
}

// A listener closed by someone else ends Serve with its error.
func TestServeEndsWhenTheListenerIsClosed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ln := newStubListener()
		srv, err := server.New(nil, server.WithLogger(iec60870.NopLogger()))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = srv.Close() }()
		served := make(chan error, 1)
		go func() { served <- srv.Serve(ln) }()
		synctest.Wait()
		_ = ln.Close()
		if err := <-served; !errors.Is(err, net.ErrClosed) {
			t.Fatalf("Serve: %v, want net.ErrClosed", err)
		}
	})
}

// With WithMaxSessions(n), TLS handshakes in progress count against the
// limit: of n+1 connections that never start their handshake, n wait for
// it (up to t0) and the last one is closed at once.
func TestTLSHandshakesCountAgainstTheSessionLimit(t *testing.T) {
	serverTLS, _ := testutil.TLSConfigs(t) // outside the bubble: real time for the certificate
	for _, limit := range []int{1, 2, 4} {
		t.Run(fmt.Sprintf("limit %d", limit), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				var steps []func() (net.Conn, error)
				var peers []net.Conn
				for range limit + 1 {
					station, peer := net.Pipe()
					peers = append(peers, peer)
					steps = append(steps, func() (net.Conn, error) { return tls.Server(pipeConn{station}, serverTLS), nil })
				}
				ln := newStubListener(steps...)
				srv, err := server.New(nil, server.WithParams(testutil.Params()), server.WithTLS(serverTLS),
					server.WithMaxSessions(limit), server.WithLogger(iec60870.NopLogger()))
				if err != nil {
					t.Fatal(err)
				}
				served := make(chan error, 1)
				go func() { served <- srv.Serve(ln) }()

				closed := make(chan int, len(peers))
				for i, p := range peers {
					go func() {
						_, err := p.Read(make([]byte, 1))
						if err != nil {
							closed <- i
						}
					}()
				}
				synctest.Wait() // every connection accepted; each handshake waits or was refused
				if n := len(closed); n != 1 {
					t.Errorf("%d of %d silent connections closed while %d handshakes are pending, want 1", n, len(peers), limit)
				}
				if n := len(srv.Sessions()); n != 0 {
					t.Errorf("%d sessions, want 0", n)
				}
				if err := srv.Close(); err != nil {
					t.Errorf("Close: %v", err)
				}
				for _, p := range peers {
					_ = p.Close()
				}
				if err := <-served; !errors.Is(err, server.ErrServerClosed) {
					t.Errorf("Serve: %v, want ErrServerClosed", err)
				}
			})
		})
	}
}
