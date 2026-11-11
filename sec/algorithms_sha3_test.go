// SPDX-License-Identifier: MIT

package sec

import "testing"

func TestSHA3(t *testing.T) {
	for _, m := range []MACAlgorithm{HMACSHA3_256, HMACSHA3_256Serial} {
		t.Run(m.Name, func(t *testing.T) {
			// The 8-octet MAC is the serial-link profile (IEC TS 60870-5-7:2025 9.3.2).
			p := newPair(t, func(a, b *Config) { a.MAC, a.Serial, b.Serial = m.Code, m.Len == 8, m.Len == 8 })
			p.up()
			p.mustReady()
			p.exchange()
			if p.b.e.Status().MAC != m.Name {
				t.Errorf("MAC %q", p.b.e.Status().MAC)
			}
		})
	}
}
