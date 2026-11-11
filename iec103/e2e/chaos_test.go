// SPDX-License-Identifier: MIT

package e2e

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/otfabric/go-iec60870-5/iec103"
	"github.com/otfabric/go-iec60870-5/iec103/client"
	"github.com/otfabric/go-iec60870-5/iec103/server"
	"github.com/otfabric/go-iec60870-5/internal/testutil/serialline"
	"github.com/otfabric/go-iec60870-5/link"
)

// The chaos test runs a controlling system and protection equipment over a
// line that drops, duplicates, corrupts, delays, splits, truncates and
// reorders frames and adds noise, with concurrent requests (general
// interrogation, general commands, time synchronization, broadcast
// synchronization, the list of disturbances), spontaneous events and
// class 2 measurands, and checks:
//
//   - no panic, no deadlock: every request returns, with an answer or an
//     error, within its deadline and a bound
//   - no general command is executed twice: a repeated frame is answered
//     from the FCB bookkeeping, never processed again (IEC 60870-5-2 5.1);
//     consecutive commands carry different RII, so the same RII twice in a
//     row is a repetition processed again
//   - once the faults stop the link comes up again and serves requests
//   - after Close nothing is left running
//
// E2E_CHAOS_SECONDS and E2E_CHAOS_SEEDS as for IEC 101 and IEC 104;
// `make chaos` soaks it.

func TestChaos(t *testing.T) {
	d, seeds := serialline.ChaosRuns()
	for seed := 1; seed <= seeds; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) { chaos103(t, int64(seed), d) })
	}
}

// chaosEquipment acknowledges commands and answers interrogations, and
// records a command whose RII repeats the previous one.
type chaosEquipment struct {
	mu      sync.Mutex
	last    int // RII of the previous command, -1 for none
	runs    int
	repeats []uint8
}

func (h *chaosEquipment) HandleASDU(s *server.Server, a *iec103.ASDU) {
	switch b := a.Body.(type) {
	case iec103.GeneralCommand:
		h.mu.Lock()
		if int(b.RII) == h.last {
			h.repeats = append(h.repeats, b.RII)
		}
		h.last = int(b.RII)
		h.runs++
		h.mu.Unlock()
		_ = s.AckCommand(a, true)
		_ = s.Event(iec103.CauseSpontaneous, a.Fun, a.Inf, b.DCO)
	case iec103.GIInit:
		_ = s.AnswerGI(a, iec103.New(iec103.CauseGI, s.CommonAddr(), fun, 16, iec103.TimeTagged{DPI: iec103.DPIOn, Time: s.Now()}),
			iec103.New(iec103.CauseGI, s.CommonAddr(), fun, 17, iec103.TimeTagged{DPI: iec103.DPIOff, Time: s.Now()}))
	}
}

func chaos103(t *testing.T, seed int64, d time.Duration) {
	check := serialline.Goroutines(t)
	// The client's primary messages stay in order (serialline.InOrder).
	line := serialline.New(seed, serialline.InOrder(serialline.ChaosFaults), serialline.ChaosFaults)
	h := &chaosEquipment{last: -1}
	lc := link.Config{Timeout: 60 * time.Millisecond, Retries: 3}
	s, err := server.New(line.B, server.Config{Link: lc, LinkAddr: 3, Fun: fun, Handler: h, Disturbances: newRecords(), QueueSize: 256,
		Identification: iec103.IdentificationOf(iec103.COLWithGeneric, "OTFABRIC", [4]byte{1, 0, 0, 0})})
	if err != nil {
		t.Fatal(err)
	}
	var events atomic.Int64
	c, err := client.New(line.A, client.Config{Link: lc, LinkAddr: 3, PollInterval: 5 * time.Millisecond,
		RetryInterval: 20 * time.Millisecond, RequestTimeout: 2 * time.Second, Handler: func(*iec103.ASDU) { events.Add(1) }})
	if err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { // events and measurands
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			case <-time.After(25 * time.Millisecond):
			}
			if i%2 == 0 {
				_ = s.Event(iec103.CauseSpontaneous, fun, 64, iec103.DPIOn)
			} else {
				_ = s.Send(iec103.New(iec103.CauseCyclic, s.CommonAddr(), fun, 148, iec103.Measurands{Values: []iec103.MEA{iec103.MEAOf(0.5)}}), server.Class2)
			}
		}
	}()
	var ok, failed atomic.Int64
	for w := range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed*100 + int64(w)))
			for {
				select {
				case <-stop:
					return
				default:
				}
				ctx, cancel := context.WithTimeout(context.Background(), time.Duration(200+rng.Intn(1000))*time.Millisecond)
				err := serialline.Within(t, 5*time.Second, "request", func() error {
					switch rng.Intn(6) {
					case 0:
						_, err := c.Interrogate(ctx)
						return err
					case 1, 2:
						return c.Command(ctx, fun, 16, iec103.DPIOn)
					case 3:
						_, err := c.ClockSync(ctx, time.Now())
						return err
					case 4:
						return c.BroadcastClockSync(time.Now())
					default:
						_, err := c.DisturbanceList(ctx, fun)
						return err
					}
				})
				cancel()
				if err != nil {
					failed.Add(1)
					time.Sleep(time.Millisecond) // ErrBusy and ErrLinkDown return at once
				} else {
					ok.Add(1)
				}
			}
		}()
	}
	time.Sleep(d)
	close(stop)
	wg.Wait()

	line.Calm(true)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for {
		_, err := c.Interrogate(ctx)
		if err == nil {
			break
		}
		if ctx.Err() != nil {
			t.Fatalf("no interrogation after the faults stopped: %v (client %s, server %s)", err, c.State(), s.State())
		}
		time.Sleep(10 * time.Millisecond)
	}
	if c.State() != iec103.StateUp || s.State() != iec103.StateUp {
		t.Errorf("after the faults: client %s, server %s", c.State(), s.State())
	}
	h.mu.Lock()
	if len(h.repeats) > 0 {
		t.Errorf("commands executed again for a repeated frame, RII %v", h.repeats)
	}
	runs := h.runs
	h.mu.Unlock()
	if ok.Load() == 0 {
		t.Errorf("no request succeeded in %v", d)
	}
	t.Logf("requests %d ok %d failed, %d commands executed, %d ASDUs to the handler, line %+v",
		ok.Load(), failed.Load(), runs, events.Load(), line.Stats())
	if err := serialline.Within(t, closeBound, "client Close", c.Close); err != nil {
		t.Errorf("client close: %v", err)
	}
	if err := serialline.Within(t, closeBound, "server Close", s.Close); err != nil {
		t.Errorf("server close: %v", err)
	}
	check()
}
