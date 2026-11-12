//go:build interop

// SPDX-License-Identifier: MIT

package interop

import (
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// fields are the field sizes and the link address of a run (contract 2.3 of
// iec60870-5-interop): the link flags --link-address-size, --cot-size,
// --ca-size, --ioa-size and --link-address, which both ends of a line are
// given alike.
type fields struct {
	addrSize, cot, ca, ioa int
	linkAddr               int
}

// defaultFields are the contract's defaults: link address 1 in one octet,
// COT 1, CA 1, IOA 2.
func defaultFields() fields { return fields{addrSize: 1, cot: 1, ca: 1, ioa: 2, linkAddr: 1} }

// linkFlagNames are the flags of an operation that the case's server gets too.
var linkFlagNames = []string{"--link-address", "--link-address-size", "--cot-size", "--ca-size", "--ioa-size"}

// flagValue returns the value of flag in op, or def.
func flagValue(op []string, flag string, def int) int {
	for i := 0; i+1 < len(op); i++ {
		if op[i] == flag {
			if n, err := strconv.Atoi(op[i+1]); err == nil {
				return n
			}
		}
	}
	return def
}

// caseFields returns the field sizes of a case's operation and the link
// flags it carries, joined: the key of the server those flags need ("" for
// the defaults).
func caseFields(op []string) (fields, string) {
	d := defaultFields()
	f := fields{
		addrSize: flagValue(op, "--link-address-size", d.addrSize),
		cot:      flagValue(op, "--cot-size", d.cot),
		ca:       flagValue(op, "--ca-size", d.ca),
		ioa:      flagValue(op, "--ioa-size", d.ioa),
		linkAddr: flagValue(op, "--link-address", d.linkAddr),
	}
	var key []string
	for i := 0; i+1 < len(op); i++ {
		if slices.Contains(linkFlagNames, op[i]) {
			key = append(key, op[i], op[i+1])
		}
	}
	return f, strings.Join(key, " ")
}

// sizesOf is the list a capability document declares, or the contract's
// default alone for an image of contract 2.2, which declares none.
func sizesOf(declared []int, def int) []int {
	if declared == nil {
		return []int{def}
	}
	return declared
}

// takes reports whether a reference image takes the field sizes of f in the
// link mode: it lists every size, and a link address field of 0 octets is
// for balanced mode only.
func (a adapter) takes(f fields, mode string) bool {
	d := defaultFields()
	if f.addrSize == 0 && mode != "balanced" {
		return false
	}
	return slices.Contains(sizesOf(a.caps.LinkAddressSizes, d.addrSize), f.addrSize) &&
		slices.Contains(sizesOf(a.caps.CotSizes, d.cot), f.cot) &&
		slices.Contains(sizesOf(a.caps.CaSizes, d.ca), f.ca) &&
		slices.Contains(sizesOf(a.caps.IoaSizes, d.ioa), f.ioa)
}

// clientCanSend reports whether the reference client can send the case's
// request in the link mode: an unbalanced client without the feature
// unbalancedCommonAddress sends only to the common address equal to the link
// address (connect and monitor send no request).
func (a adapter) clientCanSend(op []string, f fields, mode string) bool {
	if mode != "unbalanced" || len(op) == 0 || op[0] == "connect" || op[0] == "monitor" {
		return true
	}
	if a.caps.Features["unbalancedCommonAddress"] {
		return true
	}
	return flagValue(op, "--common-address", 1) == f.linkAddr
}

// knownBug returns the description of a known bug of the reference's
// upstream version in a role ("client" or "server") that may explain a
// mismatch of the operation in the link mode, or "". The table is the
// harness's testdata/known-bugs.tsv: adapter (or adapter@role), upstream
// version, a regex matched against the operation followed by
// " --link-mode <mode>", description, waiver id, evidence.
func knownBug(t *testing.T, a adapter, role, mode string, op []string) string {
	t.Helper()
	raw, err := os.ReadFile("testdata/known-bugs.tsv")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		col := strings.Split(line, "\t")
		who, only, scoped := strings.Cut(col0(col), "@")
		if len(col) < 4 || who != a.name || scoped && only != role || col[1] != a.caps.Upstream.Version {
			continue
		}
		if re, err := regexp.Compile(col[2]); err == nil && re.MatchString(strings.Join(op, " ")+" --link-mode "+mode) {
			return col[3]
		}
	}
	return ""
}

func col0(col []string) string {
	if len(col) == 0 {
		return ""
	}
	return col[0]
}
