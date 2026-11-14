// SPDX-License-Identifier: MIT

package client

import (
	"context"
	"fmt"
	"strings"

	"github.com/otfabric/go-iec60870-5/iec103"
)

// DisturbanceList requests the list of recorded disturbances of function
// type fun (ASDU 24 with TOO 24, clause 7.2.6.26) and returns the faults
// of the ASDU 23 the equipment answers with. The equipment also sends
// ASDU 23 on its own (a new disturbance, after a transfer, after a general
// interrogation, clause 7.4.7); those reach the Handler.
func (c *Client) DisturbanceList(ctx context.Context, fun iec103.FunctionType) ([]iec103.RecordedFault, error) {
	ctx, cancel := c.bound(ctx)
	defer cancel()
	w, err := c.request(ctx, iec103.New(iec103.CauseDisturbanceData, c.ca, fun, 0, iec103.DisturbanceOrder{TOO: iec103.TOORequestList}),
		func(a *iec103.ASDU) bool {
			return a.CommonAddr == c.ca && a.Fun == fun && a.Type == iec103.TypeDisturbanceList && a.Cause == iec103.CauseDisturbanceData
		})
	if err != nil {
		return nil, err
	}
	defer c.unwatch(w)
	a, err := c.wait(ctx, w)
	if err != nil {
		return nil, fmt.Errorf("iec103: list of recorded disturbances: %w", err)
	}
	return a.Body.(iec103.DisturbanceList).Faults, nil
}

// transfer is one disturbance data transfer in progress.
type transfer struct {
	c   *Client
	ctx context.Context
	w   *waiter
	fun iec103.FunctionType
	tov uint8
	fan uint16
}

func (t *transfer) order(ack bool, too iec103.TOO, acc uint8) error {
	return t.c.Send(t.ctx, iec103.New(iec103.CauseDisturbanceData, t.c.ca, t.fun, 0,
		iec103.DisturbanceOrder{Ack: ack, TOO: too, TOV: t.tov, FAN: t.fan, ACC: acc}))
}

// next returns the next ASDU of the transfer of one of the given types; an
// ASDU 31 is always returned. An ASDU 31 that ends the whole disturbance
// data aborted by the protection equipment (TOO 34) is acknowledged
// negatively (ASDU 25, TOO 65: clause 7.4.7, figure 10 sheet 4, every end
// of the disturbance data is acknowledged) and returns
// [iec103.ErrAborted].
func (t *transfer) next(types ...iec103.TypeID) (*iec103.ASDU, error) {
	for {
		a, err := t.c.wait(t.ctx, t.w)
		if err != nil {
			return nil, err
		}
		if e, ok := a.Body.(iec103.EndOfTransmission); ok {
			if e.TOO == iec103.TOOEndDataAbortProt {
				if err := t.order(true, iec103.TOODataFailed, iec103.ACCGlobal); err != nil {
					return nil, err
				}
				return nil, fmt.Errorf("%w: TOO %d", iec103.ErrAborted, e.TOO)
			}
			return a, nil
		}
		for _, ty := range types {
			if a.Type == ty {
				return a, nil
			}
		}
		t.c.log.Debug("unexpected ASDU in disturbance data transfer", "asdu", a.String())
	}
}

// Disturbance transfers the recorded disturbance fan of function type fun
// with the procedure of clause 7.4.7 (figure 10):
//
//  1. ASDU 24 "selection of fault", answered by ASDU 26;
//  2. ASDU 24 "request for disturbance data", answered by ASDU 28;
//  3. ASDU 24 "request for tags", answered by ASDUs 29 and an ASDU 31,
//     acknowledged by ASDU 25 "tags transmitted successfully", or "not
//     successfully" when the equipment aborted them (TOO 40) or the
//     initial states were not at position 0 (clause 7.4.7);
//  4. for each ASDU 27 the equipment offers: ASDU 24 "channel request",
//     answered by ASDUs 30 and an ASDU 31, acknowledged by ASDU 25 positive
//     when NFE runs without gaps up to NOE, negative otherwise or when the
//     equipment aborted the channel (TOO 37);
//  5. the ASDU 31 "end of disturbance data", acknowledged by ASDU 25:
//     negative when tags or a channel were, or when fewer channels came
//     than NOC announced.
//
// A negative acknowledgement returns the record with an error wrapping
// [iec103.ErrNegative]; clause 7.4.7 lets the equipment then offer the
// transfer again. An abortion of the whole transfer by the equipment (TOO
// 34) is acknowledged negatively and returns [iec103.ErrAborted]. The ctx
// should allow for the whole transfer; on its expiry the transfer is left
// as it is (no abortion order is sent). One transfer at a time.
//
// The record holds at most what the ASDU 26 announced: NOC channels of NOE
// values each (clauses 7.2.6.10, 7.2.6.11). A value beyond NOE in a
// channel, or a channel beyond NOC, ends the transfer at once with the
// record so far and an error wrapping [iec103.ErrInvalid]; as on the expiry
// of ctx, no order is sent for it.
func (c *Client) Disturbance(ctx context.Context, fun iec103.FunctionType, fan uint16) (*iec103.Disturbance, error) {
	if err := lock(&c.distMu); err != nil {
		return nil, err
	}
	defer c.distMu.Unlock()
	ctx, cancel := c.bound(ctx)
	defer cancel()
	w, err := c.watch(func(a *iec103.ASDU) bool {
		return a.CommonAddr == c.ca && a.Fun == fun && a.Cause == iec103.CauseDisturbanceData &&
			a.Type >= iec103.TypeDisturbanceReady && a.Type <= iec103.TypeEndOfTransmission
	})
	if err != nil {
		return nil, err
	}
	defer c.unwatch(w)
	t := &transfer{c: c, ctx: ctx, w: w, fun: fun, tov: iec103.TOVInstantaneous, fan: fan}
	rec, err := t.run()
	if err != nil {
		return rec, fmt.Errorf("iec103: disturbance data FAN %d: %w", fan, err)
	}
	return rec, nil
}

func (t *transfer) run() (*iec103.Disturbance, error) {
	if err := t.order(false, iec103.TOOSelectFault, iec103.ACCGlobal); err != nil {
		return nil, err
	}
	a, err := t.next(iec103.TypeDisturbanceReady)
	if err != nil {
		return nil, err
	}
	ready, ok := a.Body.(iec103.DisturbanceReady)
	if !ok {
		return nil, fmt.Errorf("%w: %v instead of ready for transmission", iec103.ErrInvalid, a)
	}
	if ready.FAN != t.fan {
		return nil, fmt.Errorf("%w: ready for FAN %d", iec103.ErrInvalid, ready.FAN)
	}
	t.tov = ready.TOV
	rec := &iec103.Disturbance{Fun: t.fun, TOV: ready.TOV, FAN: ready.FAN, NOF: ready.NOF, Interval: ready.INT, Start: ready.Time}

	var reasons []string
	if err := t.tags(rec, &reasons); err != nil {
		return rec, err
	}
	if err := t.channels(rec, ready, &reasons); err != nil {
		return rec, err
	}
	too := iec103.TOODataOK
	if reasons != nil {
		too = iec103.TOODataFailed
	}
	if err := t.order(true, too, iec103.ACCGlobal); err != nil {
		return rec, err
	}
	if reasons != nil {
		return rec, fmt.Errorf("%w: %s", iec103.ErrNegative, strings.Join(reasons, "; "))
	}
	return rec, nil
}

// tags requests the tags and acknowledges them.
func (t *transfer) tags(rec *iec103.Disturbance, reasons *[]string) error {
	if err := t.order(false, iec103.TOORequestData, iec103.ACCGlobal); err != nil {
		return err
	}
	if _, err := t.next(iec103.TypeTagsReady); err != nil {
		return err
	}
	if err := t.order(false, iec103.TOORequestTags, iec103.ACCGlobal); err != nil {
		return err
	}
	ok, first := true, true
	for n := 0; ; n++ {
		a, err := t.next(iec103.TypeTags)
		if err != nil {
			return err
		}
		if e, end := a.Body.(iec103.EndOfTransmission); end {
			if e.TOO != iec103.TOOEndTags {
				ok = false
				*reasons = append(*reasons, fmt.Sprintf("tags ended with TOO %d", e.TOO))
			}
			break
		}
		if err := t.c.limit(n, "request for tags"); err != nil {
			return err
		}
		tg := a.Body.(iec103.Tags)
		// Clause 7.4.7: the initial states come first, with position 0.
		if tg.FAN != t.fan || first && tg.TAP != 0 {
			if ok {
				*reasons = append(*reasons, fmt.Sprintf("tags of FAN %d at position %d", tg.FAN, tg.TAP))
			}
			ok = false
		}
		first = false
		for _, x := range tg.Tags {
			rec.Tags = append(rec.Tags, iec103.TagChange{Pos: tg.TAP, Tag: x})
		}
	}
	too := iec103.TOOTagsOK
	if !ok {
		too = iec103.TOOTagsFailed
	}
	return t.order(true, too, iec103.ACCGlobal)
}

// channels requests every channel the equipment offers until the end of
// the disturbance data.
func (t *transfer) channels(rec *iec103.Disturbance, ready iec103.DisturbanceReady, reasons *[]string) error {
	for {
		a, err := t.next(iec103.TypeChannelReady)
		if err != nil {
			return err
		}
		if _, end := a.Body.(iec103.EndOfTransmission); end {
			break
		}
		cr := a.Body.(iec103.ChannelReady)
		if len(rec.Channels) >= int(ready.NOC) {
			return fmt.Errorf("%w: channel ACC %d beyond the %d channels announced (NOC, clause 7.2.6.10)", iec103.ErrInvalid, cr.ACC, ready.NOC)
		}
		ch := iec103.Channel{ACC: cr.ACC, RPV: cr.RPV, RSV: cr.RSV, RFA: cr.RFA}
		if err := t.order(false, iec103.TOORequestChannel, cr.ACC); err != nil {
			return err
		}
		ok := true
		for {
			a, err := t.next(iec103.TypeDisturbanceValues)
			if err != nil {
				return err
			}
			if e, end := a.Body.(iec103.EndOfTransmission); end {
				if e.TOO != iec103.TOOEndChannel {
					ok = false
				}
				break
			}
			v := a.Body.(iec103.DisturbanceValues)
			if len(ch.Values)+len(v.Values) > int(ready.NOE) {
				rec.Channels = append(rec.Channels, ch)
				return fmt.Errorf("%w: channel ACC %d: %d values beyond the %d announced (NOE, clause 7.2.6.11)",
					iec103.ErrInvalid, cr.ACC, len(ch.Values)+len(v.Values)-int(ready.NOE), ready.NOE)
			}
			if v.ACC != cr.ACC || int(v.NFE) != len(ch.Values) {
				ok = false
			}
			ch.Values = append(ch.Values, v.Values...)
		}
		if len(ch.Values) != int(ready.NOE) {
			ok = false
		}
		rec.Channels = append(rec.Channels, ch)
		too := iec103.TOOChannelOK
		if !ok {
			too = iec103.TOOChannelFailed
			*reasons = append(*reasons, fmt.Sprintf("channel ACC %d incomplete or aborted", cr.ACC))
		}
		if err := t.order(true, too, cr.ACC); err != nil {
			return err
		}
	}
	if len(rec.Channels) != int(ready.NOC) {
		*reasons = append(*reasons, fmt.Sprintf("%d of %d channels", len(rec.Channels), ready.NOC))
	}
	return nil
}
