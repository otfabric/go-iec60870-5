//go:build interop

// SPDX-License-Identifier: MIT

package interop

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/otfabric/go-iec60870-5/ft12"
	"github.com/otfabric/go-iec60870-5/iec102"
	"github.com/otfabric/go-iec60870-5/iec102/ree"
	"github.com/otfabric/go-iec60870-5/iec102/server"
)

// gisceImage is the reference: the gisce/iec870ree v0.14.1 client of
// otfabric/iec60870-5-interop v0.3.0 (contract 2.2), pinned by multi-arch
// index digest (the release's manifest.json).
const gisceImage = "ghcr.io/otfabric/iec60870-5-interop-iec102-gisce@sha256:4d0e42bd267d066b04ddb48cc8542b1fc17da1485dc8c0aacecdec460b2c760a"

type adapter struct {
	name, image string
	caps        capabilities
}

type capabilities struct {
	Adapter  string `json:"adapter"`
	Standard string `json:"standard"`
	Roles    struct {
		Server bool `json:"server"`
		Client bool `json:"client"`
	} `json:"roles"`
	Features         map[string]bool `json:"features"`
	LinkAddressSizes []int           `json:"linkAddressSizes"`
	CASizes          []int           `json:"caSizes"`
	Profiles         []string        `json:"profiles"`
	ClientOperations []string        `json:"clientOperations"`
	Upstream         struct {
		Name, Version, Revision string
		IndependentEngine       bool `json:"independentEngine"`
	} `json:"upstream"`
}

func require() bool { return os.Getenv("IEC102_REQUIRE_INTEROP") == "1" }

// skipOrFail skips for missing infrastructure (Docker, the image), or fails
// with IEC102_REQUIRE_INTEROP=1.
func skipOrFail(t *testing.T, format string, args ...any) {
	t.Helper()
	if require() {
		t.Fatalf(format, args...)
	}
	t.Skipf(format, args...)
}

func docker(ctx context.Context, args ...string) ([]byte, error) {
	var out, errb bytes.Buffer
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return out.Bytes(), fmt.Errorf("docker %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return out.Bytes(), nil
}

var (
	gisceOnce sync.Once
	gisce     adapter
	gisceErr  error
)

// reference returns the gisce image and its capabilities.
func reference(t *testing.T) adapter {
	t.Helper()
	gisceOnce.Do(func() {
		img := gisceImage
		if v, ok := os.LookupEnv("IEC102_INTEROP_GISCE_IMAGE"); ok {
			img = v
		}
		if img == "" {
			gisceErr = errors.New("IEC102_INTEROP_GISCE_IMAGE is empty")
			return
		}
		if _, err := exec.LookPath("docker"); err != nil {
			gisceErr = err
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		out, err := docker(ctx, "run", "--rm", img, "print-capabilities")
		if err != nil {
			gisceErr = err
			return
		}
		gisce = adapter{name: "gisce", image: img}
		if err := json.Unmarshal(out, &gisce.caps); err != nil {
			gisceErr = fmt.Errorf("gisce capabilities: %w", err)
			return
		}
		if gisce.caps.Standard != "iec60870-5-102" || !gisce.caps.Roles.Client {
			gisceErr = fmt.Errorf("%s is not an IEC 102 client image: %s", img, out)
		}
	})
	if gisceErr != nil {
		skipOrFail(t, "reference unavailable: %v", gisceErr)
	}
	return gisce
}

// fixture is the IEC 102 baseline fixture as the reference prints it
// (iec60870-5-interop docs/FIXTURES.md, "IEC 102").
type fixture struct {
	Station struct {
		CommonAddress int    `json:"commonAddress"`
		AccessKey     uint32 `json:"accessKey"`
		Clock         string `json:"clock"`
		Product       struct {
			Standard     uint8  `json:"standard"`
			Manufacturer uint8  `json:"manufacturer"`
			Product      uint32 `json:"product"`
		} `json:"product"`
	} `json:"station"`
	Totals []struct {
		Record  uint8  `json:"record"`
		Kind    string `json:"kind"`
		Periods []struct {
			End    string  `json:"end"`
			Totals []total `json:"totals"`
		} `json:"periods"`
	} `json:"totals"`
	Tariffs []struct {
		Register uint8          `json:"register"`
		Current  []tariffRecord `json:"current"`
		Stored   []tariffRecord `json:"stored"`
	} `json:"tariffs"`
}

// total is an integrated total as the fixture and the documents write it.
type total struct {
	Address uint8  `json:"address"`
	Value   uint32 `json:"value"`
	Quality uint8  `json:"quality"`
}

// tariffRecord is a record of M_TA_VC_2 / M_TA_VM_2 as the fixture and the
// documents write it.
type tariffRecord struct {
	Address            uint8  `json:"address"`
	ActiveAbs          uint32 `json:"activeAbs"`
	ActiveInc          uint32 `json:"activeInc"`
	ActiveQuality      uint8  `json:"activeQuality"`
	ReactiveIndAbs     uint32 `json:"reactiveIndAbs"`
	ReactiveIndInc     uint32 `json:"reactiveIndInc"`
	ReactiveIndQuality uint8  `json:"reactiveIndQuality"`
	ReactiveCapAbs     uint32 `json:"reactiveCapAbs"`
	ReactiveCapInc     uint32 `json:"reactiveCapInc"`
	ReactiveCapQuality uint8  `json:"reactiveCapQuality"`
	MaxPower           uint32 `json:"maxPower"`
	MaxPowerTime       string `json:"maxPowerTime"`
	MaxPowerQuality    uint8  `json:"maxPowerQuality"`
	ExcessPower        uint32 `json:"excessPower"`
	ExcessPowerQuality uint8  `json:"excessPowerQuality"`
	PeriodStart        string `json:"periodStart"`
	PeriodEnd          string `json:"periodEnd"`
}

func loadFixture(t *testing.T, a adapter) *fixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	out, err := docker(ctx, "run", "--rm", a.image, "print-fixture", "baseline")
	if err != nil {
		t.Fatal(err)
	}
	var f fixture
	if err := json.Unmarshal(out, &f); err != nil {
		t.Fatal(err)
	}
	if len(f.Totals) == 0 || len(f.Tariffs) == 0 {
		t.Fatalf("fixture without totals or tariffs: %s", out)
	}
	return &f
}

// Time formats of the contract: the station's local time without a zone.
const (
	layoutA = "2006-01-02T15:04"
	layoutB = "2006-01-02T15:04:05.000"
)

// timeA converts a fixture time a; the station's location is UTC, so the
// wall clock fields are the fixture's and SU is 0 (standard time).
func timeA(t *testing.T, s string) iec102.TimeA {
	t.Helper()
	tt, err := time.Parse(layoutA, s)
	if err != nil {
		t.Fatalf("fixture time %q: %v", s, err)
	}
	return iec102.TimeAOf(tt, time.UTC)
}

func textA(a iec102.TimeA) string {
	return fmt.Sprintf("20%02d-%02d-%02dT%02d:%02d", a.Year, a.Month, a.Day, a.Hour, a.Minute)
}

// station serves the fixture with this module's server, following
// iec60870-5-interop docs/FIXTURES.md, "Station behaviour (IEC 102)". The
// store and the clock live across connections; every connection gets a
// fresh server (link state, class 2 queue, session).
type station struct {
	f     *fixture
	ca    uint16
	store *server.Store

	mu    sync.Mutex
	clock time.Time // stands still until set-time
	sets  []iec102.TimeB
}

func newStation(t *testing.T, f *fixture) *station {
	t.Helper()
	st := &station{f: f, ca: uint16(f.Station.CommonAddress), store: server.NewStore()}
	c, err := time.Parse(layoutB, f.Station.Clock)
	if err != nil {
		t.Fatalf("fixture clock %q: %v", f.Station.Clock, err)
	}
	st.clock = c
	for _, l := range f.Totals {
		typ := iec102.M_IT_TG_2
		if l.Kind == "incremental" {
			typ = iec102.M_IT_TK_2
		}
		for _, p := range l.Periods {
			body := iec102.IntegratedTotals{T: typ, Time: timeA(t, p.End)}
			for _, o := range p.Totals {
				body.Totals = append(body.Totals, iec102.IntegratedTotal{Addr: o.Address, Value: o.Value, Quality: iec102.Quality(o.Quality)})
			}
			st.store.AddPeriod(l.Record, body)
		}
	}
	return st
}

func (st *station) now() time.Time {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.clock
}

func (st *station) setTime(tb iec102.TimeB) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.sets = append(st.sets, tb)
	if t, ok := tb.Time(time.UTC); ok {
		st.clock = t
	}
}

func (st *station) config(t *testing.T) server.Config {
	t.Helper()
	p := st.f.Station.Product
	cfg := server.Config{
		LinkAddr:    1,
		CommonAddr:  st.ca,
		Params:      iec102.Params{Profile: ree.Profile, Location: time.UTC},
		ProductSpec: iec102.ProductSpec{Standard: p.Standard, Manufacturer: p.Manufacturer, Product: p.Product},
		Store:       st.store,
		Clock:       st.now,
	}
	if os.Getenv("IEC102_INTEROP_TRACE") == "1" {
		cfg.Link.OnFrame = func(tx bool, f ft12.Frame) {
			dir := "rx"
			if tx {
				dir = "tx"
			}
			t.Logf("station %s %s % X", dir, f, f.Data)
		}
	}
	return cfg
}

// HandleASDU answers the tariff requests of the REE profile (the session
// requests are ree.Session's): C_TA_VC_2 with the fixture's current
// records, C_TA_VM_2 with its stored records whose end lies within the
// range, each as confirmation, one record per M_TA_VC_2 / M_TA_VM_2 and
// termination; cause 16 for a register without tariff information, 18 when
// no record matches. Anything else is refused with cause 14.
func (st *station) HandleASDU(s *server.Server, a *iec102.ASDU) {
	q, ok := a.Body.(ree.Request)
	if !ok || (q.T != ree.C_TA_VC_2 && q.T != ree.C_TA_VM_2) {
		_ = s.Mirror(a, iec102.CauseUnknownType)
		return
	}
	var recs []tariffRecord
	known := false
	for _, tr := range st.f.Tariffs {
		if tr.Register != a.Record {
			continue
		}
		known = true
		if q.T == ree.C_TA_VC_2 {
			recs = tr.Current
			continue
		}
		lo, hi := textA(q.From), textA(q.To)
		for _, r := range tr.Stored {
			if r.PeriodEnd >= lo && r.PeriodEnd <= hi {
				recs = append(recs, r)
			}
		}
	}
	switch {
	case !known:
		_ = s.Mirror(a, iec102.CauseUnknownRecordAddr)
		return
	case len(recs) == 0:
		_ = s.Mirror(a, iec102.CauseNoPeriod)
		return
	}
	answer := ree.M_TA_VC_2
	if q.T == ree.C_TA_VM_2 {
		answer = ree.M_TA_VM_2
	}
	_ = s.Mirror(a, iec102.CauseActivationCon)
	for _, r := range recs {
		_ = s.Send(iec102.New(iec102.CauseRequest, a.CommonAddr, a.Record, ree.TariffInfo{T: answer, Records: []ree.TariffRecord{st.tariff(r)}}))
	}
	_ = s.Mirror(a, iec102.CauseActivationTerm)
}

func (st *station) tariff(r tariffRecord) ree.TariffRecord {
	parse := func(s string) iec102.TimeA {
		tt, _ := time.Parse(layoutA, s)
		return iec102.TimeAOf(tt, time.UTC)
	}
	return ree.TariffRecord{
		Addr: r.Address, ActiveAbs: r.ActiveAbs, ActiveInc: r.ActiveInc, ActiveQual: r.ActiveQuality,
		ReactiveIndAbs: r.ReactiveIndAbs, ReactiveIndInc: r.ReactiveIndInc, ReactiveIndQual: r.ReactiveIndQuality,
		ReactiveCapAbs: r.ReactiveCapAbs, ReactiveCapInc: r.ReactiveCapInc, ReactiveCapQual: r.ReactiveCapQuality,
		MaxPower: r.MaxPower, MaxPowerTime: parse(r.MaxPowerTime), MaxPowerQual: r.MaxPowerQuality,
		ExcessPower: r.ExcessPower, ExcessPowerQual: r.ExcessPowerQuality,
		Start: parse(r.PeriodStart), End: parse(r.PeriodEnd),
	}
}

// serve accepts connections on ln one at a time, as a line has one
// controlling station, and runs a fresh server with a fresh REE session on
// each.
func serve(t *testing.T, ln net.Listener, st *station) {
	t.Helper()
	cfg := st.config(t)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			c := cfg
			c.Handler = &ree.Session{Key: st.f.Station.AccessKey, OnSetTime: st.setTime, Next: st}
			s, err := server.New(conn, c)
			if err != nil {
				t.Error(err)
				_ = conn.Close()
				continue
			}
			<-s.Done()
			_ = s.Close()
		}
	}()
	t.Cleanup(func() { _ = ln.Close(); <-done })
}

// object is an information object of an IEC 102 ASDU document (contract
// 2.2, "IEC 102 ASDU documents"): an integrated total, a time, a product
// identification, a tariff record or the elements of a mirrored request.
type object struct {
	tariffRecord
	Value        uint32 `json:"value"`
	Quality      uint8  `json:"quality"`
	Time         string `json:"time"`
	SU           bool   `json:"su"`
	TimeInvalid  bool   `json:"timeInvalid"`
	Standard     uint8  `json:"standard"`
	Manufacturer uint8  `json:"manufacturer"`
	Product      uint32 `json:"product"`
	First        uint8  `json:"first"`
	Last         uint8  `json:"last"`
	From         string `json:"from"`
	To           string `json:"to"`
}

type asduDoc struct {
	Type          string   `json:"type"`
	TypeID        int      `json:"typeId"`
	Cot           int      `json:"cot"`
	Negative      bool     `json:"negative"`
	CommonAddress int      `json:"commonAddress"`
	Record        int      `json:"record"`
	Count         int      `json:"count"`
	Objects       []object `json:"objects"`
	Time          string   `json:"time"`
	SU            bool     `json:"su"`
	TimeInvalid   bool     `json:"timeInvalid"`
}

type confirmation struct {
	Cot      int  `json:"cot"`
	Negative bool `json:"negative"`
}

// result is the client result document of the contract.
type result struct {
	OK    bool `json:"ok"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
	Connected     bool           `json:"connected"`
	Confirmations []confirmation `json:"confirmations"`
	Terminated    bool           `json:"terminated"`
	Session       *struct {
		Login  bool `json:"login"`
		Logout bool `json:"logout"`
	} `json:"session"`
	ASDUs []asduDoc `json:"asdus"`
}

func (r result) code() string {
	if r.Error == nil {
		return "-"
	}
	return r.Error.Code
}

// of returns the ASDUs of a type identification.
func (r result) of(typeID int) []asduDoc {
	var out []asduDoc
	for _, a := range r.ASDUs {
		if a.TypeID == typeID {
			out = append(out, a)
		}
	}
	return out
}

// runClient runs one client operation of the reference against port on this
// host (host.docker.internal in the container) and returns its result and
// exit code.
func runClient(ctx context.Context, a adapter, port int, op []string) (result, int, error) {
	args := []string{"run", "--rm", "--add-host=host.docker.internal:host-gateway", a.image, "client"}
	args = append(args, op...)
	args = append(args, "--host", "host.docker.internal", "--port", strconv.Itoa(port))
	var out, errb bytes.Buffer
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	code := 0
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		code = ee.ExitCode()
	} else if err != nil {
		return result{}, -1, err
	}
	var r result
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &r); err != nil {
		return r, code, fmt.Errorf("result %q (stderr %q): %w", out.String(), errb.String(), err)
	}
	return r, code, nil
}

// knownBug is an entry of the harness's known-bugs.tsv.
type knownBug struct {
	adapter, version string
	match            *regexp.Regexp
	what, id, url    string
}

func knownBugs(t *testing.T) []knownBug {
	t.Helper()
	f, err := os.Open("testdata/known-bugs.tsv")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	var bugs []knownBug
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		col := strings.Split(line, "\t")
		if len(col) != 6 {
			t.Fatalf("known-bugs.tsv: %d columns in %q", len(col), line)
		}
		re, err := regexp.Compile(col[2])
		if err != nil {
			t.Fatalf("known-bugs.tsv: %v", err)
		}
		bugs = append(bugs, knownBug{adapter: col[0], version: col[1], match: re, what: col[3], id: col[4], url: col[5]})
	}
	return bugs
}

// knownDifference skips a test whose failure a known bug of the reference
// explains (never counted as a pass) and reports whether it did.
func knownDifference(t *testing.T, a adapter, op []string, got string) bool {
	t.Helper()
	s := strings.Join(op, " ")
	for _, b := range knownBugs(t) {
		if b.adapter == a.name && b.version == a.caps.Upstream.Version && b.match.MatchString(s) {
			t.Skipf("known difference %s (%s %s): %s; got %s; %s", b.id, b.adapter, b.version, b.what, got, b.url)
			return true
		}
	}
	return false
}
