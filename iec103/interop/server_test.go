//go:build interop

// SPDX-License-Identifier: MIT

package interop

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"slices"
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

// clients returns the references that have a client.
func clients(t *testing.T) []adapter {
	t.Helper()
	var cs []adapter
	for _, a := range adapters(t) {
		if a.caps.Roles.Client {
			cs = append(cs, a)
		}
	}
	if len(cs) == 0 {
		skipOrFail(t, "no reference client selected")
	}
	return cs
}

// satisfied reports whether the reference client declares the client
// features a case needs. The server features are this module's: the
// station serves identification, general commands and measurands.
func satisfied(a adapter, needs string) bool {
	if needs == "-" {
		return true
	}
	for _, n := range strings.Split(needs, ",") {
		side, feat, _ := strings.Cut(n, ":")
		if side == "client" && !a.caps.Features[feat] {
			return false
		}
	}
	return true
}

// host runs this module's server on a fresh listener with a fresh station;
// restart replaces both, for the "rw" cases.
type host struct {
	t  *testing.T
	f  *fixture
	ln net.Listener
}

func (h *host) restart() {
	h.t.Helper()
	if h.ln != nil {
		_ = h.ln.Close()
	}
	var err error
	if h.ln, err = net.Listen("tcp", "0.0.0.0:0"); err != nil {
		h.t.Fatal(err)
	}
	serve(h.t, h.ln, newStation(h.f))
}

func (h *host) port() int { return h.ln.Addr().(*net.TCPAddr).Port }

func (h *host) run(t *testing.T, a adapter, op ...string) (result, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	r, code, err := runClient(ctx, a, h.port(), op)
	if err != nil {
		t.Fatal(err)
	}
	return r, code
}

// The reference client of every stack replays the harness's IEC 103 cases
// against this module's server, which serves the reference's own fixture.
// Each case must give the result the harness expects of any server.
func TestReferenceClientAgainstServer(t *testing.T) {
	for _, a := range clients(t) {
		f := loadFixture(t, a)
		t.Run(a.name, func(t *testing.T) {
			h := &host{t: t, f: f}
			h.restart()
			for _, c := range cases(t) {
				if !satisfied(a, c.needs) {
					continue
				}
				if c.state == "rw" {
					h.restart()
				}
				t.Run(c.id, func(t *testing.T) {
					r, code := h.run(t, a, c.op...)
					got := r.summary()
					if got == c.summary && code == c.exit {
						return
					}
					op := strings.Join(c.op, " ")
					if b, ok := knownBugOf(t, a, true, op); ok {
						knownDifference(t, b, "exit "+strconv.Itoa(code)+" "+got)
					}
					msg := ""
					if r.Error != nil {
						msg = r.Error.Message
					}
					t.Errorf("%s:\n got  exit %d %s (%s)\n want exit %d %s", op, code, got, msg, c.exit, c.summary)
				})
			}
		})
	}
}

// valueCheck compares a value the harness checks (scripts/interop-iec103.sh)
// as JSON; a known bug of the client explains a difference.
func valueCheck(t *testing.T, a adapter, label string, got, want any) {
	t.Helper()
	g, _ := json.Marshal(got)
	w, _ := json.Marshal(want)
	if string(g) == string(w) {
		return
	}
	if b, ok := knownBugOf(t, a, true, label); ok {
		knownDifference(t, b, string(g))
	}
	t.Errorf("%s:\n got  %s\n want %s", label, g, w)
}

type indication struct {
	Fun int `json:"fun"`
	Inf int `json:"inf"`
	DPI int `json:"dpi"`
	SIN int `json:"sin"`
}

func intOf(p *int) int {
	if p == nil {
		return -1
	}
	return *p
}

func strOf(p *string) string {
	if p == nil {
		return "<none>"
	}
	return *p
}

func sortIndications(s []indication) {
	slices.SortFunc(s, func(a, b indication) int {
		if a.Fun != b.Fun {
			return a.Fun - b.Fun
		}
		return a.Inf - b.Inf
	})
}

// The values and sessions of scripts/interop-iec103.sh: the interrogation
// returns the fixture's indications with the scan number, the
// identification after the reset, a command confirmed with the new state
// and the RII and visible to the next interrogation, the measurands as
// class 2 data, an idle line and the next connection.
func TestReferenceClientValues(t *testing.T) {
	for _, a := range clients(t) {
		f := loadFixture(t, a)
		t.Run(a.name, func(t *testing.T) {
			h := &host{t: t, f: f}
			h.restart()
			t.Run("values.interrogation", func(t *testing.T) {
				r, _ := h.run(t, a, "interrogate", "--scn", "5")
				got := []indication{}
				for _, x := range r.ASDUs {
					if (x.TypeID == 1 || x.TypeID == 2) && x.Cot == 9 && len(x.Objects) > 0 {
						o := x.Objects[0]
						got = append(got, indication{o.Fun, o.Inf, intOf(o.DPI), intOf(o.SIN)})
					}
				}
				want := []indication{}
				for _, ind := range f.Indications {
					if ind.GI == nil || *ind.GI {
						want = append(want, indication{ind.Fun, ind.Inf, ind.DPI, 5})
					}
				}
				sortIndications(got)
				sortIndications(want)
				valueCheck(t, a, "interrogate --scn 5", got, want)
			})
			t.Run("values.identification", func(t *testing.T) {
				if !a.caps.Features["identification"] {
					t.Skip("client feature identification not declared")
				}
				r, _ := h.run(t, a, "connect", "--hold-ms", "300")
				type ident struct {
					Fun      int    `json:"fun"`
					Inf      int    `json:"inf"`
					Col      int    `json:"col"`
					ASCII    string `json:"ascii"`
					Software string `json:"software"`
				}
				got := []ident{}
				for _, x := range r.ASDUs {
					if x.TypeID == 5 && x.Cot == 4 && len(x.Objects) > 0 {
						o := x.Objects[0]
						got = append(got, ident{o.Fun, o.Inf, intOf(o.Col), strOf(o.ASCII), strOf(o.Software)})
					}
				}
				id := f.Station.Identification
				valueCheck(t, a, "identification", got, []ident{{id.Fun, 3, id.Col, id.ASCII, id.Software}})
			})
			t.Run("values.command", func(t *testing.T) {
				if !a.caps.Features["generalCommand"] {
					t.Skip("client feature generalCommand not declared")
				}
				h.restart()
				r, _ := h.run(t, a, "command", "--fun", "160", "--inf", "17", "--dco", "on", "--rii", "9")
				type conf struct {
					Cot int `json:"cot"`
					indication
				}
				got := []conf{}
				for _, x := range r.ASDUs {
					if (x.TypeID == 1 || x.TypeID == 2) && (x.Cot == 20 || x.Cot == 21) && len(x.Objects) > 0 {
						o := x.Objects[0]
						got = append(got, conf{20, indication{o.Fun, o.Inf, intOf(o.DPI), intOf(o.SIN)}})
					}
				}
				valueCheck(t, a, "command --fun 160 --inf 17", got, []conf{{20, indication{160, 17, 2, 9}}})
				r, _ = h.run(t, a, "interrogate")
				after := []int{}
				for _, x := range r.ASDUs {
					if x.Cot == 9 && len(x.Objects) > 0 && x.Objects[0].Fun == 160 && x.Objects[0].Inf == 17 {
						after = append(after, intOf(x.Objects[0].DPI))
					}
				}
				valueCheck(t, a, "command --fun 160 --inf 17", after, []int{2})
			})
			t.Run("values.measurands", func(t *testing.T) {
				if !a.caps.Features["measurands"] {
					t.Skip("client feature measurands not declared")
				}
				r, _ := h.run(t, a, "monitor", "--duration-ms", "2000")
				type meas struct {
					Type   int     `json:"type"`
					Fun    int     `json:"fun"`
					Inf    int     `json:"inf"`
					Values []value `json:"values"`
				}
				seen := map[funInf]bool{}
				got := []meas{}
				for _, x := range r.ASDUs {
					if x.Cot == 2 && (x.TypeID == 3 || x.TypeID == 9) && len(x.Objects) > 0 {
						o := x.Objects[0]
						if !seen[funInf{o.Fun, o.Inf}] {
							seen[funInf{o.Fun, o.Inf}] = true
							got = append(got, meas{x.TypeID, o.Fun, o.Inf, o.Values})
						}
					}
				}
				want := []meas{}
				for _, m := range f.Measurands {
					want = append(want, meas{m.Type, m.Fun, m.Inf, m.Values})
				}
				order := func(a, b meas) int {
					if a.Fun != b.Fun {
						return a.Fun - b.Fun
					}
					return a.Inf - b.Inf
				}
				slices.SortFunc(got, order)
				slices.SortFunc(want, order)
				valueCheck(t, a, "monitor measurands", got, want)
			})
			t.Run("session.idle-hold", func(t *testing.T) {
				r, _ := h.run(t, a, "connect", "--hold-ms", "3000")
				if !r.OK || !r.Connected {
					t.Errorf("idle line: %s", r.summary())
				}
			})
			t.Run("session.after-idle", func(t *testing.T) {
				r, _ := h.run(t, a, "interrogate", "--scn", "1")
				if !r.OK || !r.Terminated {
					t.Errorf("interrogation after an idle connection: %s", r.summary())
				}
			})
		})
	}
}
