//go:build interop

// SPDX-License-Identifier: MIT

package interop

import (
	"bufio"
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/otfabric/go-iec60870-5/ft12"
	"github.com/otfabric/go-iec60870-5/iec103"
	"github.com/otfabric/go-iec60870-5/iec103/server"
)

// The references: otfabric/iec60870-5-interop, contract 2.3.
//
// TODO: pin these by multi-arch index digest from the manifest.json of the
// harness release that carries contract 2.3 (the generic services and the
// transmission of disturbance data); until then they are the local images
// built from the harness branch (make image-iec103-<stack>, tag dev). The
// v0.3.0 digests (contract 2.2) were:
//
//	vitusdong sha256:a00e8ef368d96846c80223e87418b973be16d763f09211aa052ac61227f412a7
//	ariec103  sha256:95f94fce044dff5973b8859da75063d2ff8d3f1f8a26d433705575b487771cd9
//	open103   sha256:b632442d67ce8f247b224819308ec8c0711b91260e5ebefa0f6cc91f3327f68d
//	goiecp5   sha256:43368aca1dd9dc2822f9fc3fced3215fa5a69ed5d5a4f41f372a9aaf273aae6a
var stacks = []struct{ name, env, image string }{
	{"vitusdong", "IEC103_INTEROP_VITUSDONG_IMAGE", "ghcr.io/otfabric/iec60870-5-interop-iec103-vitusdong:dev"},
	{"ariec103", "IEC103_INTEROP_ARIEC103_IMAGE", "ghcr.io/otfabric/iec60870-5-interop-iec103-ariec103:dev"},
	{"open103", "IEC103_INTEROP_OPEN103_IMAGE", "ghcr.io/otfabric/iec60870-5-interop-iec103-open103:dev"},
	{"goiecp5", "IEC103_INTEROP_GOIECP5_IMAGE", "ghcr.io/otfabric/iec60870-5-interop-iec103-goiecp5:dev"},
}

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
	Features map[string]bool `json:"features"`
	Upstream struct {
		Name, Version, Revision string
		IndependentEngine       bool `json:"independentEngine"`
	} `json:"upstream"`
}

func require() bool { return os.Getenv("IEC103_REQUIRE_INTEROP") == "1" }

// skipOrFail skips for missing infrastructure (Docker, an image), or fails
// with IEC103_REQUIRE_INTEROP=1. Known bugs of a reference are skipped with
// knownDifference instead, whatever the switch says.
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
	err := cmd.Run()
	if err != nil {
		return out.Bytes(), fmt.Errorf("docker %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return out.Bytes(), nil
}

var (
	adaptersOnce sync.Once
	adapterList  []adapter
	adapterErr   error
)

func adapters(t *testing.T) []adapter {
	t.Helper()
	adaptersOnce.Do(func() {
		if _, err := exec.LookPath("docker"); err != nil {
			adapterErr = err
			return
		}
		only := os.Getenv("IEC103_INTEROP_ADAPTERS")
		for _, s := range stacks {
			if only != "" && !slices.Contains(strings.Split(only, ","), s.name) {
				continue
			}
			img := s.image
			if v, ok := os.LookupEnv(s.env); ok {
				img = v
			}
			if img == "" {
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			out, err := docker(ctx, "run", "--rm", img, "print-capabilities")
			cancel()
			if err != nil {
				adapterErr = err
				return
			}
			a := adapter{name: s.name, image: img}
			if err := json.Unmarshal(out, &a.caps); err != nil {
				adapterErr = fmt.Errorf("%s capabilities: %w", s.name, err)
				return
			}
			if a.caps.Standard != "iec60870-5-103" {
				adapterErr = fmt.Errorf("%s is %q, not an IEC 103 image", img, a.caps.Standard)
				return
			}
			adapterList = append(adapterList, a)
		}
	})
	if adapterErr != nil {
		skipOrFail(t, "references unavailable: %v", adapterErr)
	}
	if len(adapterList) == 0 {
		skipOrFail(t, "no reference selected")
	}
	return adapterList
}

// fixture is the IEC 103 baseline fixture as the reference prints it
// (iec60870-5-interop docs/FIXTURES.md, "IEC 103").
type fixture struct {
	Station struct {
		CommonAddress  int `json:"commonAddress"`
		Identification struct {
			Fun      int    `json:"fun"`
			Col      int    `json:"col"`
			ASCII    string `json:"ascii"`
			Software string `json:"software"`
		} `json:"identification"`
	} `json:"station"`
	Indications []struct {
		Fun int   `json:"fun"`
		Inf int   `json:"inf"`
		DPI int   `json:"dpi"`
		GI  *bool `json:"gi"`
	} `json:"indications"`
	Measurands []struct {
		Fun    int     `json:"fun"`
		Inf    int     `json:"inf"`
		Type   int     `json:"type"`
		Values []value `json:"values"`
	} `json:"measurands"`
	Commands     []command            `json:"commands"`
	Generic      []genericEntry       `json:"generic"`
	Disturbances []fixtureDisturbance `json:"disturbances"`
}

// command is a general command the fixture's device takes.
type command struct {
	Fun int `json:"fun"`
	Inf int `json:"inf"`
}

// value is a measurand as the fixture and the result documents write it.
type value struct {
	Value int  `json:"value"`
	OV    bool `json:"ov"`
	ER    bool `json:"er"`
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
	if len(f.Indications) == 0 {
		t.Fatalf("%s: fixture without indications: %s", a.name, out)
	}
	return &f
}

type funInf struct{ fun, inf int }

// station serves the fixture with this module's server, following
// iec60870-5-interop docs/FIXTURES.md, "Station behaviour (IEC 103)". Its
// process state (the DPI of the indications) lives across connections,
// as the contract's servers do; the link state and the class 1 queue start
// afresh with every connection, as every connection gets a fresh server.
type station struct {
	f  *fixture
	ca uint8

	mu      sync.Mutex
	dpi     map[funInf]iec103.DPI
	generic map[iec103.GIN]iec103.Value // the actual values (KOD 1)
}

func newStation(f *fixture) *station {
	st := &station{f: f, ca: uint8(f.Station.CommonAddress), dpi: map[funInf]iec103.DPI{}, generic: map[iec103.GIN]iec103.Value{}}
	for _, ind := range f.Indications {
		st.dpi[funInf{ind.Fun, ind.Inf}] = iec103.DPI(ind.DPI)
	}
	for _, g := range f.Generic {
		st.generic[g.gin()] = g.actual()
	}
	return st
}

func (st *station) config() (server.Config, error) {
	id := st.f.Station.Identification
	sw, err := hex.DecodeString(id.Software)
	if err != nil || len(sw) != 4 {
		return server.Config{}, fmt.Errorf("fixture software %q: %v", id.Software, err)
	}
	return server.Config{
		LinkAddr:       1,
		CommonAddr:     st.ca,
		Fun:            iec103.FunctionType(id.Fun),
		Identification: iec103.IdentificationOf(uint8(id.Col), id.ASCII, [4]byte(sw)),
		Handler:        st,
		Disturbances:   st,
		SyncDelay:      -1, // TCP, not a serial line
	}, nil
}

func (st *station) HandleASDU(s *server.Server, a *iec103.ASDU) {
	st.mu.Lock()
	defer st.mu.Unlock()
	switch b := a.Body.(type) {
	case iec103.GIInit:
		// ASDU 1, cause 9, SIN the scan number (set by AnswerGI), for every
		// indication with gi not false, in the fixture's order; then ASDU 8.
		var data []*iec103.ASDU
		for _, ind := range st.f.Indications {
			if ind.GI != nil && !*ind.GI {
				continue
			}
			data = append(data, iec103.New(iec103.CauseGI, st.ca, iec103.FunctionType(ind.Fun), iec103.InfoNumber(ind.Inf),
				iec103.TimeTagged{DPI: st.dpi[funInf{ind.Fun, ind.Inf}], Time: s.Now()}))
		}
		_ = s.AnswerGI(a, data...)
	case iec103.GeneralCommand:
		// A command of the fixture with DCO 1 or 2 sets the indication of
		// the same FUN/INF and is confirmed with cause 20; when the state
		// changed, the change follows with cause 12 (clause 7.4.4, figure
		// 7). Anything else is refused with cause 21 (DCO 0 and 3, and OFF
		// where table 17 allows only ON, the server refuses itself).
		k := funInf{int(a.Fun), int(a.Inf)}
		if !slices.Contains(st.f.Commands, command{k.fun, k.inf}) {
			_ = s.AckCommand(a, false)
			return
		}
		changed := st.dpi[k] != b.DCO
		st.dpi[k] = b.DCO
		_ = s.AckCommand(a, true)
		if changed {
			_ = s.Event(iec103.CauseRemoteOperation, a.Fun, a.Inf, b.DCO)
		}
	case iec103.GenericCommand:
		st.genericRead(s, a, b)
	case iec103.GenericData:
		st.genericWrite(s, a, b)
	}
}

// serve accepts connections on ln one at a time, as a line has one
// controlling system, and runs a fresh server on each. The measurands of
// the fixture are kept queued as class 2 data, one after the other,
// cyclically (cause 2).
func serve(t *testing.T, ln net.Listener, st *station) {
	t.Helper()
	cfg, err := st.config()
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("IEC103_INTEROP_TRACE") == "1" {
		cfg.Link.OnFrame = func(tx bool, f ft12.Frame) {
			dir := "rx"
			if tx {
				dir = "tx"
			}
			t.Logf("server %s %s % X", dir, f, f.Data)
		}
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			w := &watched{Conn: conn, closed: make(chan struct{})}
			s, err := server.New(w, cfg)
			if err != nil {
				t.Error(err)
				_ = conn.Close()
				continue
			}
			stop := make(chan struct{})
			fed := make(chan struct{})
			go func() { defer close(fed); st.feed(s, stop) }()
			<-w.closed
			close(stop)
			<-fed
			_ = s.Close()
		}
	}()
	t.Cleanup(func() { _ = ln.Close(); <-done })
}

// feed keeps the next measurand of the fixture queued as class 2 data.
func (st *station) feed(s *server.Server, stop <-chan struct{}) {
	if len(st.f.Measurands) == 0 {
		return
	}
	tick := time.NewTicker(2 * time.Millisecond)
	defer tick.Stop()
	for i := 0; ; {
		if s.Pending(server.Class2) == 0 {
			m := st.f.Measurands[i%len(st.f.Measurands)]
			var vals []iec103.MEA
			for _, v := range m.Values {
				vals = append(vals, iec103.MEA{Raw: int16(v.Value), Overflow: v.OV, Error: v.ER})
			}
			a := iec103.New(iec103.CauseCyclic, st.ca, iec103.FunctionType(m.Fun), iec103.InfoNumber(m.Inf),
				iec103.Measurands{Set2: m.Type == 9, Values: vals})
			if s.Send(a, server.Class2) == nil {
				i++
			}
		}
		select {
		case <-stop:
			return
		case <-tick.C:
		}
	}
}

// watched reports the end of a connection: the first failed read.
type watched struct {
	net.Conn
	once   sync.Once
	closed chan struct{}
}

func (w *watched) Read(b []byte) (int, error) {
	n, err := w.Conn.Read(b)
	if err != nil {
		w.once.Do(func() { close(w.closed) })
	}
	return n, err
}

var _ io.ReadWriter = (*watched)(nil)

// object is the information object of an ASDU document (contract 2.2,
// "IEC 103 ASDU documents").
type object struct {
	Fun      int     `json:"fun"`
	Inf      int     `json:"inf"`
	DPI      *int    `json:"dpi"`
	SIN      *int    `json:"sin"`
	SCN      *int    `json:"scn"`
	Col      *int    `json:"col"`
	ASCII    *string `json:"ascii"`
	Software *string `json:"software"`
	Values   []value `json:"values"`
	Data     string  `json:"data"`
}

// result is the client result document of the contract.
type result struct {
	Operation string `json:"operation"`
	OK        bool   `json:"ok"`
	Error     *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
	Connected     bool `json:"connected"`
	Confirmations []struct {
		Cot      int  `json:"cot"`
		Negative bool `json:"negative"`
	} `json:"confirmations"`
	Terminated   bool            `json:"terminated"`
	Generic      *genericDoc     `json:"generic"`
	Disturbances []faultDoc      `json:"disturbances"`
	Disturbance  *disturbanceDoc `json:"disturbance"`
	ASDUs        []struct {
		Type    string   `json:"type"`
		TypeID  int      `json:"typeId"`
		Cot     int      `json:"cot"`
		Objects []object `json:"objects"`
	} `json:"asdus"`
}

// summary renders a result as tests/iec103/cases.tsv states it: SUMMARY_103
// of the harness's scripts/lib.sh, which leaves out the identification
// (type 5) and the cyclic measurands (cause 2), and for the generic
// services and the transmission of disturbance data every ASDU (contract
// 2.3: their content is in generic, disturbances and disturbance).
func (r result) summary() string {
	code := "-"
	if r.Error != nil && r.Error.Code != "" {
		code = r.Error.Code
	}
	var conf, asdus []string
	for _, c := range r.Confirmations {
		s := strconv.Itoa(c.Cot)
		if c.Negative {
			s += "n"
		}
		conf = append(conf, s)
	}
	svc := strings.HasPrefix(r.Operation, "generic-") || strings.HasPrefix(r.Operation, "disturbance-")
	for _, a := range r.ASDUs {
		if a.TypeID == 5 || a.Cot == 2 || svc {
			continue
		}
		typ := a.Type
		if typ == "" {
			typ = strconv.Itoa(a.TypeID)
		}
		asdus = append(asdus, fmt.Sprintf("%s/%d", typ, a.Cot))
	}
	return fmt.Sprintf("ok=%t error=%s conf=%s term=%t asdus=%s", r.OK, code, strings.Join(conf, ","), r.Terminated, strings.Join(asdus, ","))
}

// runClient runs one client operation of a reference image against port
// on this host (host.docker.internal in the container) and returns its
// result and exit code.
func runClient(ctx context.Context, a adapter, port int, op []string) (result, int, error) {
	args := []string{"run", "--rm", "--add-host=host.docker.internal:host-gateway", a.image, "client"}
	args = append(args, op...)
	args = append(args, "--host", "host.docker.internal", "--port", strconv.Itoa(port), "--link-mode", "unbalanced")
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

// startServer runs a reference server and returns its host port.
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

// knownBug is an entry of the harness's known-bugs.tsv.
type knownBug struct {
	adapter, version string
	match            *regexp.Regexp
	what, id, url    string
}

// clientBugs are the entries of known-bugs.tsv that describe the client of
// the adapter; the others describe its server. The harness applies an
// entry to every pairing with the adapter; here a server bug is no excuse
// for a reference client against this module's server, nor a client bug
// for this module's client against a reference server.
var clientBugs = map[string]bool{
	"ariec103-clock-sync-cp32":        true,
	"ariec103-command-no-rii":         true,
	"open103-command-typical-fun":     true,
	"vitusdong-generic-command-inf":   true,
	"vitusdong-sof-bits":              true,
	"vitusdong-disturbance-procedure": true,
	"open103-asdu30-nfe":              true,
}

// knownBugs reads the harness's known-bugs.tsv and known-bugs-consumer.tsv
// (contract 2.3: the bugs seen only against a consumer's own station or
// control system, which no harness pairing shows).
func knownBugs(t *testing.T) []knownBug {
	t.Helper()
	var bugs []knownBug
	for _, name := range []string{"testdata/known-bugs.tsv", "testdata/known-bugs-consumer.tsv"} {
		bugs = append(bugs, readBugs(t, name)...)
	}
	return bugs
}

func readBugs(t *testing.T, name string) []knownBug {
	t.Helper()
	f, err := os.Open(name)
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
			t.Fatalf("%s: %d columns in %q", name, len(col), line)
		}
		re, err := regexp.Compile(col[2])
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		bugs = append(bugs, knownBug{adapter: col[0], version: col[1], match: re, what: col[3], id: col[4], url: col[5]})
	}
	return bugs
}

// knownBugOf returns the entry of known-bugs.tsv for the reference a in
// the given role (client true) that explains a failure of the operation
// op (the operation and its flags, as the harness matches them).
func knownBugOf(t *testing.T, a adapter, client bool, op string) (knownBug, bool) {
	t.Helper()
	for _, b := range knownBugs(t) {
		if b.adapter == a.name && b.version == a.caps.Upstream.Version && clientBugs[b.id] == client && b.match.MatchString(op) {
			return b, true
		}
	}
	return knownBug{}, false
}

// knownDifference ends a test whose failure a known bug of the reference
// explains: skipped with the bug id, never counted as a pass, whatever
// IEC103_REQUIRE_INTEROP says.
func knownDifference(t *testing.T, b knownBug, got string) {
	t.Helper()
	t.Skipf("known difference %s (%s %s): %s; got %s; %s", b.id, b.adapter, b.version, b.what, got, b.url)
}
