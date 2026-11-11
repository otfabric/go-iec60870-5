// SPDX-License-Identifier: MIT

// Package capture decodes recorded IEC 60870-5-101 traffic for tests: the
// FT 1.2 frames that package serialcap splits out of an RTAC serial-line
// capture or a TCP stream of a serial port server, and the ASDUs in them
// under the field sizes the recording uses.
//
// It is test infrastructure for the captures in testdata/captures and is
// not part of the public API.
package capture

import (
	"bytes"
	"fmt"

	"github.com/otfabric/go-iec60870-5/asdu"
	"github.com/otfabric/go-iec60870-5/ft12"
	"github.com/otfabric/go-iec60870-5/internal/serialcap"
)

// Params are the system parameters of a recording.
type Params struct {
	LinkAddr   int `json:"link_address_octets"`
	Cause      int `json:"cause_octets"`
	CommonAddr int `json:"common_address_octets"`
	IOA        int `json:"ioa_octets"`
}

// FT12 returns the frame parameters.
func (p Params) FT12() ft12.Params { return ft12.Params{AddrSize: p.LinkAddr} }

// ASDU returns the ASDU parameters; the largest ASDU is what one frame
// carries.
func (p Params) ASDU() asdu.Params {
	return asdu.Params{CauseSize: p.Cause, CommonAddrSize: p.CommonAddr, IOASize: p.IOA, MaxSize: p.FT12().MaxData()}
}

// Decoded is one frame of a recording with its ASDU.
type Decoded struct {
	serialcap.Frame
	// ASDU is the decoded user data of a variable length frame.
	ASDU    *asdu.ASDU
	ASDUErr error
	// Reencoded is the frame go-iec60870-5 encodes from the decoded
	// control field, address and ASDU; nil when the ASDU did not decode.
	Reencoded []byte
}

// Exact reports whether the re-encoding equals the recorded octets.
func (d Decoded) Exact() bool { return d.Reencoded != nil && bytes.Equal(d.Reencoded, d.Raw) }

// Decode decodes the ASDU of every frame of l under p and encodes the
// frame again.
func Decode(l serialcap.Line, p Params) []Decoded {
	out := make([]Decoded, 0, len(l.Frames))
	ap, fp := p.ASDU(), p.FT12()
	for _, f := range l.Frames {
		d := Decoded{Frame: f}
		if f.Kind != ft12.KindVariable {
			d.Reencoded, _ = f.Encode(fp)
			out = append(out, d)
			continue
		}
		d.ASDU, d.ASDUErr = asdu.Decode(f.Data, ap)
		if d.ASDUErr == nil {
			body, err := d.ASDU.Encode(ap)
			if err != nil {
				d.ASDUErr = fmt.Errorf("re-encode: %w", err)
			} else {
				g := f.Frame
				g.Data = body
				d.Reencoded, _ = g.Encode(fp)
			}
		}
		out = append(out, d)
	}
	return out
}

// Replay is a committed recording the replay tests play.
type Replay struct {
	Path   string // relative to testdata/captures
	Ports  []uint16
	Params Params
}

// Replays are the recordings of real stations (evidence A to D) with the
// ports and field sizes of the manifest; TestCorpus checks them against it.
var Replays = []Replay{
	{"wireshark-issues/unknown_sel-rtac_class1-class2-polls.pcap", nil, Params{1, 1, 1, 2}},
	{"wireshark-issues/unknown_sel-rtac_double-command-sbo.pcap", nil, Params{1, 1, 1, 2}},
	{"wireshark-issues/unknown_unknown_energisa-polls-m-sp-ta-1.pcapng", []uint16{2101, 2102, 50001}, Params{1, 1, 1, 2}},
	{"wireshark-issues/unknown_unknown_energisa-polls-m-sp-tb-1.pcapng", []uint16{2101, 2102, 50001}, Params{1, 1, 1, 2}},
}
