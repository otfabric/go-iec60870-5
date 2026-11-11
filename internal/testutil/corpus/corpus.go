// SPDX-License-Identifier: MIT

// Package corpus holds what the capture suites of the serial companion
// standards share: the manifest of a testdata/captures directory, its
// check against the committed files, and the tshark oracle files next to
// the captures. It is test infrastructure (TESTING.md) and is not part of
// the public API.
package corpus

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/otfabric/go-iec60870-5/internal/pcapio"
)

// GenerateEnv is the environment variable that makes [CheckManifest] write
// the manifest instead of comparing it (make captures-manifest).
const GenerateEnv = "CAPTURE_GENERATE"

// OracleEnv is the environment variable that makes [GenerateOracle] run
// tshark (make captures-oracle).
const OracleEnv = "CAPTURE_ORACLE"

// Levels are the evidence levels of the OT Fabric capture convention.
var Levels = map[string]string{
	"A": "verified physical-device traffic",
	"B": "credibly documented physical-device traffic",
	"C": "independent recorded traffic, hardware origin unknown",
	"D": "simulated or software testbed",
	"E": "standards-derived examples or generated fixtures",
}

// Source is where a set of files comes from.
type Source struct {
	ID       string `json:"id"`
	URL      string `json:"url"`
	Revision string `json:"revision"`
	Terms    string `json:"terms"`
	Notes    string `json:"notes"`
}

// File is one committed file.
type File struct {
	Path         string   `json:"path"`
	Source       string   `json:"source"`
	OriginalName string   `json:"original_name"`
	URL          string   `json:"url"`
	Size         int      `json:"size"`
	SHA256       string   `json:"sha256"`
	Format       string   `json:"format"`
	Recorded     string   `json:"recorded,omitempty"`
	Evidence     string   `json:"evidence"`
	Basis        string   `json:"evidence_basis"`
	Systems      []string `json:"systems"`
	Operations   string   `json:"operations"`
	Privacy      string   `json:"privacy"`
	Missing      string   `json:"missing_for_verification"`
	// Params are the system parameters the file is decoded with.
	Params any `json:"params,omitempty"`
	// Counts are what go-iec60870-5 derives from the file.
	Counts any `json:"counts,omitempty"`
	// Undecodable lists, with the reason, the octets that are not a frame.
	Undecodable []string `json:"undecodable,omitempty"`
}

// Manifest describes a testdata/captures directory.
type Manifest struct {
	Description string            `json:"description"`
	Levels      map[string]string `json:"evidence_levels"`
	Sources     []Source          `json:"sources"`
	Files       []File            `json:"files"`
}

// Fill sets the size, digest and recording span of f, a file in dir.
// Format is set from the capture when it is empty.
func Fill(t testing.TB, dir string, f *File) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(f.Path)))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	f.Size, f.SHA256 = len(b), hex.EncodeToString(sum[:])
	if !strings.Contains(f.Path, ".pcap") {
		return
	}
	ps, err := pcapio.Packets(b)
	if err != nil {
		t.Fatalf("%s: %v", f.Path, err)
	}
	if len(ps) > 0 {
		first, last := pcapio.Span(ps)
		f.Recorded = first.Format("2006-01-02 15:04:05Z07:00") + " .. " + last.Format("2006-01-02 15:04:05Z07:00")
	}
}

// CheckManifest compares m, as JSON, with dir/manifest.json, or writes it
// there when [GenerateEnv] is set. It also checks dir/SHA256SUMS against
// the files of m.
func CheckManifest(t *testing.T, dir string, m Manifest) {
	t.Helper()
	sort.Slice(m.Files, func(i, j int) bool { return m.Files[i].Path < m.Files[j].Path })
	got, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	path := filepath.Join(dir, "manifest.json")
	var sums strings.Builder
	for _, f := range m.Files {
		if !strings.HasPrefix(f.Path, "../") {
			fmt.Fprintf(&sums, "%s  %s\n", f.SHA256, f.Path)
		}
	}
	if os.Getenv(GenerateEnv) != "" {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte(sums.String()), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s is not what the captures and the code give: run make captures-manifest and review the difference", path)
	}
	have, err := os.ReadFile(filepath.Join(dir, "SHA256SUMS"))
	if err != nil {
		t.Fatal(err)
	}
	if string(have) != sums.String() {
		t.Errorf("%s/SHA256SUMS does not list the files of the manifest", dir)
	}
}

// Committed returns the files below dir with one of the extensions, as
// paths relative to dir with forward slashes.
func Committed(t testing.TB, dir string, exts ...string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == "cache" {
			return filepath.SkipDir
		}
		for _, e := range exts {
			if !d.IsDir() && strings.HasSuffix(p, e) {
				rel, _ := filepath.Rel(dir, p)
				out = append(out, filepath.ToSlash(rel))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(out)
	return out
}

// OracleFile is the oracle of a capture: <capture without extension>.tshark.tsv.gz.
func OracleFile(capture string) string {
	c := strings.TrimSuffix(capture, ".gz")
	return strings.TrimSuffix(c, filepath.Ext(c)) + ".tshark.tsv.gz"
}

// GenerateOracle runs tshark over capture with args (decode-as rules,
// preferences, a display filter) and keeps the fields per frame in
// out, gzip-compressed, with the tshark version in the first line. It
// does nothing unless [OracleEnv] is set.
func GenerateOracle(t *testing.T, capture, out string, args, fields []string) {
	t.Helper()
	if os.Getenv(OracleEnv) == "" {
		return
	}
	version, err := exec.Command("tshark", "--version").Output()
	if err != nil {
		t.Fatal(err)
	}
	first, _, _ := strings.Cut(string(version), "\n")
	a := append([]string{"-r", capture, "-n"}, args...)
	a = append(a, "-T", "fields", "-E", "occurrence=a", "-E", "aggregator=,", "-E", "separator=/t", "-e", "frame.number")
	for _, f := range fields {
		a = append(a, "-e", f)
	}
	res, err := exec.Command("tshark", a...).Output()
	if err != nil {
		t.Fatalf("%s: %v", capture, err)
	}
	var buf bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	_, _ = fmt.Fprintf(zw, "# %s\n# tshark %s\n# frame.number\t%s\n", strings.TrimSpace(first), strings.Join(args, " "), strings.Join(fields, "\t"))
	_, _ = zw.Write(res)
	_ = zw.Close()
	if err := os.WriteFile(out, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Row is one frame of an oracle: the values of each field.
type Row map[string][]string

// ReadOracle reads an oracle file written by [GenerateOracle] with fields.
func ReadOracle(t testing.TB, path string, fields []string) (version string, rows map[int]Row) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	rows = map[int]Row{}
	sc := bufio.NewScanner(zr)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "# ") {
			if version == "" {
				version = line[2:]
			}
			continue
		}
		cols := strings.Split(line, "\t")
		frame, err := strconv.Atoi(cols[0])
		if err != nil || len(cols) != len(fields)+1 {
			t.Fatalf("%s: malformed line %q", path, line)
		}
		row := rows[frame]
		if row == nil {
			row = Row{}
			rows[frame] = row
		}
		for i, f := range fields {
			if cols[i+1] != "" {
				row[f] = append(row[f], strings.Split(cols[i+1], ",")...)
			}
		}
	}
	return version, rows
}

// FuzzSeed writes a seed of the fuzz corpus dir, named after its digest,
// when [GenerateEnv] is set: b, followed by an int argument for each of
// ints (targets such as ft12.FuzzDecode take the address size).
func FuzzSeed(t testing.TB, dir string, b []byte, ints ...int) {
	t.Helper()
	if os.Getenv(GenerateEnv) == "" {
		return
	}
	body := "go test fuzz v1\n[]byte(" + strconv.Quote(string(b)) + ")\n"
	for _, n := range ints {
		body += "int(" + strconv.Itoa(n) + ")\n"
	}
	sum := sha256.Sum256([]byte(body))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "capture-"+hex.EncodeToString(sum[:8])), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
