// SPDX-License-Identifier: MIT

package capture

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/otfabric/go-iec60870-5/ft12"
	"github.com/otfabric/go-iec60870-5/internal/testutil/corpus"
	"github.com/otfabric/go-iec60870-5/link"
)

const dir = "../../testdata/captures"

// p are the frame parameters of both sources: a 2-octet link address
// (the Spanish REE profile of IEC 60870-5-102 the open implementations
// follow; we do not hold IEC 60870-5-102).
var p = ft12.Params{AddrSize: 2}

const (
	srcInnoWatt = "innoWatt/icra-tarifes-optic"
	srcGisce    = "gisce/iec870ree"
)

var sources = []corpus.Source{
	{ID: srcInnoWatt, URL: "https://github.com/innoWatt/icra-tarifes-optic/tree/b76638cef85e345aba883776ab5b106554df5c5b", Revision: "b76638cef85e345aba883776ab5b106554df5c5b",
		Terms: "GPL-3.0 (repository licence).",
		Notes: "Python 2 tool reading electricity meters over IEC 60870-5-102 through their optical port from a Raspberry Pi. The README lists the meters tried; \"icra\" is the Actaris SL762B. usr/tests/icra_201605_raw.txt is the tool's log of a reading: per message the octets and its interpretation (Catalan)."},
	{ID: srcGisce, URL: "https://github.com/gisce/iec870ree/tree/993242daa3e998c9d86948881d97835438d8bef3/tests", Revision: "993242daa3e998c9d86948881d97835438d8bef3",
		Terms: "AGPL-3.0 (repository licence).",
		Notes: "Python IEC 60870-5-102 (REE profile) implementation; its unit tests hold link frames and ASDU element octets."},
}

type spec struct {
	corpus.File
	python bool
}

var specs = []spec{
	{File: corpus.File{Path: "innowatt/icra-tarifes-optic_actaris-sl762b_load-profile-201605.txt", Source: srcInnoWatt,
		OriginalName: "usr/tests/icra_201605_raw.txt", URL: "https://github.com/innoWatt/icra-tarifes-optic/blob/b76638cef85e345aba883776ab5b106554df5c5b/usr/tests/icra_201605_raw.txt",
		Format: "text log: \"N bytes:\" and the octets in hexadecimal per message, then the tool's interpretation", Evidence: "B",
		Basis:      "The tool's log of reading the Actaris SL762B meter at ICRA through its optical port (README: meters tried, \"icra\" Actaris SL762B OK). A log, not a capture: it prints the meter's answers only, not the requests, and no time stamps.",
		Systems:    []string{"icra-tarifes-optic (Python 2, Raspberry Pi) as controlling station", "Actaris SL762B electricity meter, link address 1, measurement point 1"},
		Operations: "the meter's answers of one reading: the confirmation of the session start with access key (ASDU 183), confirmations of load profile requests (ASDU 123) and the integrated totals of the load profile (ASDU 11), two ACKs",
		Privacy:    "meter readings of a research institute, no personal data", Missing: "the requests, the time of the session, the meter firmware"}},
	{File: corpus.File{Path: "gisce/synthetic_synthetic_link-frames.py", Source: srcGisce, OriginalName: "tests/test_protocol.py",
		URL: "https://github.com/gisce/iec870ree/blob/993242daa3e998c9d86948881d97835438d8bef3/tests/test_protocol.py", Format: "Python unit test with frames as bytearray.fromhex", Evidence: "E",
		Basis: "Frames written into unit tests; whether they were taken from a meter is not stated.", Systems: []string{"meters at link addresses 34572 and 53653 (test data)"},
		Operations: "request of link status, its answer, reset of the remote link, an ASDU 183 confirmation", Privacy: "none", Missing: "-"}, python: true},
	{File: corpus.File{Path: "gisce/synthetic_synthetic_base-asdu.py", Source: srcGisce, OriginalName: "tests/test_base_asdu.py",
		URL: "https://github.com/gisce/iec870ree/blob/993242daa3e998c9d86948881d97835438d8bef3/tests/test_base_asdu.py", Format: "Python unit test with frames as bytearray.fromhex", Evidence: "E",
		Basis: "Frames written into unit tests.", Systems: []string{"meter at link address 34572 (test data)"},
		Operations: "fixed and variable frames of the link layer", Privacy: "none", Missing: "-"}, python: true},
	{File: corpus.File{Path: "gisce/synthetic_synthetic_app-asdu.py", Source: srcGisce, OriginalName: "tests/test_app_asdu.py",
		URL: "https://github.com/gisce/iec870ree/blob/993242daa3e998c9d86948881d97835438d8bef3/tests/test_app_asdu.py", Format: "Python unit test with ASDU element octets as bytearray.fromhex", Evidence: "E",
		Basis: "Element octets written into unit tests, kept for the coming ASDU codec (no frames).", Systems: []string{"-"},
		Operations: "access key, time tags of the REE profile, integrated totals and their quality", Privacy: "none", Missing: "-"}, python: true},
}

func frames(t testing.TB, s spec) []Frame {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(s.Path)))
	if err != nil {
		t.Fatal(err)
	}
	var fs []Frame
	if s.python {
		fs, err = Python(b)
	} else {
		fs, err = Log(b)
	}
	if err != nil {
		t.Fatalf("%s: %v", s.Path, err)
	}
	return fs
}

type counts struct {
	Frames    int      `json:"frames"`
	Fixed     int      `json:"fixed_frames"`
	Variable  int      `json:"variable_frames"`
	Primary   int      `json:"primary"`
	Secondary int      `json:"secondary"`
	Addresses []int    `json:"link_addresses"`
	ASDUTypes []int    `json:"first_user_data_octets"`
	Errors    []string `json:"errors,omitempty"`
}

func derive(t testing.TB, s spec) counts {
	t.Helper()
	var c counts
	for _, f := range frames(t, s) {
		fr, n, err := ft12.Decode(f.Raw, p)
		if err == nil && n != len(f.Raw) {
			err = fmt.Errorf("%d octets after the frame", len(f.Raw)-n)
		}
		if err != nil {
			c.Errors = append(c.Errors, fmt.Sprintf("line %d: %v", f.Line, err))
			continue
		}
		c.Frames++
		if fr.Kind == ft12.KindVariable {
			c.Variable++
			if len(fr.Data) > 0 && !slices.Contains(c.ASDUTypes, int(fr.Data[0])) {
				c.ASDUTypes = append(c.ASDUTypes, int(fr.Data[0]))
			}
		} else {
			c.Fixed++
		}
		if link.Control(fr.Control).PRM() {
			c.Primary++
		} else {
			c.Secondary++
		}
		if !slices.Contains(c.Addresses, int(fr.Addr)) {
			c.Addresses = append(c.Addresses, int(fr.Addr))
		}
	}
	slices.Sort(c.Addresses)
	slices.Sort(c.ASDUTypes)
	return c
}

// TestFrames decodes every frame of the IEC 102 sources as FT 1.2 with a
// 2-octet link address: start and stop characters, both length octets and
// the checksum, and that the frame encodes back to the same octets.
func TestFrames(t *testing.T) {
	for _, s := range specs {
		t.Run(s.Path, func(t *testing.T) {
			fs := frames(t, s)
			for _, f := range fs {
				fr, n, err := ft12.Decode(f.Raw, p)
				if err != nil || n != len(f.Raw) {
					t.Errorf("line %d: %v (% x)", f.Line, err, f.Raw)
					continue
				}
				if enc, err := fr.Encode(p); err != nil || !bytes.Equal(enc, f.Raw) {
					t.Errorf("line %d: re-encoded % x, recorded % x", f.Line, enc, f.Raw)
				}
			}
			c := derive(t, s)
			if strings.HasSuffix(s.Path, "app-asdu.py") {
				if len(fs) != 0 {
					t.Errorf("%d frames in the element vectors", len(fs))
				}
				return
			}
			if c.Frames == 0 {
				t.Fatal("no frames")
			}
			// The log prints the meter's answers only.
			if !s.python && (c.Primary != 0 || !slices.Equal(c.Addresses, []int{1})) {
				t.Errorf("%+v", c)
			}
		})
	}
}

func TestManifest(t *testing.T) {
	m := corpus.Manifest{
		Description: "IEC 60870-5-102 frames kept for the coming IEC 102 work (testdata/captures/README.md). Files are byte-identical to their sources.",
		Levels:      corpus.Levels, Sources: sources,
	}
	var listed []string
	for _, s := range specs {
		f := s.File
		corpus.Fill(t, dir, &f)
		f.Params = map[string]int{"link_address_octets": 2}
		f.Counts = derive(t, s)
		m.Files = append(m.Files, f)
		listed = append(listed, s.Path)
	}
	corpus.CheckManifest(t, dir, m)
	slices.Sort(listed)
	if got := corpus.Committed(t, dir, ".txt", ".py"); !slices.Equal(got, listed) {
		t.Errorf("committed %v, in specs %v", got, listed)
	}
}
