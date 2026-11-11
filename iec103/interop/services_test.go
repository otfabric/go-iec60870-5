//go:build interop

// SPDX-License-Identifier: MIT

package interop

import (
	"encoding/json"
	"fmt"
	"strconv"
	"testing"
)

// The values checks of contract 2.3 (scripts/interop-iec103.sh): every
// reference client that declares the generic services or the transmission
// of disturbance data runs them against this module's server, which serves
// the reference's own fixture; what arrives must be the fixture's.
func TestReferenceClientServices(t *testing.T) {
	for _, a := range clients(t) {
		f := loadFixture(t, a)
		t.Run(a.name, func(t *testing.T) {
			h := &host{t: t, f: f}
			h.restart()
			t.Run("values.generic-read", func(t *testing.T) {
				if !a.caps.Features["genericServices"] {
					t.Skip("client feature genericServices not declared")
				}
				if len(f.Generic) == 0 {
					t.Fatal("fixture without generic entries")
				}
				// Clause 7.4.8.6.2: KOD 1 is the entry's GDD and value, KOD
				// 10 its description.
				var got, want []setDoc
				for _, g := range f.Generic {
					gin := fmt.Sprintf("%d.%d", g.GIN.Group, g.GIN.Entry)
					want = append(want,
						setDoc{GIN: gin, KOD: 1, Type: g.Type, Size: g.Size, Number: 1, Values: []any{g.want()}},
						setDoc{GIN: gin, KOD: 10, Type: 1, Size: len(g.Description), Number: 1, Values: []any{g.Description}})
					for _, kod := range []string{"1", "10"} {
						r, _ := h.run(t, a, "generic-read", "--gin", gin, "--kod", kod)
						if r.Generic != nil {
							got = append(got, r.Generic.Sets...)
						}
					}
				}
				valueCheck(t, a, "generic-read", normalize(got), normalize(want))
			})
			t.Run("values.generic-write", func(t *testing.T) {
				if !a.caps.Features["genericServices"] {
					t.Skip("client feature genericServices not declared")
				}
				h.restart()
				// Clause 7.4.8.6.3, figure 17: cause 40, then the new value.
				r, _ := h.run(t, a, "generic-write", "--gin", "1.2", "--type", "7", "--size", "4", "--value", "2.25")
				cots := []int{}
				for _, c := range r.Confirmations {
					cots = append(cots, c.Cot)
				}
				after, _ := h.run(t, a, "generic-read", "--gin", "1.2")
				var vals []any
				if after.Generic != nil {
					for _, s := range after.Generic.Sets {
						vals = append(vals, s.Values...)
					}
				}
				valueCheck(t, a, "generic-write", fmt.Sprint(cots, normalize(vals)), fmt.Sprint([]int{40}, normalize([]any{2.25})))
			})
			t.Run("values.disturbance-list", func(t *testing.T) {
				if !a.caps.Features["disturbanceList"] {
					t.Skip("client feature disturbanceList not declared")
				}
				r, _ := h.run(t, a, "disturbance-list")
				want := []faultDoc{}
				for _, d := range f.Disturbances {
					w := faultDoc{FAN: d.FAN, Time: d.Time}
					w.SOF.TP, w.SOF.TM, w.SOF.Test, w.SOF.OTEV = d.SOF.TP, d.SOF.TM, d.SOF.Test, d.SOF.OTEV
					want = append(want, w)
				}
				got := r.Disturbances
				if got == nil {
					got = []faultDoc{}
				}
				valueCheck(t, a, "disturbance-list", got, want)
			})
			t.Run("values.disturbance-transfer", func(t *testing.T) {
				if !a.caps.Features["disturbanceData"] {
					t.Skip("client feature disturbanceData not declared")
				}
				if len(f.Disturbances) == 0 {
					t.Fatal("fixture without disturbances")
				}
				h.restart()
				d := f.Disturbances[0]
				r, code := h.run(t, a, "disturbance-transfer", "--fan", strconv.Itoa(d.FAN))
				// Clause 7.4.7: ASDU 26, the tags with their positions,
				// every channel with its values. NOF and TOV are compared
				// where the reference reports them.
				// The values of the channels (ASDU 30) are a check of their own.
				want := wantDisturbance(d)
				wantVals := [][]int{}
				for i := range want.Channels {
					wantVals = append(wantVals, want.Channels[i].Values)
					want.Channels[i].Values = nil
				}
				var got any = fmt.Sprintf("exit %d %s", code, r.summary())
				gotVals := [][]int{}
				if r.Disturbance != nil {
					g := *r.Disturbance
					if g.NOF != nil && *g.NOF != d.NOF || g.TOV != nil && *g.TOV != 1 {
						t.Errorf("NOF %v TOV %v, want %d 1", deref(g.NOF), deref(g.TOV), d.NOF)
					}
					g.NOF, g.TOV = nil, nil
					g.Channels = append([]channelDoc{}, g.Channels...)
					for i := range g.Channels {
						gotVals = append(gotVals, g.Channels[i].Values)
						g.Channels[i].Values = nil
					}
					got = g
				}
				t.Run("description", func(t *testing.T) { valueCheck(t, a, "disturbance-transfer", got, want) })
				t.Run("values", func(t *testing.T) { valueCheck(t, a, "disturbance-transfer values", gotVals, wantVals) })
			})
		})
	}
}

func deref(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}

// normalize makes the numbers of single precision compare as the
// documents write them.
func normalize(v any) any {
	b, _ := json.Marshal(v)
	var out any
	_ = json.Unmarshal(b, &out)
	var walk func(any) any
	walk = func(x any) any {
		switch y := x.(type) {
		case []any:
			for i := range y {
				y[i] = walk(y[i])
			}
		case map[string]any:
			for k := range y {
				y[k] = walk(y[k])
			}
		case float64:
			return float64(float32(y))
		}
		return x
	}
	return walk(out)
}
