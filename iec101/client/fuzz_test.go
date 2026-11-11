// SPDX-License-Identifier: MIT

package client

import (
	"testing"

	"github.com/otfabric/go-iec60870-5/asdu"
	"github.com/otfabric/go-iec60870-5/iec101"
	"github.com/otfabric/go-iec60870-5/internal/testutil/serialline"
)

// FuzzReceive feeds arbitrary user data (per ASDU a length octet, then
// that many octets) to the receive path of a client, as the link layer
// hands it over, with and without the profile checks of Params.Strict, and
// checks that nothing panics; the ASDUs that answer no request go to the
// handler.
func FuzzReceive(f *testing.F) {
	p := iec101.DefaultParams().ASDU
	var seed []byte
	for _, a := range []*asdu.ASDU{
		asdu.New(asdu.CauseActivationCon, 5, asdu.Interrogation{Qualifier: asdu.QOIStation}),
		asdu.New(asdu.CauseInterrogatedStation, 5, asdu.SinglePoint{IOA: 1}),
		asdu.New(asdu.CauseActivationTerm, 5, asdu.SingleCommand{IOA: 1}),
		asdu.New(asdu.CauseSpontaneous, 5, asdu.MeasuredFloat{IOA: 9, Time: asdu.Now()}),
		asdu.New(asdu.CauseInitialized, 5, asdu.EndOfInitialization{}),
		asdu.New(asdu.CauseUnknownType, 5, asdu.ResetProcess{Qualifier: 1}),
	} {
		b, err := a.Encode(p)
		if err != nil {
			f.Fatal(err)
		}
		seed = append(seed, byte(len(b)))
		seed = append(seed, b...)
	}
	f.Add(seed, false)
	f.Add(seed, true)
	f.Add([]byte{4, 0xFF, 0x81, 3, 5}, true)
	f.Fuzz(func(t *testing.T, data []byte, strict bool) {
		a, b := serialline.Pair()
		defer func() { _ = b.Close() }()
		params := iec101.DefaultParams()
		params.Strict = strict

		c, err := New(a, Config{Params: params, LinkAddr: 3, RetryInterval: 1 << 40, Handler: func(*asdu.ASDU) {}})
		if err != nil {
			t.Fatal(err)
		}
		for len(data) > 0 {
			n := min(int(data[0]), len(data)-1)
			c.receive(data[1 : 1+n])
			data = data[1+n:]
		}
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
	})
}
