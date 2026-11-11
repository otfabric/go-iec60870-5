// SPDX-License-Identifier: MIT

package client

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/otfabric/go-iec60870-5/cp"
	"github.com/otfabric/go-iec60870-5/ft12"
	"github.com/otfabric/go-iec60870-5/iec103"
	"github.com/otfabric/go-iec60870-5/internal/testutil/serialline"
	"github.com/otfabric/go-iec60870-5/link"
)

// FuzzReceive starts one request of every kind against equipment that
// acknowledges them on the link and answers nothing, then feeds an
// arbitrary stream of user data (per ASDU a length octet, then that many
// octets) to the client's receive path, where the requests wait for their
// answers. The requests take the counter values 1 (general interrogation),
// 2 (command), 3 (generic read) and 4 (general interrogation of generic
// data) in that order, so the seeds can answer them. It checks that
// nothing panics, that every request returns by its deadline and that no
// waiter is left behind.
func FuzzReceive(f *testing.F) {
	enc := func(as ...*iec103.ASDU) []byte {
		var out []byte
		for _, a := range as {
			b, err := a.Encode()
			if err != nil {
				f.Fatal(err)
			}
			out = append(out, byte(len(b)))
			out = append(out, b...)
		}
		return out
	}
	const fun = 128
	f.Add(enc(
		iec103.New(iec103.CauseGI, 3, fun, 16, iec103.TimeTagged{DPI: iec103.DPIOn, SIN: 1}),
		iec103.New(iec103.CauseGITermination, 3, iec103.FunGlobal, iec103.InfGIEnd, iec103.GITermination{SCN: 1}),
		iec103.New(iec103.CauseCommandAck, 3, fun, 16, iec103.TimeTagged{DPI: iec103.DPIOn, SIN: 2}),
		iec103.New(iec103.CauseGenericReadValid, 3, iec103.FunGeneric, iec103.InfReadGroup, iec103.GenericData{RII: 3, NGD: iec103.Counted{Cont: true}}),
		iec103.New(iec103.CauseGenericReadValid, 3, iec103.FunGeneric, iec103.InfReadGroup, iec103.GenericData{RII: 3}),
		iec103.New(iec103.CauseGI, 3, iec103.FunGeneric, iec103.InfReadEntry, iec103.GenericData{RII: 4}),
		iec103.New(iec103.CauseGITermination, 3, iec103.FunGeneric, iec103.InfGenericGI, iec103.GenericData{RII: 4}),
		iec103.New(iec103.CauseTimeSync, 3, iec103.FunGlobal, iec103.InfTimeSync, iec103.TimeSync{Time: cp.At(time.Now())}),
		iec103.New(iec103.CauseDisturbanceData, 3, fun, 0, iec103.DisturbanceList{Faults: []iec103.RecordedFault{{FAN: 7}}}),
		iec103.New(iec103.CauseDisturbanceData, 3, fun, 0, iec103.DisturbanceReady{FAN: 7, NOF: 1, NOC: 1, NOE: 2}),
		iec103.New(iec103.CauseDisturbanceData, 3, fun, 0, iec103.TagsReady{FAN: 7}),
		iec103.New(iec103.CauseDisturbanceData, 3, fun, 0, iec103.Tags{FAN: 7, Tags: []iec103.Tag{{Fun: fun, Inf: 84, DPI: iec103.DPIOn}}}),
		iec103.New(iec103.CauseDisturbanceData, 3, fun, 0, iec103.EndOfTransmission{TOO: iec103.TOOEndTags, FAN: 7}),
		iec103.New(iec103.CauseDisturbanceData, 3, fun, 0, iec103.ChannelReady{FAN: 7, ACC: 1, RPV: 1, RSV: 1, RFA: 1}),
		iec103.New(iec103.CauseDisturbanceData, 3, fun, 0, iec103.DisturbanceValues{FAN: 7, ACC: 1, Values: []int16{1, 2}}),
		iec103.New(iec103.CauseDisturbanceData, 3, fun, 0, iec103.EndOfTransmission{TOO: iec103.TOOEndChannel, FAN: 7, ACC: 1}),
		iec103.New(iec103.CauseDisturbanceData, 3, fun, 0, iec103.EndOfTransmission{TOO: iec103.TOOEndData, FAN: 7}),
	))
	f.Add(enc(iec103.New(iec103.CauseDisturbanceData, 3, fun, 0, iec103.EndOfTransmission{TOO: iec103.TOOEndDataAbortProt, FAN: 7}),
		iec103.New(iec103.CauseCommandNack, 3, fun, 16, iec103.TimeTagged{SIN: 2})))
	f.Add([]byte{6, 5, 0x81, 4, 3, 0xFF, 3})
	f.Fuzz(func(t *testing.T, data []byte) {
		a, b := serialline.Pair()
		defer func() { _ = b.Close() }()
		rs := serialline.NewRawSecondary(serialline.NewPeer(t, b, ft12.Params{AddrSize: 1}), 3)
		rs.Start()
		c, err := New(a, Config{Link: link.Config{Timeout: 50 * time.Millisecond}, LinkAddr: 3, NoInitGI: true, NoInitTimeSync: true,
			PollInterval: time.Millisecond, Handler: func(*iec103.ASDU) {}})
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = c.Close() }()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := c.WaitUp(ctx); err != nil {
			t.Fatal(err)
		}
		waiters := func() int { c.mu.Lock(); defer c.mu.Unlock(); return len(c.waiters) }
		var wg sync.WaitGroup
		start := func(req func() error) {
			n := waiters()
			wg.Add(1)
			go func() {
				defer wg.Done()
				t0 := time.Now()
				_ = req()
				if d := time.Since(t0); d > 3*time.Second {
					t.Errorf("request returned after %v", d)
				}
			}()
			for deadline := time.Now().Add(time.Second); waiters() == n && time.Now().Before(deadline); {
				time.Sleep(100 * time.Microsecond)
			}
		}
		start(func() error { _, err := c.Interrogate(ctx); return err })
		start(func() error { return c.Command(ctx, fun, 16, iec103.DPIOn) })
		start(func() error { _, err := c.ReadGroup(ctx, 1, iec103.KODActualValue); return err })
		start(func() error { _, err := c.GenericInterrogation(ctx); return err })
		start(func() error { _, err := c.ClockSync(ctx, time.Now()); return err })
		start(func() error { _, err := c.DisturbanceList(ctx, fun); return err })
		start(func() error { _, err := c.Disturbance(ctx, fun, 7); return err })
		for len(data) > 0 {
			n := min(int(data[0]), len(data)-1)
			c.receive(data[1 : 1+n])
			data = data[1+n:]
		}
		time.Sleep(time.Millisecond) // orders of the disturbance transfer
		cancel()
		wg.Wait()
		if n := waiters(); n != 0 {
			t.Fatalf("%d waiters left", n)
		}
	})
}
