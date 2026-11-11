// SPDX-License-Identifier: MIT

// Package capture reads the recorded IEC 60870-5-102 frames kept in
// testdata/captures for the coming IEC 102 work: the frame log of the
// innoWatt icra-tarifes-optic tool and the frames in the gisce/iec870ree
// tests. There is no IEC 102 ASDU codec yet (docs/ROADMAP.md); the tests
// check the FT 1.2 framing, which IEC 102 shares (with a 2-octet link
// address in these sources).
//
// It is test infrastructure and not part of the public API.
package capture

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Frame is one frame of a log or test file, with the line it starts on.
type Frame struct {
	Line int
	Raw  []byte
}

// innoWatt's log prints every message as
//
//	<missatge>
//	  19 bytes: 68 d d 68 8 1 0 b7 ... ca 16
//
// with each octet in hexadecimal without leading zeros.
var logLine = regexp.MustCompile(`^\s*(\d+) bytes: ((?:[0-9a-f]{1,2} )*[0-9a-f]{1,2})\s*$`)

// Log returns the frames of an innoWatt log: the octets on the line after
// each <missatge>. It fails when the octet count disagrees with the line.
func Log(b []byte) ([]Frame, error) {
	var out []Frame
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 1<<16), 1<<20)
	message := false
	for n := 1; sc.Scan(); n++ {
		line := sc.Text()
		if strings.TrimSpace(line) == "<missatge>" {
			message = true
			continue
		}
		if !message {
			continue
		}
		message = false
		m := logLine.FindStringSubmatch(line)
		if m == nil {
			return nil, fmt.Errorf("line %d: no octets after <missatge>", n)
		}
		var raw []byte
		for _, f := range strings.Fields(m[2]) {
			v, err := strconv.ParseUint(f, 16, 8)
			if err != nil {
				return nil, fmt.Errorf("line %d: %w", n, err)
			}
			raw = append(raw, byte(v))
		}
		if want, _ := strconv.Atoi(m[1]); want != len(raw) {
			return nil, fmt.Errorf("line %d: %d octets, the line says %d", n, len(raw), want)
		}
		out = append(out, Frame{Line: n, Raw: raw})
	}
	return out, sc.Err()
}

// fromhex matches bytearray.fromhex( followed by string literals joined by
// + or white space, up to the closing parenthesis.
var fromhex = regexp.MustCompile(`fromhex\(((?:\s*\+?\s*"[0-9A-Fa-f ]*")+)\s*\)`)

var literal = regexp.MustCompile(`"([0-9A-Fa-f ]*)"`)

// Python returns the octet strings of the bytearray.fromhex calls of a
// Python test file whose first octet starts an FT 1.2 frame (10h, 68h or
// E5h); the other calls hold ASDU elements.
func Python(b []byte) ([]Frame, error) {
	var out []Frame
	for _, loc := range fromhex.FindAllSubmatchIndex(b, -1) {
		var s strings.Builder
		for _, l := range literal.FindAllSubmatch(b[loc[2]:loc[3]], -1) {
			s.Write(l[1])
		}
		raw, err := hex.DecodeString(strings.ReplaceAll(s.String(), " ", ""))
		if err != nil {
			return nil, err
		}
		if len(raw) == 0 || raw[0] != 0x10 && raw[0] != 0x68 && raw[0] != 0xE5 {
			continue
		}
		out = append(out, Frame{Line: 1 + bytes.Count(b[:loc[0]], []byte{'\n'}), Raw: raw})
	}
	return out, nil
}
