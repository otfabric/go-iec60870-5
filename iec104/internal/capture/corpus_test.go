// SPDX-License-Identifier: MIT

package capture

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/otfabric/go-iec60870-5/asdu"
)

// TestCorpusReencode decodes every APDU of every committed capture and
// encodes it again. The recordings of implementations (evidence C and D)
// come back byte for byte. The generated capture (E) sets reserved bits
// (quality descriptors, SCO, protection events) and days of week that do
// not match the date: the encoder writes reserved bits as zero and the
// day of the week of the date, and nothing else differs. One recording
// writes the year of CP56Time2a as years since 1900 (109 for 2009), out of
// the range 0..99 of IEC 60870-5-4: cp.ParseCP56 reads an impossible time
// as the invalid zero time, which the encoder writes; those APDUs differ
// in the time octets only.
func TestCorpusReencode(t *testing.T) {
	inexact := map[string]int{
		"wireshark/synthetic_synthetic_iec104-coverage.pcap.gz":          132,
		"wireshark-issues/unknown_unknown_diverse-signals-loopback.pcap": 10,
	}
	for _, f := range committed(t) {
		name := strings.TrimPrefix(filepath.ToSlash(f), "../../testdata/captures/")
		convs, err := ReadFile(f)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		n, decoded := 0, 0
		for _, c := range convs {
			for _, d := range Analyze(c, asdu.IEC104).Decoded {
				if d.Reencoded == nil {
					continue
				}
				decoded++
				if d.Exact() {
					continue
				}
				n++
				if len(d.Reencoded) != len(d.Data) {
					t.Errorf("%s frame %d: re-encoded %d octets, recorded %d", name, d.APDU.Frame, len(d.Reencoded), len(d.Data))
					continue
				}
				if impossibleTime(d) {
					continue
				}
				for i := range d.Data {
					rec, enc := d.Data[i], d.Reencoded[i]
					if rec == enc || enc&^rec == 0 || (rec^enc)&^0xE0 == 0 {
						continue // a reserved bit cleared, or the day of week
					}
					t.Errorf("%s frame %d octet %d: re-encoded 0x%02X, recorded 0x%02X", name, d.APDU.Frame, i, enc, rec)
				}
			}
		}
		if n != inexact[name] {
			t.Errorf("%s: %d of %d APDUs not re-encoded exactly, want %d", name, n, decoded, inexact[name])
		}
	}
}

// impossibleTime reports whether the CP56Time2a of an APDU carries a year
// beyond 99 (octet 7 of the time), which the decoder reads as the invalid
// zero time.
func impossibleTime(d Decoded) bool {
	if d.ASDU == nil || !d.ASDU.Type.HasTimeTag() || len(d.Data) < 7 {
		return false
	}
	return d.Data[len(d.Data)-1]&0x7F > 99
}
