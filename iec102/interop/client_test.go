//go:build interop

// SPDX-License-Identifier: MIT

package interop

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/otfabric/go-iec60870-5/iec102"
	"github.com/otfabric/go-iec60870-5/iec102/client"
	"github.com/otfabric/go-iec60870-5/iec102/ree"
)

// TestClientAgainstReferenceServer runs this module's client against the
// station of every image that has one (contract 2.2, "IEC 102 server"): the
// product, the clock and the absolute integrated totals of the baseline
// fixture. No reference has a station (no open IEC 102 controlled station
// exists), so today only otfabric runs here, and only when it is selected.
func TestClientAgainstReferenceServer(t *testing.T) {
	for _, a := range adapters(t) {
		if !a.caps.Roles.Server {
			continue
		}
		t.Run(a.name, func(t *testing.T) {
			f := loadFixture(t, a)
			port := startServer(t, a)
			conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 5*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			c, err := client.New(conn, client.Config{LinkAddr: 1, CommonAddr: uint16(f.Station.CommonAddress),
				Params: iec102.Params{Profile: ree.Profile, Location: time.UTC}})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = c.Close() }()
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			if err := c.Init(ctx); err != nil {
				t.Fatalf("link start-up: %v", err)
			}
			t.Run("read-product", func(t *testing.T) {
				p, err := c.ReadProductSpec(ctx)
				w := f.Station.Product
				if err != nil || p.Standard != w.Standard || p.Manufacturer != w.Manufacturer || p.Product != w.Product {
					t.Fatalf("%+v %v, want %+v", p, err, w)
				}
			})
			t.Run("read-time", func(t *testing.T) {
				b, err := c.ReadTime(ctx)
				if err != nil {
					t.Fatal(err)
				}
				got := fmt.Sprintf("%s:%02d.%03d", textA(b.TimeA), b.Second, b.Millisecond)
				if got != f.Station.Clock || b.Invalid {
					t.Fatalf("clock %s (IV %t), want %s", got, b.Invalid, f.Station.Clock)
				}
			})
			t.Run("read-totals", func(t *testing.T) {
				from, to := "2026-01-01T00:00", "2026-01-01T05:00"
				got, err := c.ReadTotals(ctx, 11, iec102.Read{T: iec102.C_CI_NT_2, First: 1, Last: 8, From: timeA(t, from), To: timeA(t, to)})
				if err != nil {
					t.Fatal(err)
				}
				var want []string
				for _, l := range f.Totals {
					if l.Kind != "absolute" || l.Record != 11 {
						continue
					}
					for _, p := range l.Periods {
						if p.End >= from && p.End <= to {
							want = append(want, fmt.Sprintf("%s %v", p.End, p.Totals))
						}
					}
				}
				var have []string
				for _, p := range got {
					ts := make([]total, 0, len(p.Totals))
					for _, o := range p.Totals {
						ts = append(ts, total{Address: o.Addr, Value: o.Value, Quality: uint8(o.Quality)})
					}
					have = append(have, fmt.Sprintf("%s %v", textA(p.Time), ts))
				}
				if len(want) == 0 || strings.Join(have, "\n") != strings.Join(want, "\n") {
					t.Fatalf("periods:\n%s\nwant:\n%s", strings.Join(have, "\n"), strings.Join(want, "\n"))
				}
			})
		})
	}
}

// startServer runs the station of an image and returns its host port.
func startServer(t *testing.T, a adapter) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	out, err := docker(ctx, "run", "-d", "-p", "127.0.0.1::2401", a.image, "server")
	if err != nil {
		t.Fatal(err)
	}
	id := strings.TrimSpace(string(out))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if t.Failed() {
			logs, _ := docker(ctx, "logs", id)
			t.Logf("%s server log:\n%s", a.name, logs)
		}
		_, _ = docker(ctx, "rm", "-f", id)
	})
	deadline := time.Now().Add(30 * time.Second)
	for {
		if _, err := docker(ctx, "exec", id, "test", "-f", "/run/iec60870-5-interop/ready"); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s server not ready", a.name)
		}
		time.Sleep(100 * time.Millisecond)
	}
	out, err = docker(ctx, "port", id, "2401/tcp")
	if err != nil {
		t.Fatal(err)
	}
	line := strings.TrimSpace(strings.Split(string(out), "\n")[0])
	port, err := strconv.Atoi(line[strings.LastIndex(line, ":")+1:])
	if err != nil {
		t.Fatalf("port %q: %v", out, err)
	}
	return port
}
