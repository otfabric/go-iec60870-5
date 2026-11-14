// SPDX-License-Identifier: MIT

package client_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/otfabric/go-iec60870-5/asdu"
	iec104 "github.com/otfabric/go-iec60870-5/iec104"
	"github.com/otfabric/go-iec60870-5/iec104/client"
	"github.com/otfabric/go-iec60870-5/iec104/server"
	"github.com/otfabric/go-iec60870-5/telecontrol"
)

// streaming is a station that confirms an interrogation or a counter
// request, sends n ASDUs of process data with the matching cause and
// terminates the request when term is set.
func streaming(t *testing.T, n int, term bool) string {
	t.Helper()
	return serveHandler(t, server.HandlerFunc(func(s *server.Session, req *asdu.ASDU) {
		var cause asdu.Cause
		var obj func(i int) asdu.InformationObject
		switch o := req.First().(type) {
		case asdu.Interrogation:
			cause = o.Qualifier.Cause()
			obj = func(i int) asdu.InformationObject { return asdu.SinglePoint{IOA: asdu.IOA(i + 1)} }
		case asdu.CounterInterrogation:
			cause = o.Cause()
			obj = func(i int) asdu.InformationObject { return asdu.IntegratedTotal{IOA: asdu.IOA(i + 1)} }
		default:
			return
		}
		_ = s.Confirm(req)
		for i := range n {
			_ = s.Send(s.Context(), asdu.New(cause, req.CommonAddr, obj(i)))
		}
		if term {
			_ = s.Terminate(req)
		}
	}))
}

// An interrogation answered by more ASDUs than WithMaxResult allows ends
// at the bound with the ASDUs so far and ErrResultLimit, without waiting
// for its deadline; a termination within the bound completes it, and a
// negative bound is none. A request that ended at the bound is not
// retried.
func TestInterrogateResultLimit(t *testing.T) {
	for _, tc := range []struct {
		name      string
		max, sent int
		term      bool
		want      int
		err       error
	}{
		{"below the bound", 4, 3, true, 3, nil},
		{"at the bound", 4, 4, true, 4, nil},
		{"one beyond", 4, 5, true, 4, telecontrol.ErrResultLimit},
		{"far beyond, no termination", 4, 500, false, 4, telecontrol.ErrResultLimit},
		{"no bound", -1, 500, true, 500, nil},
		{"default bound", 0, 500, true, 500, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			addr := streaming(t, tc.sent, tc.term)
			c := dial(t, addr, client.WithMaxResult(tc.max), client.WithRetry(client.Retry{Attempts: 3}))
			got, err := c.Interrogate(context.Background(), 1, asdu.QOIStation)
			if len(got) != tc.want || !errors.Is(err, tc.err) || (tc.err == nil) != (err == nil) {
				t.Fatalf("Interrogate: %d ASDUs, %v; want %d, %v", len(got), err, tc.want, tc.err)
			}
			if errors.Is(err, context.DeadlineExceeded) {
				t.Errorf("ended at the deadline: %v", err)
			}
			got, err = c.CounterInterrogate(context.Background(), 1, asdu.CounterGeneral, asdu.FreezeRead)
			if len(got) != tc.want || !errors.Is(err, tc.err) || (tc.err == nil) != (err == nil) {
				t.Fatalf("CounterInterrogate: %d ASDUs, %v; want %d, %v", len(got), err, tc.want, tc.err)
			}
		})
	}
}

// A directory of more entries than WithMaxResult allows ends at the bound
// with the entries so far and ErrResultLimit, counted per entry, also
// across ASDUs of several entries.
func TestListFilesResultLimit(t *testing.T) {
	directory := func(n, perASDU int) string {
		return serveHandler(t, server.HandlerFunc(func(s *server.Session, req *asdu.ASDU) {
			for i := 0; i < n; i += perASDU {
				var objs []asdu.InformationObject
				for j := i; j < min(i+perASDU, n); j++ {
					e := asdu.FileDirectoryEntry{IOA: asdu.IOA(j + 1), Name: 1, Length: 10}
					if j == n-1 {
						e.Status = asdu.FileLastOfDirectory
					}
					objs = append(objs, e)
				}
				_ = s.Send(s.Context(), asdu.New(asdu.CauseRequest, req.CommonAddr, objs...))
			}
		}))
	}
	for _, tc := range []struct {
		name        string
		max, n, per int
		want        int
		err         error
	}{
		{"at the bound", 3, 3, 1, 3, nil},
		{"beyond, one entry per ASDU", 3, 50, 1, 3, telecontrol.ErrResultLimit},
		{"beyond, several entries per ASDU", 3, 50, 4, 3, telecontrol.ErrResultLimit},
		{"no bound", -1, 50, 1, 50, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := dial(t, directory(tc.n, tc.per), client.WithMaxResult(tc.max))
			got, err := c.ListFiles(context.Background(), 1, 0)
			if len(got) != tc.want || !errors.Is(err, tc.err) || (tc.err == nil) != (err == nil) {
				t.Fatalf("ListFiles: %d entries, %v; want %d, %v", len(got), err, tc.want, tc.err)
			}
		})
	}
}

// A station that keeps announcing empty sections does not keep a download
// going: after one section per name of section the transfer ends with
// ErrProtocol.
func TestGetFileSectionLimit(t *testing.T) {
	var sections atomic.Int64
	addr := serveHandler(t, server.HandlerFunc(func(s *server.Session, req *asdu.ASDU) {
		send := func(o asdu.InformationObject) {
			_ = s.Send(s.Context(), asdu.New(asdu.CauseFileTransfer, req.CommonAddr, o))
		}
		announce := func() {
			n := sections.Add(1)
			send(asdu.SectionReady{IOA: 1, Name: 1, Section: uint8(n%255 + 1)})
		}
		switch o := req.First().(type) {
		case asdu.FileCall:
			switch o.Qualifier & 0x0f {
			case asdu.FileSelect:
				send(asdu.FileReady{IOA: 1, Name: 1})
			case asdu.FileRequest:
				announce()
			case asdu.SectionRequest:
				send(asdu.FileLastSegment{IOA: 1, Name: 1, Section: o.Section, Qualifier: asdu.LastSectionNoDeactivation})
			}
		case asdu.FileAck:
			if o.Qualifier&0x0f == asdu.AckSectionPositive {
				announce()
			}
		}
	}))
	c := dial(t, addr)
	_, err := c.GetFile(context.Background(), 1, 1, 1)
	if !errors.Is(err, iec104.ErrProtocol) {
		t.Fatalf("GetFile: %v, want ErrProtocol", err)
	}
	if n := sections.Load(); n != 256 {
		t.Errorf("%d sections announced before the end, want 256", n)
	}
}
