//go:build interop

// SPDX-License-Identifier: MIT

package interop

import (
	"bufio"
	"context"
	"net"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

type tcase struct {
	id, state, summary, needs string
	exit                      int
	op                        []string
}

func cases(t *testing.T) []tcase {
	t.Helper()
	f, err := os.Open("testdata/cases.tsv")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	var cs []tcase
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		col := strings.Split(line, "\t")
		if len(col) != 6 {
			t.Fatalf("cases.tsv: %d columns in %q", len(col), line)
		}
		exit, _ := strconv.Atoi(col[2])
		cs = append(cs, tcase{id: col[0], state: col[1], exit: exit, summary: col[3], needs: col[4], op: strings.Fields(col[5])})
	}
	return cs
}

// The reference client of every stack replays the harness's IEC 101 cases
// against this module's server, which serves the reference's own fixture.
// Each case must give the result the harness expects of any server.
func TestReferenceClientAgainstServer(t *testing.T) {
	for _, a := range adapters(t) {
		f := loadFixture(t, a)
		for _, mode := range modes {
			if !containsString(a.caps.LinkModes, mode) {
				continue
			}
			t.Run(a.name+"/"+mode, func(t *testing.T) {
				// One station per set of link flags (contract 2.3: both ends of
				// a line get the same field sizes); a case that changes the
				// station gets a fresh one.
				listeners := map[string]net.Listener{}
				restart := func(key string, fs fields) net.Listener {
					if ln := listeners[key]; ln != nil {
						_ = ln.Close()
					}
					ln, err := net.Listen("tcp", "0.0.0.0:0")
					if err != nil {
						t.Fatal(err)
					}
					st, err := newStation(f)
					if err != nil {
						t.Fatal(err)
					}
					serve(t, ln, paramsFor(mode, fs), uint16(fs.linkAddr), st)
					listeners[key] = ln
					return ln
				}
				for _, c := range cases(t) {
					if c.needs != "-" && !satisfied(a, c.needs) {
						continue
					}
					fs, key := caseFields(c.op)
					if !a.takes(fs, mode) || !a.clientCanSend(c.op, fs, mode) {
						continue // the reference declares it cannot (contract 2.3)
					}
					ln := listeners[key]
					if ln == nil || c.state == "rw" {
						ln = restart(key, fs)
					}
					t.Run(c.id, func(t *testing.T) {
						ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
						defer cancel()
						r, code, err := runClient(ctx, a, ln.Addr().(*net.TCPAddr).Port, mode, c.op)
						if err != nil {
							t.Fatal(err)
						}
						if got := r.summary(); got != c.summary || code != c.exit {
							if bug := knownBug(t, a, "client", mode, c.op); bug != "" {
								t.Skipf("known bug of %s %s: %s", a.name, a.caps.Upstream.Version, bug)
							}
							t.Errorf("%s:\n got  exit %d %s\n want exit %d %s", strings.Join(c.op, " "), code, got, c.exit, c.summary)
						}
					})
				}
			})
		}
	}
}

// satisfied reports whether the reference client declares the client
// features a case needs. Server features are this module's: the station
// above serves the baseline only, without groups.
func satisfied(a adapter, needs string) bool {
	for _, n := range strings.Split(needs, ",") {
		side, feat, _ := strings.Cut(n, ":")
		if side == "server" || !a.caps.Features[feat] {
			return false
		}
	}
	return true
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
