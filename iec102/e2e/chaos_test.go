// SPDX-License-Identifier: MIT

package e2e

import (
	"context"
	"encoding/binary"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/otfabric/go-iec60870-5/iec102"
	"github.com/otfabric/go-iec60870-5/iec102/client"
	"github.com/otfabric/go-iec60870-5/iec102/server"
	"github.com/otfabric/go-iec60870-5/internal/testutil/serialline"
	"github.com/otfabric/go-iec60870-5/link"
)

// The chaos test runs a controlling station and an integrated total
// station over a line that drops, duplicates, corrupts, delays, splits,
// truncates and reorders frames and adds noise, with concurrent requests
// (time, totals, single points, private requests the handler executes),
// and checks:
//
//   - no panic, no deadlock: every request returns, with an answer or an
//     error, within its deadline and a bound
//   - no private request is executed twice: a repeated frame is answered
//     from the FCB bookkeeping, never processed again (IEC 60870-5-2 5.1)
//   - once the faults stop the link comes up again and serves requests
//   - after Close nothing is left running
//
// E2E_CHAOS_SECONDS and E2E_CHAOS_SEEDS as for IEC 101 and IEC 104;
// `make chaos` soaks it.

func TestChaos(t *testing.T) {
	d, seeds := serialline.ChaosRuns()
	for seed := 1; seed <= seeds; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) { chaos102(t, int64(seed), d) })
	}
}

// chaosExec executes each private request once per key.
type chaosExec struct {
	mu   sync.Mutex
	runs map[uint32]int
	dup  []uint32
}

func (e *chaosExec) HandleASDU(s *server.Server, a *iec102.ASDU) {
	raw, ok := a.Body.(iec102.Raw)
	if !ok || a.Type != privateType || len(raw.Data) != 4 {
		_ = s.Mirror(a, iec102.CauseUnknownType)
		return
	}
	k := binary.LittleEndian.Uint32(raw.Data)
	e.mu.Lock()
	e.runs[k]++
	if e.runs[k] > 1 {
		e.dup = append(e.dup, k)
	}
	e.mu.Unlock()
	_ = s.Mirror(a, iec102.CauseActivationCon)
}

func chaos102(t *testing.T, seed int64, d time.Duration) {
	check := serialline.Goroutines(t)
	line := serialline.New(seed, serialline.ChaosFaults, serialline.ChaosFaults)
	e := &chaosExec{runs: map[uint32]int{}}
	lc := link.Config{Timeout: 60 * time.Millisecond, Retries: 3}
	s, err := server.New(line.B, server.Config{Link: lc, LinkAddr: linkAddr, CommonAddr: commonAddr, Store: meter(), Handler: e})
	if err != nil {
		t.Fatal(err)
	}
	var other atomic.Int64
	c, err := client.New(line.A, client.Config{Link: lc, LinkAddr: linkAddr, CommonAddr: commonAddr,
		PollInterval: 5 * time.Millisecond, RequestTimeout: 2 * time.Second, Handler: func(*iec102.ASDU) { other.Add(1) }})
	if err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	var wg sync.WaitGroup
	var key atomic.Uint32
	var ok, failed atomic.Int64
	for w := range 3 {
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
				ctx, cancel := context.WithTimeout(context.Background(), time.Duration(300+rng.Intn(1500))*time.Millisecond)
				err := serialline.Within(t, 5*time.Second, "request", func() error {
					switch rng.Intn(5) {
					case 0:
						_, err := c.ReadTime(ctx)
						return err
					case 1:
						_, err := c.ReadTotals(ctx, 21, iec102.Read{T: iec102.C_CI_NG_2})
						return err
					case 2:
						from, to := hour(1, 0), hour(1, 12)
						_, err := c.ReadSinglePoints(ctx, 52, &from, &to)
						return err
					default:
						k := make([]byte, 4)
						binary.LittleEndian.PutUint32(k, key.Add(1))
						a := iec102.New(iec102.CauseActivation, commonAddr, 0, iec102.Raw{Data: k})
						a.Type = privateType
						_, err := c.Request(ctx, a, client.UntilConfirmation)
						return err
					}
				})
				cancel()
				if err != nil {
					failed.Add(1)
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
		_, err := c.ReadTime(ctx)
		if err == nil {
			break
		}
		if ctx.Err() != nil {
			t.Fatalf("no answer after the faults stopped: %v (client %s, server %s)", err, c.State(), s.State())
		}
	}
	if c.State() != iec102.StateUp || s.State() != iec102.StateUp {
		t.Errorf("after the faults: client %s, server %s", c.State(), s.State())
	}
	e.mu.Lock()
	if len(e.dup) > 0 {
		t.Errorf("requests executed more than once: %v", e.dup)
	}
	executed := len(e.runs)
	e.mu.Unlock()
	if ok.Load() == 0 {
		t.Errorf("no request succeeded in %v", d)
	}
	t.Logf("requests %d ok %d failed, %d private executed, %d to the handler, line %+v",
		ok.Load(), failed.Load(), executed, other.Load(), line.Stats())
	if err := serialline.Within(t, closeBound, "client Close", c.Close); err != nil {
		t.Errorf("client close: %v", err)
	}
	if err := serialline.Within(t, closeBound, "server Close", s.Close); err != nil {
		t.Errorf("server close: %v", err)
	}
	check()
}
