// SPDX-License-Identifier: MIT

package e2e

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/otfabric/go-iec60870-5/ft12"
	"github.com/otfabric/go-iec60870-5/iec102"
	"github.com/otfabric/go-iec60870-5/iec102/client"
	"github.com/otfabric/go-iec60870-5/internal/testutil/serialline"
	"github.com/otfabric/go-iec60870-5/link"
)

// FuzzClientAnswers plays an integrated total station that answers the
// client's polls with an arbitrary stream of user data (per ASDU a length
// octet, a flag octet whose bit 0 sets ACD, then the octets) while the
// client reads totals, single points, the time and the product
// specification. It checks that nothing panics and every request returns
// by its deadline.
func FuzzClientAnswers(f *testing.F) {
	enc := func(acd byte, as ...*iec102.ASDU) []byte {
		var out []byte
		for _, a := range as {
			b, err := a.Encode(iec102.Params{})
			if err != nil {
				f.Fatal(err)
			}
			out = append(out, byte(len(b)), acd)
			out = append(out, b...)
		}
		return out
	}
	q := iec102.Read{T: iec102.C_CI_NU_2, First: 1, Last: 6, From: hour(1, 0), To: hour(3, 0)}
	req := iec102.New(iec102.CauseActivation, commonAddr, 11, q)
	con, term := *req, *req
	con.Cause, term.Cause = iec102.CauseActivationCon, iec102.CauseActivationTerm
	f.Add(enc(0, &con, iec102.New(iec102.CauseRequest, commonAddr, 11, iec102.IntegratedTotals{T: iec102.M_IT_TK_2,
		Totals: []iec102.IntegratedTotal{{Addr: 1, Value: 5}}, Time: hour(1, 1)}), &term,
		iec102.New(iec102.CauseRequest, commonAddr, 0, iec102.SystemTime{Time: iec102.TimeBOf(time.Now(), nil)}),
		iec102.New(iec102.CauseRequest, commonAddr, 0, iec102.ProductSpec{Manufacturer: 1})))
	f.Add(enc(1, &iec102.ASDU{Type: iec102.C_CI_NU_2, Cause: iec102.CauseUnknownRecordAddr | iec102.CauseNegative, CommonAddr: commonAddr, Record: 11, Body: q},
		iec102.New(iec102.CauseSpontaneous, 9, 0, iec102.Raw{Data: []byte{1}})))
	f.Add([]byte{3, 1, 72, 1, 7})
	f.Fuzz(func(t *testing.T, stream []byte) {
		a, b := serialline.Pair()
		defer func() { _ = b.Close() }()
		rs := serialline.NewRawSecondary(serialline.NewPeer(t, b, p2), linkAddr)
		var mu sync.Mutex
		rs.Hook = func(f ft12.Frame, ans [][]byte) [][]byte {
			if fn := serialline.Func(f); fn != 10 && fn != 11 {
				return ans
			}
			mu.Lock()
			defer mu.Unlock()
			if len(stream) < 2 {
				return ans
			}
			n := min(int(stream[0]), len(stream)-2)
			acd := stream[1]&1 != 0
			d := stream[2 : 2+n]
			stream = stream[2+n:]
			if n == 0 {
				return [][]byte{rs.Encode(serialline.Fixed(serialline.Secondary(link.FuncNoData, acd, false), linkAddr))}
			}
			return [][]byte{rs.Encode(serialline.Variable(serialline.Secondary(link.FuncUserData, acd, false), linkAddr, d))}
		}
		rs.Start()
		c, err := client.New(a, client.Config{Link: link.Config{Timeout: 20 * time.Millisecond, Retries: 1}, LinkAddr: linkAddr,
			CommonAddr: commonAddr, PollInterval: time.Millisecond, Handler: func(*iec102.ASDU) {}})
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = c.Close() }()
		for _, op := range []func(context.Context) error{
			func(ctx context.Context) error { _, err := c.ReadTotals(ctx, 11, q); return err },
			func(ctx context.Context) error { _, err := c.ReadTime(ctx); return err },
			func(ctx context.Context) error { _, err := c.ReadProductSpec(ctx); return err },
			func(ctx context.Context) error {
				from, to := hour(1, 0), hour(2, 0)
				_, err := c.ReadSinglePoints(ctx, 52, &from, &to)
				return err
			},
		} {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
			t0 := time.Now()
			_ = op(ctx)
			cancel()
			if d := time.Since(t0); d > 2*time.Second {
				t.Fatalf("request returned after %v", d)
			}
		}
	})
}
