// SPDX-License-Identifier: MIT

package telecontrol

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/otfabric/go-iec60870-5/asdu"
)

// FuzzDispatch runs every kind of request at once and dispatches an
// arbitrary stream of ASDUs to them: per ASDU a length octet, then that
// many octets, decoded with the IEC 101 parameters (undecodable ones are
// skipped, as the stations drop them). It checks that nothing panics,
// that every request returns by its deadline, that a request reports data
// only of the station it addressed, and that no exchange is left behind.
func FuzzDispatch(f *testing.F) {
	enc := func(as ...*asdu.ASDU) []byte {
		var out []byte
		for _, a := range as {
			b, err := a.Encode(p)
			if err != nil {
				f.Fatal(err)
			}
			out = append(out, byte(len(b)))
			out = append(out, b...)
		}
		return out
	}
	gi := asdu.New(asdu.CauseActivation, 1, asdu.Interrogation{Qualifier: asdu.QOIStation})
	cmd := asdu.New(asdu.CauseActivation, 1, asdu.SingleCommand{IOA: 7, Value: true})
	ci := asdu.New(asdu.CauseActivation, 1, asdu.CounterInterrogation{Request: asdu.CounterGeneral, Freeze: asdu.FreezeRead})
	f.Add(enc(gi.Reply(asdu.CauseActivationCon, false), asdu.New(asdu.CauseInterrogatedStation, 1, asdu.SinglePoint{IOA: 1}),
		gi.Reply(asdu.CauseActivationTerm, false), cmd.Reply(asdu.CauseActivationTerm, false), cmd.Reply(asdu.CauseActivationCon, false),
		asdu.New(asdu.CauseRequest, 1, asdu.MeasuredFloat{IOA: 9, Value: 1}),
		ci.Reply(asdu.CauseActivationCon, false), asdu.New(asdu.CauseCounterGeneral, 1, asdu.IntegratedTotal{IOA: 3}),
		ci.Reply(asdu.CauseActivationTerm, false)))
	f.Add(enc(gi.Reply(asdu.CauseUnknownCommonAddr, true), cmd.Reply(asdu.CauseActivationCon, true),
		asdu.New(asdu.CauseUnknownIOA, 1, asdu.Read{IOA: 9}), asdu.New(asdu.CauseActivationCon, 1, asdu.ClockSync{})))
	f.Add([]byte{3, 0xFF, 0x00, 0x00, 1, 100})
	f.Fuzz(func(t *testing.T, stream []byte) {
		var as []*asdu.ASDU
		for len(stream) > 0 {
			n := min(int(stream[0]), len(stream)-1)
			if a, err := asdu.Decode(stream[1:1+n], p); err == nil {
				as = append(as, a)
			}
			stream = stream[1+n:]
		}
		sent := make(chan struct{}, 16)
		r := &Requester{Params: p, Timeout: time.Second}
		r.Send = func(context.Context, *asdu.ASDU) error { sent <- struct{}{}; return nil }
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		var wg sync.WaitGroup
		run := func(fn func() ([]*asdu.ASDU, error)) {
			wg.Add(1)
			go func() {
				defer wg.Done()
				start := time.Now()
				data, _ := fn()
				if d := time.Since(start); d > 2*time.Second {
					t.Errorf("request returned after %v", d)
				}
				for _, a := range data {
					if a.CommonAddr != 1 {
						t.Errorf("data of station %d", a.CommonAddr)
					}
				}
			}()
		}
		run(func() ([]*asdu.ASDU, error) { return r.Interrogate(ctx, 1, asdu.QOIStation) })
		run(func() ([]*asdu.ASDU, error) {
			return r.CounterInterrogate(ctx, 1, asdu.CounterGeneral, asdu.FreezeRead)
		})
		run(func() ([]*asdu.ASDU, error) {
			return nil, r.Command(ctx, 1, asdu.SingleCommand{IOA: 7, Value: true}, false)
		})
		run(func() ([]*asdu.ASDU, error) { return nil, r.ClockSync(ctx, 1, time.Now()) })
		run(func() ([]*asdu.ASDU, error) { return nil, r.TestCommand(ctx, 1, false) })
		run(func() ([]*asdu.ASDU, error) { _, err := r.MeasureDelay(ctx, 1); return nil, err })
		run(func() ([]*asdu.ASDU, error) {
			a, err := r.Read(ctx, 1, 9)
			if a != nil {
				return []*asdu.ASDU{a}, err
			}
			return nil, err
		})
		for range 7 {
			<-sent
		}
		for _, a := range as {
			r.Dispatch(a)
		}
		cancel() // what the stream did not answer ends now
		wg.Wait()
		r.mu.Lock()
		left := len(r.exchanges)
		r.mu.Unlock()
		if left != 0 {
			t.Fatalf("%d exchanges left", left)
		}
	})
}
