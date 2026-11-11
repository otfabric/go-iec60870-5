// SPDX-License-Identifier: MIT

package e2e

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/otfabric/go-iec60870-5/asdu"
	"github.com/otfabric/go-iec60870-5/iec101"
	"github.com/otfabric/go-iec60870-5/iec101/client"
	"github.com/otfabric/go-iec60870-5/iec101/server"
	"github.com/otfabric/go-iec60870-5/internal/testutil/serialline"
)

// The chaos test runs a client and a server over a line that drops,
// duplicates, corrupts, delays, splits, truncates and reorders frames and
// adds noise, with concurrent requests and spontaneous data, and checks
// what must hold whatever happens:
//
//   - no panic, no deadlock: every request returns, with an answer or an
//     error, within its deadline and a bound
//   - no command is executed twice: a repeated frame is answered from the
//     FCB bookkeeping, never processed again (IEC 60870-5-2 5.1)
//   - once the faults stop the link comes up again and serves requests
//   - after Close nothing is left running
//
// E2E_CHAOS_SECONDS sets how long each seed runs (default 2),
// E2E_CHAOS_SEEDS how many seeds (default 3), in each transmission mode;
// -short runs one seed of one second. `make chaos` soaks it.

func TestChaos(t *testing.T) {
	d, seeds := serialline.ChaosRuns()
	for _, mode := range []iec101.Mode{iec101.ModeUnbalanced, iec101.ModeBalanced} {
		for seed := 1; seed <= seeds; seed++ {
			t.Run(fmt.Sprintf("%s/seed=%d", mode, seed), func(t *testing.T) {
				chaos101(t, int64(seed), mode, d)
			})
		}
	}
}

// chaosStation executes each command once per information object address
// and remembers how often it did.
type chaosStation struct {
	mu   sync.Mutex
	runs map[asdu.IOA]int
	dup  []asdu.IOA
}

func (st *chaosStation) HandleASDU(s *server.Server, a *asdu.ASDU) {
	switch o := a.First().(type) {
	case asdu.Interrogation:
		_ = s.Confirm(a)
		_ = s.Send(asdu.New(o.Qualifier.Cause(), ca, asdu.SinglePoint{IOA: 100, Value: true}))
		_ = s.Terminate(a)
	case asdu.SingleCommand:
		st.mu.Lock()
		st.runs[o.IOA]++
		if st.runs[o.IOA] > 1 {
			st.dup = append(st.dup, o.IOA)
		}
		st.mu.Unlock()
		_ = s.Confirm(a)
		_ = s.Terminate(a)
	case asdu.Read:
		_ = s.Send(asdu.New(asdu.CauseRequest, ca, asdu.MeasuredFloat{IOA: o.IOA, Value: 1}))
	case asdu.ClockSync, asdu.TestCommand:
		_ = s.Confirm(a)
	default:
		_ = s.Reject(a, asdu.CauseUnknownType)
	}
}

func chaos101(t *testing.T, seed int64, mode iec101.Mode, d time.Duration) {
	check := serialline.Goroutines(t)
	line := serialline.New(seed, serialline.ChaosFaults, serialline.ChaosFaults)
	p := params(mode)
	p.ASDU.IOASize = 3 // room for a new address per command
	st := &chaosStation{runs: map[asdu.IOA]int{}}
	s, err := server.New(line.B, server.Config{Params: p, LinkAddr: 3, Remote: 1, CommonAddr: ca, Handler: st,
		RetryInterval: 20 * time.Millisecond, QueueSize: 256})
	if err != nil {
		t.Fatal(err)
	}
	var spont atomic.Int64
	c, err := client.New(line.A, client.Config{Params: p, LinkAddr: 3, OwnAddr: 1,
		PollInterval: 5 * time.Millisecond, RetryInterval: 20 * time.Millisecond, TestInterval: 30 * time.Millisecond,
		RequestTimeout: time.Second, Handler: func(a *asdu.ASDU) {
			if a.Cause == asdu.CauseSpontaneous {
				spont.Add(1)
			}
		}})
	if err != nil {
		t.Fatal(err)
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup
	// Spontaneous data, as class 1 and class 2.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			case <-time.After(25 * time.Millisecond):
			}
			class := server.Class1
			if i%2 == 0 {
				class = server.Class2
			}
			_ = s.SendClass(asdu.New(asdu.CauseSpontaneous, ca, asdu.MeasuredScaled{IOA: asdu.IOA(i % 100), Value: int16(i)}), class)
		}
	}()
	var nextIOA atomic.Uint32
	nextIOA.Store(1000)
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
				ctx, cancel := context.WithTimeout(context.Background(), time.Duration(200+rng.Intn(800))*time.Millisecond)
				err := serialline.Within(t, 5*time.Second, "request", func() error {
					switch rng.Intn(6) {
					case 0:
						_, err := c.Interrogate(ctx, ca, asdu.QOIStation)
						return err
					case 1, 2:
						return c.Command(ctx, ca, asdu.SingleCommand{IOA: asdu.IOA(nextIOA.Add(1)), Value: true})
					case 3:
						_, err := c.Read(ctx, ca, asdu.IOA(rng.Intn(50)))
						return err
					case 4:
						return c.ClockSync(ctx, ca, time.Now())
					default:
						return c.TestCommand(ctx, ca)
					}
				})
				cancel()
				if err != nil {
					if os.Getenv("CHAOS_DEBUG") != "" {
						t.Log(err)
					}
					failed.Add(1)
					if errors.Is(err, iec101.ErrLinkDown) {
						time.Sleep(5 * time.Millisecond)
					}
				} else {
					ok.Add(1)
				}
			}
		}()
	}
	time.Sleep(d)
	close(stop)
	wg.Wait()

	// The faults stop: the link must come up again and serve.
	line.Calm(true)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for {
		_, err := c.Interrogate(ctx, ca, asdu.QOIStation)
		if err == nil {
			break
		}
		if ctx.Err() != nil {
			t.Fatalf("no interrogation after the faults stopped: %v (client %s, server %s)", err, c.State(), s.State())
		}
		time.Sleep(10 * time.Millisecond)
	}
	if c.State() != iec101.StateUp || s.State() != iec101.StateUp {
		t.Errorf("after the faults: client %s, server %s", c.State(), s.State())
	}
	st.mu.Lock()
	if len(st.dup) > 0 {
		t.Errorf("commands executed more than once: %v", st.dup)
	}
	executed := len(st.runs)
	st.mu.Unlock()
	if ok.Load() == 0 {
		t.Errorf("no request succeeded in %v", d)
	}
	t.Logf("requests %d ok %d failed, %d commands executed, %d spontaneous, line %+v",
		ok.Load(), failed.Load(), executed, spont.Load(), line.Stats())

	if err := serialline.Within(t, closeBound, "client Close", c.Close); err != nil {
		t.Errorf("client close: %v", err)
	}
	if err := serialline.Within(t, closeBound, "server Close", s.Close); err != nil {
		t.Errorf("server close: %v", err)
	}
	check()
}
