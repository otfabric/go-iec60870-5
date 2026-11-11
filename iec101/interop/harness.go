//go:build interop

// SPDX-License-Identifier: MIT

package interop

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/otfabric/go-iec60870-5/asdu"
	"github.com/otfabric/go-iec60870-5/iec101"
	"github.com/otfabric/go-iec60870-5/iec101/server"
)

// The references: otfabric/iec60870-5-interop v0.2.1 (contract 2.1),
// lib60870-C v2.4.1 and cB-Sigma/iec60870 v0.3.0, pinned by multi-arch
// index digest (the release's manifest.json).
var stacks = []struct{ name, env, image string }{
	{"lib60870", "IEC101_INTEROP_LIB60870_IMAGE", "ghcr.io/otfabric/iec60870-5-interop-iec101-lib60870@sha256:dfad176d87432463526d262c4b60c831ff04828eb3ae8e257c05c5745ef3e5d6"},
	{"cbsigma", "IEC101_INTEROP_CBSIGMA_IMAGE", "ghcr.io/otfabric/iec60870-5-interop-iec101-cbsigma@sha256:f381e0c37a51f0e5bc5149d93ded82e96f7e4635b3ad8aafa720b6d7e1e62236"},
}

var modes = []string{"unbalanced", "balanced"}

type adapter struct {
	name, image string
	caps        capabilities
}

type capabilities struct {
	Adapter   string          `json:"adapter"`
	Standard  string          `json:"standard"`
	LinkModes []string        `json:"linkModes"`
	Features  map[string]bool `json:"features"`
	Upstream  struct {
		Name, Version, Revision string
		IndependentEngine       bool `json:"independentEngine"`
	} `json:"upstream"`
}

func require() bool { return os.Getenv("IEC101_REQUIRE_INTEROP") == "1" }

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
		only := os.Getenv("IEC101_INTEROP_ADAPTERS")
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
			if a.caps.Standard != "iec60870-5-101" {
				adapterErr = fmt.Errorf("%s is %q, not an IEC 101 image", img, a.caps.Standard)
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

func linkFlags(mode string) []string { return []string{"--link-mode", mode} }

func params(mode string) iec101.Params {
	p := iec101.DefaultParams() // the contract's defaults: address 1 octet, COT 1, CA 1, IOA 2
	if mode == "balanced" {
		p.Mode = iec101.ModeBalanced
	}
	p.Link.Timeout = 500 * time.Millisecond
	return p
}

// fixture is the baseline fixture as the reference prints it.
type fixture struct {
	Station struct {
		CommonAddress int `json:"commonAddress"`
	} `json:"station"`
	Points []struct {
		IOA      int             `json:"ioa"`
		Type     string          `json:"type"`
		Value    json.RawMessage `json:"value"`
		Quality  []string        `json:"quality"`
		Sequence int             `json:"sequence"`
	} `json:"points"`
	Commands []struct {
		IOA            int    `json:"ioa"`
		Type           string `json:"type"`
		Target         int    `json:"target"`
		ReportCause    int    `json:"reportCause"`
		SelectRequired bool   `json:"selectRequired"`
	} `json:"commands"`
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
	return &f
}

var qualityNames = map[string]asdu.Quality{
	"OV": asdu.QualityOverflow, "BL": asdu.QualityBlocked, "SB": asdu.QualitySubstituted,
	"NT": asdu.QualityNotTopical, "IV": asdu.QualityInvalid,
}

func quality(names []string) asdu.Quality {
	var q asdu.Quality
	for _, n := range names {
		q |= qualityNames[n]
	}
	return q
}

// point returns the information object of a fixture point.
func pointObject(typ string, ioa asdu.IOA, raw json.RawMessage, q asdu.Quality, seq int) (asdu.InformationObject, error) {
	var b bool
	var n float64
	if typ == "M_SP_NA_1" {
		if err := json.Unmarshal(raw, &b); err != nil {
			return nil, err
		}
	} else if err := json.Unmarshal(raw, &n); err != nil {
		return nil, err
	}
	switch typ {
	case "M_SP_NA_1":
		return asdu.SinglePoint{IOA: ioa, Value: b, Quality: q}, nil
	case "M_DP_NA_1":
		return asdu.DoublePoint{IOA: ioa, Value: asdu.DoubleState(n), Quality: q}, nil
	case "M_ST_NA_1":
		return asdu.StepPosition{IOA: ioa, Value: int8(n), Quality: q}, nil
	case "M_BO_NA_1":
		return asdu.Bitstring32{IOA: ioa, Value: uint32(n), Quality: q}, nil
	case "M_ME_NA_1":
		return asdu.MeasuredNormalized{IOA: ioa, Value: asdu.Normalized(int16(n)), Quality: q}, nil
	case "M_ME_NB_1":
		return asdu.MeasuredScaled{IOA: ioa, Value: int16(n), Quality: q}, nil
	case "M_ME_NC_1":
		return asdu.MeasuredFloat{IOA: ioa, Value: float32(n), Quality: q}, nil
	case "M_IT_NA_1":
		return asdu.IntegratedTotal{IOA: ioa, Value: int32(n), Sequence: uint8(seq)}, nil
	}
	return nil, fmt.Errorf("fixture point type %s not modelled", typ)
}

// valueOf returns the value of an object as the fixture writes it.
func valueOf(o asdu.InformationObject) any {
	switch v := o.(type) {
	case asdu.SinglePoint:
		return v.Value
	case asdu.DoublePoint:
		return float64(v.Value)
	case asdu.StepPosition:
		return float64(v.Value)
	case asdu.Bitstring32:
		return float64(v.Value)
	case asdu.MeasuredNormalized:
		return float64(v.Value)
	case asdu.MeasuredScaled:
		return float64(v.Value)
	case asdu.MeasuredFloat:
		return float64(v.Value)
	case asdu.IntegratedTotal:
		return float64(v.Value)
	}
	return nil
}

// station serves the fixture with this module's server, following
// iec60870-5-interop docs/FIXTURES.md for the answers. It is the system
// under test in the reference-client direction; its state lives across
// connections, as the contract's servers do.
type station struct {
	f  *fixture
	ca asdu.CommonAddr

	mu       sync.Mutex
	points   map[asdu.IOA]asdu.InformationObject
	order    []asdu.IOA
	selected map[asdu.IOA]bool
}

func newStation(f *fixture) (*station, error) {
	st := &station{f: f, ca: asdu.CommonAddr(f.Station.CommonAddress), points: map[asdu.IOA]asdu.InformationObject{}, selected: map[asdu.IOA]bool{}}
	for _, p := range f.Points {
		o, err := pointObject(p.Type, asdu.IOA(p.IOA), p.Value, quality(p.Quality), p.Sequence)
		if err != nil {
			return nil, err
		}
		st.points[asdu.IOA(p.IOA)] = o
		st.order = append(st.order, asdu.IOA(p.IOA))
	}
	slices.Sort(st.order)
	return st, nil
}

func (st *station) HandleASDU(s *server.Server, a *asdu.ASDU) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if a.CommonAddr != st.ca && !s.Params().ASDU.IsBroadcast(a.CommonAddr) {
		_ = s.Reject(a, asdu.CauseUnknownCommonAddr)
		return
	}
	reply := func(r *asdu.ASDU) {
		r.CommonAddr = st.ca
		_ = s.Send(r)
	}
	switch o := a.First().(type) {
	case asdu.Interrogation:
		if o.Qualifier != asdu.QOIStation {
			reply(a.Reply(asdu.CauseActivationCon, true))
			return
		}
		reply(a.Reply(asdu.CauseActivationCon, false))
		var batch []asdu.InformationObject
		flush := func() {
			if len(batch) > 0 {
				_ = s.Send(asdu.New(asdu.CauseInterrogatedStation, st.ca, batch...))
				batch = nil
			}
		}
		for _, ioa := range st.order {
			p := st.points[ioa]
			if _, ok := p.(asdu.IntegratedTotal); ok {
				continue
			}
			if len(batch) > 0 && asdu.TypeOf(batch[0]) != asdu.TypeOf(p) {
				flush()
			}
			batch = append(batch, p)
		}
		flush()
		reply(a.Reply(asdu.CauseActivationTerm, false))
	case asdu.CounterInterrogation:
		if o.Request != asdu.CounterGeneral || o.Freeze != asdu.FreezeRead {
			reply(a.Reply(asdu.CauseActivationCon, true))
			return
		}
		reply(a.Reply(asdu.CauseActivationCon, false))
		for _, ioa := range st.order {
			if p, ok := st.points[ioa].(asdu.IntegratedTotal); ok {
				_ = s.Send(asdu.New(asdu.CauseCounterGeneral, st.ca, p))
			}
		}
		reply(a.Reply(asdu.CauseActivationTerm, false))
	case asdu.Read:
		p, ok := st.points[o.IOA]
		if !ok {
			_ = s.Reject(a, asdu.CauseUnknownIOA)
			return
		}
		_ = s.Send(asdu.New(asdu.CauseRequest, st.ca, p))
	case asdu.ClockSync:
		reply(a.Reply(asdu.CauseActivationCon, false))
	case asdu.DelayAcquisition:
		if a.Cause == asdu.CauseActivation {
			reply(a.Reply(asdu.CauseActivationCon, false))
		}
	case asdu.TestCommand:
		if a.Type != asdu.C_TS_NA_1 {
			_ = s.Reject(a, asdu.CauseUnknownType)
			return
		}
		reply(a.Reply(asdu.CauseActivationCon, false))
	default:
		if a.Type.IsProcessCommand() {
			st.command(s, a, reply)
			return
		}
		_ = s.Reject(a, asdu.CauseUnknownType)
	}
}

func (st *station) command(s *server.Server, a *asdu.ASDU, reply func(*asdu.ASDU)) {
	obj := a.First()
	ioa := obj.Address()
	var cmd *struct {
		IOA            int    `json:"ioa"`
		Type           string `json:"type"`
		Target         int    `json:"target"`
		ReportCause    int    `json:"reportCause"`
		SelectRequired bool   `json:"selectRequired"`
	}
	for i := range st.f.Commands {
		if st.f.Commands[i].IOA == int(ioa) {
			cmd = &st.f.Commands[i]
		}
	}
	if cmd == nil || cmd.Type != a.Type.String() {
		if cmd == nil && a.Type >= asdu.C_SC_TA_1 && a.Type <= asdu.C_BO_TA_1 {
			_ = s.Reject(a, asdu.CauseUnknownType) // CP56Time2a commands do not exist in IEC 101
			return
		}
		_ = s.Reject(a, asdu.CauseUnknownIOA)
		return
	}
	if a.Cause == asdu.CauseDeactivation {
		delete(st.selected, ioa)
		reply(a.Reply(asdu.CauseDeactivationCon, false))
		return
	}
	target := asdu.IOA(cmd.Target)
	var sel bool
	var next asdu.InformationObject
	switch c := obj.(type) {
	case asdu.SingleCommand:
		sel = c.Select
		p := st.points[target].(asdu.SinglePoint)
		p.Value = c.Value
		next = p
	case asdu.DoubleCommand:
		if c.Value == 0 || c.Value == 3 {
			reply(a.Reply(asdu.CauseActivationCon, true))
			return
		}
		sel = c.Select
		p := st.points[target].(asdu.DoublePoint)
		p.Value = c.Value
		next = p
	case asdu.StepCommand:
		if c.Value == 0 || c.Value == 3 {
			reply(a.Reply(asdu.CauseActivationCon, true))
			return
		}
		sel = c.Select
		p := st.points[target].(asdu.StepPosition)
		if !sel && ((c.Value == asdu.StepLower && p.Value == -64) || (c.Value == asdu.StepHigher && p.Value == 63)) {
			delete(st.selected, ioa)
			reply(a.Reply(asdu.CauseActivationCon, true))
			return
		}
		if c.Value == asdu.StepLower {
			p.Value--
		} else {
			p.Value++
		}
		next = p
	case asdu.SetpointNormalized:
		sel = c.Select
		p := st.points[target].(asdu.MeasuredNormalized)
		p.Value = c.Value
		next = p
	case asdu.SetpointScaled:
		sel = c.Select
		p := st.points[target].(asdu.MeasuredScaled)
		p.Value = c.Value
		next = p
	case asdu.SetpointFloat:
		sel = c.Select
		p := st.points[target].(asdu.MeasuredFloat)
		p.Value = c.Value
		next = p
	default:
		_ = s.Reject(a, asdu.CauseUnknownType)
		return
	}
	if sel {
		st.selected[ioa] = true
		reply(a.Reply(asdu.CauseActivationCon, false))
		return
	}
	if cmd.SelectRequired && !st.selected[ioa] {
		reply(a.Reply(asdu.CauseActivationCon, true))
		return
	}
	delete(st.selected, ioa)
	st.points[target] = next
	reply(a.Reply(asdu.CauseActivationCon, false))
	_ = s.Send(asdu.New(asdu.Cause(cmd.ReportCause), st.ca, withTime(next)))
	reply(a.Reply(asdu.CauseActivationTerm, false))
}

// withTime returns the CP56Time2a variant of a point for the report.
func withTime(o asdu.InformationObject) asdu.InformationObject {
	now := asdu.Now()
	switch p := o.(type) {
	case asdu.SinglePoint:
		p.Time = now
		return p
	case asdu.DoublePoint:
		p.Time = now
		return p
	case asdu.StepPosition:
		p.Time = now
		return p
	case asdu.MeasuredNormalized:
		p.Time = now
		return p
	case asdu.MeasuredScaled:
		p.Time = now
		return p
	case asdu.MeasuredFloat:
		p.Time = now
		return p
	}
	return o
}

// serve accepts connections on ln one at a time, as a line has one
// controlling station, and runs a fresh server on each.
func serve(t *testing.T, ln net.Listener, p iec101.Params, st *station) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			s, err := server.New(conn, server.Config{Params: p, LinkAddr: 1, Remote: 1, CommonAddr: st.ca, Handler: st, NoEndOfInit: true})
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

// result is the client result document of the contract.
type result struct {
	OK    bool `json:"ok"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
	Connected     bool `json:"connected"`
	Confirmations []struct {
		Cot      int  `json:"cot"`
		Negative bool `json:"negative"`
	} `json:"confirmations"`
	Terminated bool `json:"terminated"`
	ASDUs      []struct {
		Type    string `json:"type"`
		TypeID  int    `json:"typeId"`
		Cot     int    `json:"cot"`
		Objects []struct {
			IOA   int `json:"ioa"`
			Value any `json:"value"`
		} `json:"objects"`
	} `json:"asdus"`
}

// summary renders a result as tests/<standard>/cases.tsv states it
// (scripts/lib.sh SUMMARY of the harness).
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
	for _, a := range r.ASDUs {
		typ := a.Type
		if typ == "" {
			typ = strconv.Itoa(a.TypeID)
		}
		asdus = append(asdus, fmt.Sprintf("%s/%d", typ, a.Cot))
	}
	return fmt.Sprintf("ok=%t error=%s conf=%s term=%t asdus=%s", r.OK, code, strings.Join(conf, ","), r.Terminated, strings.Join(asdus, ","))
}

// runClient runs one client operation of a reference image against addr
// (reachable from the container as host.docker.internal) and returns its
// result and exit code.
func runClient(ctx context.Context, a adapter, port int, mode string, op []string) (result, int, error) {
	args := []string{"run", "--rm", "--add-host=host.docker.internal:host-gateway", a.image, "client"}
	args = append(args, op...)
	args = append(args, "--host", "host.docker.internal", "--port", strconv.Itoa(port))
	args = append(args, linkFlags(mode)...)
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
func startServer(t *testing.T, a adapter, mode string) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	args := append([]string{"run", "-d", "-p", "127.0.0.1::2401", a.image, "server"}, linkFlags(mode)...)
	out, err := docker(ctx, args...)
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
