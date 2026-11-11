// SPDX-License-Identifier: MIT

package client

import (
	"context"
	"fmt"

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

// next returns the next ASDU of the transfer of one of the given types.
// An end of transmission that reports an abortion by the protection
// equipment returns [iec103.ErrAborted].
func (t *transfer) next(types ...iec103.TypeID) (*iec103.ASDU, error) {
	for {
		a, err := t.c.wait(t.ctx, t.w)
		if err != nil {
			return nil, err
		}
		if e, ok := a.Body.(iec103.EndOfTransmission); ok {
			switch e.TOO {
			case iec103.TOOEndDataAbortProt, iec103.TOOEndChannelAbortPrt, iec103.TOOEndTagsAbortProt:
				return nil, fmt.Errorf("%w: TOO %d", iec103.ErrAborted, e.TOO)
			default:
			}
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
//     acknowledged by ASDU 25 "tags transmitted successfully";
//  4. for each ASDU 27 the equipment offers: ASDU 24 "channel request",
//     answered by ASDUs 30 and an ASDU 31, acknowledged by ASDU 25 positive
//     when NFE runs without gaps up to NOE, negative otherwise;
//  5. the ASDU 31 "end of disturbance data", acknowledged by ASDU 25.
//
// A channel acknowledged negatively makes the final acknowledgement
// negative too and the call returns the record with an error wrapping
// [iec103.ErrNegative]; clause 7.4.7 lets the equipment then offer the
// transfer again. An abortion by the equipment returns
// [iec103.ErrAborted]. The ctx should allow for the whole transfer; on
// its expiry the transfer is left as it is (no abortion order is sent).
// One transfer at a time.
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
	ready := a.Body.(iec103.DisturbanceReady)
	if ready.FAN != t.fan {
		return nil, fmt.Errorf("%w: ready for FAN %d", iec103.ErrInvalid, ready.FAN)
	}
	t.tov = ready.TOV
	rec := &iec103.Disturbance{Fun: t.fun, TOV: ready.TOV, FAN: ready.FAN, NOF: ready.NOF, Interval: ready.INT, Start: ready.Time}

	// Tags.
	if err := t.order(false, iec103.TOORequestData, iec103.ACCGlobal); err != nil {
		return rec, err
	}
	if _, err := t.next(iec103.TypeTagsReady); err != nil {
		return rec, err
	}
	if err := t.order(false, iec103.TOORequestTags, iec103.ACCGlobal); err != nil {
		return rec, err
	}
	for {
		a, err := t.next(iec103.TypeTags, iec103.TypeEndOfTransmission)
		if err != nil {
			return rec, err
		}
		if tg, ok := a.Body.(iec103.Tags); ok {
			for _, x := range tg.Tags {
				rec.Tags = append(rec.Tags, iec103.TagChange{Pos: tg.TAP, Tag: x})
			}
			continue
		}
		break
	}
	if err := t.order(true, iec103.TOOTagsOK, iec103.ACCGlobal); err != nil {
		return rec, err
	}

	// Channels, then the end of the disturbance data.
	failed := false
	for {
		a, err := t.next(iec103.TypeChannelReady, iec103.TypeEndOfTransmission)
		if err != nil {
			return rec, err
		}
		if _, ok := a.Body.(iec103.EndOfTransmission); ok {
			break
		}
		cr := a.Body.(iec103.ChannelReady)
		ch := iec103.Channel{ACC: cr.ACC, RPV: cr.RPV, RSV: cr.RSV, RFA: cr.RFA}
		if err := t.order(false, iec103.TOORequestChannel, cr.ACC); err != nil {
			return rec, err
		}
		ok := true
		for {
			a, err := t.next(iec103.TypeDisturbanceValues, iec103.TypeEndOfTransmission)
			if err != nil {
				return rec, err
			}
			v, isValues := a.Body.(iec103.DisturbanceValues)
			if !isValues {
				break
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
			too, failed = iec103.TOOChannelFailed, true
		}
		if err := t.order(true, too, cr.ACC); err != nil {
			return rec, err
		}
	}
	too := iec103.TOODataOK
	if failed {
		too = iec103.TOODataFailed
	}
	if err := t.order(true, too, iec103.ACCGlobal); err != nil {
		return rec, err
	}
	if failed {
		return rec, fmt.Errorf("%w: a channel had missing values", iec103.ErrNegative)
	}
	return rec, nil
}
