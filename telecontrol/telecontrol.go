// SPDX-License-Identifier: MIT

// Package telecontrol holds the application procedures that
// IEC 60870-5-101 and IEC 60870-5-104 share (IEC 60870-5-5 as profiled by
// the two companion standards): how a controlling station recognises the
// answers to its requests, and how it runs the request kinds that are
// answered with a confirmation (commands, clock synchronization), with
// data and a termination (interrogation, counter interrogation) or with
// the data itself (read).
//
// Neither companion standard has a transaction identifier: answers are
// recognised by type identification, common address, information object
// address and cause of transmission. A [Requester] keeps the outstanding
// requests and routes each received ASDU to the request it answers.
//
// The package is transport independent: the companion standard packages
// supply a send function and feed received ASDUs to [Requester.Dispatch].
package telecontrol

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/otfabric/go-iec60870-5/asdu"
)

// ErrBusy reports a request that cannot be issued because an identical one
// is still awaiting its answer: the answers could not be told apart.
var ErrBusy = errors.New("telecontrol: identical request pending")

// NegativeError reports that the controlled station answered a request
// with a negative confirmation or with one of the "unknown ..." causes of
// transmission.
type NegativeError struct {
	// ASDU is the answer of the controlled station.
	ASDU *asdu.ASDU
}

// Error describes the rejection.
func (e *NegativeError) Error() string {
	if e.ASDU == nil {
		return "telecontrol: negative confirmation"
	}
	return fmt.Sprintf("telecontrol: %s rejected by station %d: %s", e.ASDU.Type, e.ASDU.CommonAddr, e.ASDU.Cause)
}

// Cause returns the cause of transmission of the rejection.
func (e *NegativeError) Cause() asdu.Cause {
	if e.ASDU == nil {
		return 0
	}
	return e.ASDU.Cause
}

// Rejected returns a [*NegativeError] for a mirror that refuses its
// request, or nil.
func Rejected(a *asdu.ASDU) error {
	if a.Negative || a.Cause.IsUnknown() {
		return &NegativeError{ASDU: a}
	}
	return nil
}

// Confirmation returns the cause of the positive or negative confirmation
// of req: deactivation confirmation for a deactivation, activation
// confirmation otherwise.
func Confirmation(req *asdu.ASDU) asdu.Cause {
	if req.Cause == asdu.CauseDeactivation {
		return asdu.CauseDeactivationCon
	}
	return asdu.CauseActivationCon
}

// Mirrored reports whether a is the controlled station's mirror of req: a
// confirmation, termination or rejection. p tells the broadcast common
// address.
func Mirrored(p asdu.Params, req, a *asdu.ASDU) bool {
	if a.Type != req.Type {
		return false
	}
	if a.CommonAddr != req.CommonAddr && !p.IsBroadcast(req.CommonAddr) {
		return false
	}
	switch a.Cause {
	case asdu.CauseActivationCon, asdu.CauseActivationTerm:
		if req.Cause == asdu.CauseDeactivation {
			return false
		}
	case asdu.CauseDeactivationCon:
		if req.Cause != asdu.CauseDeactivation {
			return false
		}
	case asdu.CauseUnknownType, asdu.CauseUnknownCause, asdu.CauseUnknownCommonAddr, asdu.CauseUnknownIOA,
		asdu.CauseNotAuthorized:
	default:
		return false
	}
	f, r := a.First(), req.First()
	return f != nil && r != nil && f.Address() == r.Address()
}

// FromStation reports whether a is monitor-direction data from the
// station (or stations) req addressed. Parameters of measured values
// (P_ME_NA_1..P_ME_NC_1) are control-direction types a station also
// reports in answer to an interrogation.
func FromStation(p asdu.Params, req, a *asdu.ASDU) bool {
	if a.Type.InControlDirection() && !interrogatedParameter(a) {
		return false
	}
	return a.CommonAddr == req.CommonAddr || p.IsBroadcast(req.CommonAddr)
}

func interrogatedParameter(a *asdu.ASDU) bool {
	return a.Type >= asdu.P_ME_NA_1 && a.Type <= asdu.P_ME_NC_1 &&
		a.Cause >= asdu.CauseInterrogatedStation && a.Cause <= asdu.CauseInterrogatedGroup16
}

func processData(p asdu.Params, req, a *asdu.ASDU) bool {
	return !a.Type.IsFileTransfer() && FromStation(p, req, a)
}

func sameRequest(a, b *asdu.ASDU) bool {
	fa, fb := a.First(), b.First()
	return a.Type == b.Type && a.CommonAddr == b.CommonAddr && fa != nil && fb != nil && fa.Address() == fb.Address()
}

type exchange struct {
	req   *asdu.ASDU
	match func(a *asdu.ASDU) bool

	mu    sync.Mutex
	items []*asdu.ASDU
	err   error
	sig   chan struct{}
}

func (x *exchange) deliver(a *asdu.ASDU) {
	x.mu.Lock()
	x.items = append(x.items, a)
	x.mu.Unlock()
	x.signal()
}

func (x *exchange) fail(err error) {
	x.mu.Lock()
	if x.err == nil {
		x.err = err
	}
	x.mu.Unlock()
	x.signal()
}

func (x *exchange) signal() {
	select {
	case x.sig <- struct{}{}:
	default:
	}
}

func (x *exchange) next(ctx context.Context) (*asdu.ASDU, error) {
	for {
		x.mu.Lock()
		if len(x.items) > 0 {
			a := x.items[0]
			x.items = x.items[1:]
			x.mu.Unlock()
			return a, nil
		}
		err := x.err
		x.mu.Unlock()
		if err != nil {
			return nil, err
		}
		select {
		case <-x.sig:
		case <-ctx.Done():
			return nil, fmt.Errorf("telecontrol: waiting for answer to %s: %w", x.req.Type, ctx.Err())
		}
	}
}

// Requester runs requests of a controlling station. Its methods are safe
// for concurrent use.
type Requester struct {
	// Params are the ASDU parameters (for the broadcast address).
	Params asdu.Params
	// Send transmits a request. It is called without locks held.
	Send func(ctx context.Context, a *asdu.ASDU) error
	// Timeout bounds every request whose context has no earlier deadline.
	// Zero means no bound beyond the context.
	Timeout time.Duration

	mu        sync.Mutex
	exchanges []*exchange
	testSeq   uint16
}

// Dispatch hands a received ASDU to the requests it answers and reports
// whether one consumed it as a mirror (a confirmation, termination or
// rejection). Process data that answers an interrogation or read is
// delivered to the request and reported as not consumed, so the caller
// passes it on to the application as well.
func (r *Requester) Dispatch(a *asdu.ASDU) (consumed bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, x := range r.exchanges {
		if Mirrored(r.Params, x.req, a) {
			x.deliver(a)
			consumed = true
		} else if x.match != nil && x.match(a) {
			x.deliver(a)
		}
	}
	return consumed
}

// FailAll ends every outstanding request with err, for example when the
// link is lost.
func (r *Requester) FailAll(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, x := range r.exchanges {
		x.fail(err)
	}
}

func (r *Requester) begin(ctx context.Context, req *asdu.ASDU, also func(*asdu.ASDU) bool) (*exchange, error) {
	if req.First() == nil {
		return nil, fmt.Errorf("%w: request without information object", asdu.ErrInvalidValue)
	}
	x := &exchange{req: req, match: also, sig: make(chan struct{}, 1)}
	r.mu.Lock()
	for _, o := range r.exchanges {
		if sameRequest(o.req, req) {
			r.mu.Unlock()
			return nil, fmt.Errorf("%w: %s to %d", ErrBusy, req.Type, req.CommonAddr)
		}
	}
	r.exchanges = append(r.exchanges, x)
	r.mu.Unlock()
	if err := r.Send(ctx, req); err != nil {
		r.end(x)
		return nil, err
	}
	return x, nil
}

func (r *Requester) end(x *exchange) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, o := range r.exchanges {
		if o == x {
			r.exchanges = append(r.exchanges[:i], r.exchanges[i+1:]...)
			return
		}
	}
}

func (r *Requester) bound(ctx context.Context) (context.Context, context.CancelFunc) {
	if r.Timeout <= 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, r.Timeout)
}

// Confirmed sends req and waits for its confirmation. A negative
// confirmation or an "unknown ..." answer is returned as a
// [*NegativeError]. An activation termination that arrives first
// concludes an earlier request for the same object and is skipped.
func (r *Requester) Confirmed(ctx context.Context, req *asdu.ASDU) error {
	ctx, cancel := r.bound(ctx)
	defer cancel()
	x, err := r.begin(ctx, req, nil)
	if err != nil {
		return err
	}
	defer r.end(x)
	for {
		a, err := x.next(ctx)
		if err != nil {
			return err
		}
		if a.Cause == asdu.CauseActivationTerm {
			continue
		}
		return Rejected(a)
	}
}

// Collect sends req and collects the process data the station sends with
// cause until the activation termination of req arrives. On error the
// data collected so far is returned with it.
func (r *Requester) Collect(ctx context.Context, req *asdu.ASDU, cause asdu.Cause) ([]*asdu.ASDU, error) {
	ctx, cancel := r.bound(ctx)
	defer cancel()
	x, err := r.begin(ctx, req, func(a *asdu.ASDU) bool {
		return a.Cause == cause && processData(r.Params, req, a)
	})
	if err != nil {
		return nil, err
	}
	defer r.end(x)
	var data []*asdu.ASDU
	confirmed := false
	for {
		a, err := x.next(ctx)
		if err != nil {
			return data, err
		}
		if a.Type != req.Type {
			data = append(data, a)
			continue
		}
		if err := Rejected(a); err != nil {
			return data, err
		}
		switch {
		case a.Cause != asdu.CauseActivationTerm:
			confirmed = true
		case confirmed:
			return data, nil
		}
	}
}

// Read sends a read command (C_RD_NA_1) for ioa and returns the ASDU the
// station answers with cause "request" for that address.
func (r *Requester) Read(ctx context.Context, ca asdu.CommonAddr, ioa asdu.IOA) (*asdu.ASDU, error) {
	ctx, cancel := r.bound(ctx)
	defer cancel()
	req := asdu.New(asdu.CauseRequest, ca, asdu.Read{IOA: ioa})
	x, err := r.begin(ctx, req, func(a *asdu.ASDU) bool {
		if a.Cause != asdu.CauseRequest || !processData(r.Params, req, a) {
			return false
		}
		for _, o := range a.Objects {
			if o.Address() == ioa {
				return true
			}
		}
		return false
	})
	if err != nil {
		return nil, err
	}
	defer r.end(x)
	for {
		a, err := x.next(ctx)
		if err != nil {
			return nil, err
		}
		if a.Type != req.Type {
			return a, nil
		}
		if err := Rejected(a); err != nil {
			return nil, err
		}
	}
}

// Interrogate runs an interrogation (C_IC_NA_1) with qualifier qoi.
func (r *Requester) Interrogate(ctx context.Context, ca asdu.CommonAddr, qoi asdu.QOI) ([]*asdu.ASDU, error) {
	return r.Collect(ctx, asdu.New(asdu.CauseActivation, ca, asdu.Interrogation{Qualifier: qoi}), qoi.Cause())
}

// CounterInterrogate runs a counter interrogation (C_CI_NA_1).
//
// With freeze [asdu.FreezeRead] it is a counter request: the station
// confirms, sends the integrated totals and terminates, and the totals are
// returned. With a freeze or reset qualifier it is a counter freeze
// command: the station only confirms it and sends no readings
// (IEC 60870-5-101:2003 7.4.8, mode C/D, figures 95 and 96), so the call
// returns at the confirmation with no data; collect the frozen values with
// a following request.
func (r *Requester) CounterInterrogate(ctx context.Context, ca asdu.CommonAddr, request, freeze uint8) ([]*asdu.ASDU, error) {
	obj := asdu.CounterInterrogation{Request: request, Freeze: freeze}
	req := asdu.New(asdu.CauseActivation, ca, obj)
	if freeze != asdu.FreezeRead {
		return nil, r.Confirmed(ctx, req)
	}
	return r.Collect(ctx, req, obj.Cause())
}

// Command sends a process command with cause activation (or deactivation
// when deactivate is set) and waits for its confirmation.
func (r *Requester) Command(ctx context.Context, ca asdu.CommonAddr, obj asdu.InformationObject, deactivate bool) error {
	if obj == nil {
		return fmt.Errorf("%w: nil command", asdu.ErrInvalidValue)
	}
	cause := asdu.CauseActivation
	if deactivate {
		cause = asdu.CauseDeactivation
	}
	req := asdu.New(cause, ca, obj)
	if !req.Type.IsProcessCommand() {
		return fmt.Errorf("%w: %T is not a process command", asdu.ErrTypeMismatch, obj)
	}
	return r.Confirmed(ctx, req)
}

// ClockSync sends a clock synchronization command (C_CS_NA_1).
func (r *Requester) ClockSync(ctx context.Context, ca asdu.CommonAddr, t time.Time) error {
	return r.Confirmed(ctx, asdu.New(asdu.CauseActivation, ca, asdu.ClockSync{Time: asdu.At(t)}))
}

// TestCommand sends a test command. With withTime set it is C_TS_TA_1
// with a fresh test sequence counter and the current time (the only test
// command of IEC 60870-5-104); otherwise C_TS_NA_1 with the fixed test bit
// pattern (IEC 60870-5-101).
func (r *Requester) TestCommand(ctx context.Context, ca asdu.CommonAddr, withTime bool) error {
	obj := asdu.TestCommand{Counter: asdu.TestPattern}
	if withTime {
		r.mu.Lock()
		r.testSeq++
		obj = asdu.TestCommand{Counter: r.testSeq, Time: asdu.Now()}
		r.mu.Unlock()
	}
	return r.Confirmed(ctx, asdu.New(asdu.CauseActivation, ca, obj))
}

// SetDelay sends the transmission delay d to the station: C_CD_NA_1 with
// cause spontaneous ("set delay", IEC 60870-5-101:2003 7.3.4.7 and
// 7.4.12). The station corrects the time of every following clock
// synchronization command by it. No application answer is defined; the
// call returns when the link layer delivered the command. d is sent in
// milliseconds, 0..59999.
func (r *Requester) SetDelay(ctx context.Context, ca asdu.CommonAddr, d time.Duration) error {
	ms := min(max(d.Milliseconds(), 0), 59999)
	return r.Send(ctx, asdu.New(asdu.CauseSpontaneous, ca, asdu.DelayAcquisition{Delay: uint16(ms)}))
}

// MeasureDelay measures the transmission delay to a station with the
// delay acquisition procedure (IEC 60870-5-101:2003 7.4.12): it sends
// C_CD_NA_1 with cause activation carrying the sending time (second and
// millisecond of the minute, CP16Time2a), and the station's confirmation
// carries that time plus its reaction time. The one-way delay is half of
// the round trip less the reaction time.
//
// The formula is that of IEC 60870-5-5 6.13 as IEC 60870-5-101 refers to
// it; we do not hold IEC 60870-5-5, so it is an interpretation. Send the
// result with [Requester.SetDelay].
func (r *Requester) MeasureDelay(ctx context.Context, ca asdu.CommonAddr) (time.Duration, error) {
	ctx, cancel := r.bound(ctx)
	defer cancel()
	start := time.Now()
	sent := (start.Second()*1000 + start.Nanosecond()/int(time.Millisecond)) % 60000
	req := asdu.New(asdu.CauseActivation, ca, asdu.DelayAcquisition{Delay: uint16(sent)})
	x, err := r.begin(ctx, req, nil)
	if err != nil {
		return 0, err
	}
	defer r.end(x)
	for {
		a, err := x.next(ctx)
		if err != nil {
			return 0, err
		}
		if a.Cause == asdu.CauseActivationTerm {
			continue
		}
		if err := Rejected(a); err != nil {
			return 0, err
		}
		roundTrip := time.Since(start)
		var reaction time.Duration
		if o, ok := a.First().(asdu.DelayAcquisition); ok {
			reaction = time.Duration((int(o.Delay)-sent+60000)%60000) * time.Millisecond
		}
		d := (roundTrip - reaction) / 2
		return max(d, 0), nil
	}
}

// ResetProcess sends a reset process command (C_RP_NA_1).
func (r *Requester) ResetProcess(ctx context.Context, ca asdu.CommonAddr, qualifier uint8) error {
	return r.Confirmed(ctx, asdu.New(asdu.CauseActivation, ca, asdu.ResetProcess{Qualifier: qualifier}))
}
