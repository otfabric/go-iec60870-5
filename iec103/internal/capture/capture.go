// SPDX-License-Identifier: MIT

// Package capture decodes recorded IEC 60870-5-103 traffic for tests: the
// FT 1.2 frames that package serialcap splits out of an RTAC serial-line
// capture or a TCP stream, and the ASDUs in them with the iec103 codec,
// and the hex test vectors of other implementations.
//
// It is test infrastructure for the captures in testdata/captures and is
// not part of the public API.
package capture

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/otfabric/go-iec60870-5/ft12"
	"github.com/otfabric/go-iec60870-5/iec103"
	"github.com/otfabric/go-iec60870-5/internal/pcapio"
	"github.com/otfabric/go-iec60870-5/internal/serialcap"
)

// FT12 are the frame parameters of IEC 60870-5-103: a one-octet link
// address (clause 6.2.3).
var FT12 = ft12.Params{AddrSize: 1}

// Decoded is one frame with its ASDU.
type Decoded struct {
	serialcap.Frame
	ASDU    *iec103.ASDU
	ASDUErr error
	// Reencoded is the frame encoded again from the decoded control
	// field, address and ASDU; nil when the ASDU did not decode.
	Reencoded []byte
}

// Exact reports whether the re-encoding equals the recorded octets.
func (d Decoded) Exact() bool { return d.Reencoded != nil && bytes.Equal(d.Reencoded, d.Raw) }

// Decode decodes the ASDU of every frame of l and encodes it again.
func Decode(l serialcap.Line) []Decoded {
	out := make([]Decoded, 0, len(l.Frames))
	for _, f := range l.Frames {
		d := Decoded{Frame: f}
		if f.Kind != ft12.KindVariable {
			d.Reencoded, _ = f.Encode(FT12)
			out = append(out, d)
			continue
		}
		d.ASDU, d.ASDUErr = iec103.Decode(f.Data)
		if d.ASDUErr == nil {
			body, err := d.ASDU.Encode()
			if err != nil {
				d.ASDUErr = fmt.Errorf("re-encode: %w", err)
			} else {
				g := f.Frame
				g.Data = body
				d.Reencoded, _ = g.Encode(FT12)
			}
		}
		out = append(out, d)
	}
	return out
}

// Hex reads a test vector file: one frame per line as hexadecimal octets,
// lines starting with # are comments. Each frame becomes a run of its own
// (frame number = line number), so that one bad vector does not hide the
// next.
func Hex(b []byte) ([]pcapio.Run, error) {
	var out []pcapio.Run
	sc := bufio.NewScanner(bytes.NewReader(b))
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		raw, err := hex.DecodeString(strings.Join(strings.Fields(line), ""))
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", n, err)
		}
		var r pcapio.Run
		r.Add(n, time.Time{}, raw)
		out = append(out, r)
	}
	return out, sc.Err()
}
