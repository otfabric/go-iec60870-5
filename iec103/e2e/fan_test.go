// SPDX-License-Identifier: MIT

package e2e

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/otfabric/go-iec60870-5/iec103"
	"github.com/otfabric/go-iec60870-5/iec103/client"
	"github.com/otfabric/go-iec60870-5/internal/testutil/serialline"
	"github.com/otfabric/go-iec60870-5/link"
)

// fanEquipment is protection equipment for a disturbance transfer of FAN
// 1 with one channel of two values. channel answers the channel request
// after the tags were acknowledged; afterTags, when set, replaces the
// offer of channel 1 that follows the tags.
type fanEquipment struct {
	afterTags func(enc func(iec103.Body) []byte) [][]byte
	channel   func(enc func(iec103.Body) []byte) [][]byte

	mu   sync.Mutex
	acks []iec103.TOO
}

func (e *fanEquipment) acked() []iec103.TOO {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.acks)
}

func (e *fanEquipment) start(t *testing.T, maxResult int) (*client.Client, func()) {
	t.Helper()
	check := serialline.Goroutines(t)
	a, b := serialline.Pair()
	rs := serialline.NewRawSecondary(serialline.NewPeer(t, b, p1), 3)
	enc := func(body iec103.Body) []byte {
		x, err := iec103.New(iec103.CauseDisturbanceData, 3, fun, 0, body).Encode()
		if err != nil {
			t.Error(err)
		}
		return x
	}
	rs.OnUserData = func(d []byte) [][]byte {
		x, err := iec103.Decode(d)
		if err != nil {
			return nil
		}
		switch o := x.Body.(type) {
		case iec103.TimeSync:
			r, _ := iec103.New(iec103.CauseTimeSync, 3, iec103.FunGlobal, iec103.InfTimeSync, iec103.TimeSync{Time: o.Time}).Encode()
			return [][]byte{r}
		case iec103.GIInit:
			r, _ := iec103.New(iec103.CauseGITermination, 3, iec103.FunGlobal, iec103.InfGIEnd, iec103.GITermination(o)).Encode()
			return [][]byte{r}
		case iec103.DisturbanceOrder:
			if o.Ack {
				e.mu.Lock()
				e.acks = append(e.acks, o.TOO)
				e.mu.Unlock()
			}
			switch {
			case !o.Ack && o.TOO == iec103.TOOSelectFault:
				return [][]byte{enc(iec103.DisturbanceReady{FAN: 1, NOF: 1, NOC: 1, NOE: 2})}
			case !o.Ack && o.TOO == iec103.TOORequestData:
				return [][]byte{enc(iec103.TagsReady{FAN: 1})}
			case !o.Ack && o.TOO == iec103.TOORequestTags:
				return [][]byte{enc(iec103.EndOfTransmission{TOO: iec103.TOOEndTags, FAN: 1})}
			case o.Ack && o.TOO == iec103.TOOTagsOK:
				if e.afterTags != nil {
					return e.afterTags(enc)
				}
				return [][]byte{enc(iec103.ChannelReady{FAN: 1, ACC: 1})}
			case !o.Ack && o.TOO == iec103.TOORequestChannel:
				return e.channel(enc)
			case o.Ack && (o.TOO == iec103.TOOChannelOK || o.TOO == iec103.TOOChannelFailed):
				return [][]byte{enc(iec103.EndOfTransmission{TOO: iec103.TOOEndData, FAN: 1})}
			}
		}
		return nil
	}
	rs.Start()
	c, err := client.New(a, client.Config{Link: link.Config{Timeout: 50 * time.Millisecond, Retries: 2}, LinkAddr: 3,
		PollInterval: 5 * time.Millisecond, RetryInterval: 20 * time.Millisecond, RequestTimeout: 5 * time.Second,
		MaxResult: maxResult, Handler: func(*iec103.ASDU) {}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.WaitUp(ctx); err != nil {
		t.Fatal(err)
	}
	return c, func() {
		if err := serialline.Within(t, closeBound, "client Close", c.Close); err != nil {
			t.Error(err)
		}
		_ = b.Close()
		check()
	}
}

// goodChannel is channel 1 of FAN 1, complete.
func goodChannel(enc func(iec103.Body) []byte) [][]byte {
	return [][]byte{
		enc(iec103.DisturbanceValues{FAN: 1, ACC: 1, NFE: 0, Values: []int16{1, 2}}),
		enc(iec103.EndOfTransmission{TOO: iec103.TOOEndChannel, FAN: 1, ACC: 1}),
	}
}

// TestDisturbanceOtherFAN: ASDUs 27, 30 and 31 of another fault than the
// selected one do not enter, end or abort the selected record, and are
// not acknowledged positively for it (IEC 60870-5-103 7.4.7).
func TestDisturbanceOtherFAN(t *testing.T) {
	t.Run("channel offered for another fault", func(t *testing.T) {
		e := &fanEquipment{
			afterTags: func(enc func(iec103.Body) []byte) [][]byte {
				return [][]byte{enc(iec103.ChannelReady{FAN: 2, ACC: 1})}
			},
			channel: goodChannel,
		}
		c, done := e.start(t, 0)
		defer done()
		rec, err := c.Disturbance(context.Background(), fun, 1)
		if !errors.Is(err, iec103.ErrInvalid) {
			t.Fatalf("Disturbance: %v, want ErrInvalid", err)
		}
		if rec == nil || len(rec.Channels) != 0 {
			t.Errorf("record %+v", rec)
		}
		if got := e.acked(); !slices.Equal(got, []iec103.TOO{iec103.TOOTagsOK}) {
			t.Errorf("acknowledgements %v", got)
		}
	})
	t.Run("values of another fault", func(t *testing.T) {
		e := &fanEquipment{channel: func(enc func(iec103.Body) []byte) [][]byte {
			return append([][]byte{enc(iec103.DisturbanceValues{FAN: 2, ACC: 1, NFE: 0, Values: []int16{111, 222}})}, goodChannel(enc)...)
		}}
		c, done := e.start(t, 0)
		defer done()
		rec, err := c.Disturbance(context.Background(), fun, 1)
		if !errors.Is(err, iec103.ErrNegative) {
			t.Fatalf("Disturbance: %v, want ErrNegative", err)
		}
		if len(rec.Channels) != 1 || !slices.Equal(rec.Channels[0].Values, []int16{1, 2}) {
			t.Errorf("channels %+v", rec.Channels)
		}
		want := []iec103.TOO{iec103.TOOTagsOK, iec103.TOOChannelFailed, iec103.TOODataFailed}
		if got := e.acked(); !slices.Equal(got, want) {
			t.Errorf("acknowledgements %v, want %v", got, want)
		}
	})
	t.Run("end and abortion of another fault", func(t *testing.T) {
		e := &fanEquipment{
			afterTags: func(enc func(iec103.Body) []byte) [][]byte {
				return [][]byte{
					enc(iec103.EndOfTransmission{TOO: iec103.TOOEndDataAbortProt, FAN: 2}),
					enc(iec103.EndOfTransmission{TOO: iec103.TOOEndData, FAN: 2}),
					enc(iec103.ChannelReady{FAN: 1, ACC: 1}),
				}
			},
			channel: func(enc func(iec103.Body) []byte) [][]byte {
				return append([][]byte{enc(iec103.EndOfTransmission{TOO: iec103.TOOEndChannelAbortPrt, FAN: 2, ACC: 1})}, goodChannel(enc)...)
			},
		}
		c, done := e.start(t, 0)
		defer done()
		rec, err := c.Disturbance(context.Background(), fun, 1)
		if err != nil {
			t.Fatalf("Disturbance: %v", err)
		}
		if len(rec.Channels) != 1 || !slices.Equal(rec.Channels[0].Values, []int16{1, 2}) {
			t.Errorf("channels %+v", rec.Channels)
		}
		want := []iec103.TOO{iec103.TOOTagsOK, iec103.TOOChannelOK, iec103.TOODataOK}
		if got := e.acked(); !slices.Equal(got, want) {
			t.Errorf("acknowledgements %v, want %v", got, want)
		}
	})
}

// TestDisturbanceEmptyValues: disturbance values without values do not
// keep a channel transfer going past Config.MaxResult.
func TestDisturbanceEmptyValues(t *testing.T) {
	e := &fanEquipment{channel: func(enc func(iec103.Body) []byte) [][]byte {
		var out [][]byte
		for range 6 {
			out = append(out, enc(iec103.DisturbanceValues{FAN: 1, ACC: 1, NFE: 0}))
		}
		return out
	}}
	c, done := e.start(t, 5)
	defer done()
	rec, err := c.Disturbance(context.Background(), fun, 1)
	if !errors.Is(err, iec103.ErrResultLimit) {
		t.Fatalf("Disturbance: %v, want ErrResultLimit", err)
	}
	if rec == nil || len(rec.Channels) != 1 || len(rec.Channels[0].Values) != 0 {
		t.Errorf("record %+v", rec)
	}
}
