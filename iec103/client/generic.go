// SPDX-License-Identifier: MIT

package client

import (
	"context"
	"fmt"

	"github.com/otfabric/go-iec60870-5/iec103"
)

// GenericResult is the answer to a generic service (clause 7.4.8): the
// data sets of every ASDU 10, or the GIN and description elements of every
// ASDU 11, with the same RII, in order.
type GenericResult struct {
	// Cause is 42 (valid data) unless an answer had cause 43 (invalid
	// data, the sets then carry generic reply codes), or the cause of a
	// write answer (40, 41 or 44).
	Cause    iec103.Cause
	Sets     []iec103.DataSet
	GIN      iec103.GIN
	Elements []iec103.DescElement
	// ASDUs are the answers as received.
	ASDUs []*iec103.ASDU
}

func genericMatch(ca uint8, inf iec103.InfoNumber, rii uint8, causes ...iec103.Cause) func(*iec103.ASDU) bool {
	return func(a *iec103.ASDU) bool {
		if a.CommonAddr != ca || a.Fun != iec103.FunGeneric || a.Inf != inf {
			return false
		}
		ok := false
		for _, c := range causes {
			ok = ok || a.Cause == c
		}
		switch b := a.Body.(type) {
		case iec103.GenericData:
			return ok && b.RII == rii
		case iec103.GenericIdent:
			return ok && b.RII == rii
		}
		return false
	}
}

// collect reads the answers of w until one with CONT = 0.
func (c *Client) collect(ctx context.Context, w *waiter, res *GenericResult) error {
	for {
		a, err := c.wait(ctx, w)
		if err != nil {
			return err
		}
		res.ASDUs = append(res.ASDUs, a)
		if res.Cause == 0 || a.Cause != iec103.CauseGenericReadValid {
			res.Cause = a.Cause
		}
		var cont bool
		switch b := a.Body.(type) {
		case iec103.GenericData:
			res.Sets = append(res.Sets, b.Sets...)
			cont = b.NGD.Cont
		case iec103.GenericIdent:
			res.GIN = b.GIN
			res.Elements = append(res.Elements, b.Elements...)
			cont = b.NDE.Cont
		}
		if !cont {
			return nil
		}
	}
}

// GenericRead sends a generic read command (ASDU 21, cause 42) with
// information number inf (240, 241, 243 or 244, table 18) and the given
// generic identifiers, and collects the answers (ASDU 10, or ASDU 11 for
// 243) with the same RII until CONT = 0 (clauses 7.4.8.5 and 7.4.8.6). An
// answer with cause 43 is not an error: Cause reports it and the data sets
// carry the generic reply codes. Clause 7.4.8.4 allows one generic read at
// a time: a second concurrent call returns [iec103.ErrBusy].
func (c *Client) GenericRead(ctx context.Context, inf iec103.InfoNumber, items ...iec103.Item) (*GenericResult, error) {
	switch inf {
	case iec103.InfReadHeadings, iec103.InfReadGroup, iec103.InfReadDirectory, iec103.InfReadEntry:
	default:
		return nil, fmt.Errorf("%w: INF %d is not a generic read", iec103.ErrInvalid, inf)
	}
	if err := lock(&c.readMu); err != nil {
		return nil, err
	}
	defer c.readMu.Unlock()
	ctx, cancel := c.bound(ctx)
	defer cancel()
	rii := c.next()
	w, err := c.request(ctx, iec103.New(iec103.CauseGenericRead, c.ca, iec103.FunGeneric, inf, iec103.GenericCommand{RII: rii, Items: items}),
		genericMatch(c.ca, inf, rii, iec103.CauseGenericReadValid, iec103.CauseGenericReadInvalid))
	if err != nil {
		return nil, err
	}
	defer c.unwatch(w)
	res := &GenericResult{}
	if err := c.collect(ctx, w, res); err != nil {
		return res, fmt.Errorf("iec103: generic read INF %d: %w", inf, err)
	}
	return res, nil
}

// ReadHeadings reads the headings of all defined groups (INF 240, clause
// 7.4.8.5.1): the description of every GIN with entry 0.
func (c *Client) ReadHeadings(ctx context.Context) (*GenericResult, error) {
	return c.GenericRead(ctx, iec103.InfReadHeadings)
}

// ReadGroup reads attribute kod of all entries of group (INF 241, clause
// 7.4.8.5.2).
func (c *Client) ReadGroup(ctx context.Context, group uint8, kod iec103.KOD) (*GenericResult, error) {
	return c.GenericRead(ctx, iec103.InfReadGroup, iec103.Item{GIN: iec103.GIN{Group: group}, KOD: kod})
}

// ReadDirectory reads the directory of one entry: all its attributes (INF
// 243, clause 7.4.8.6.1).
func (c *Client) ReadDirectory(ctx context.Context, gin iec103.GIN) (*GenericResult, error) {
	return c.GenericRead(ctx, iec103.InfReadDirectory, iec103.Item{GIN: gin})
}

// ReadEntry reads attribute kod of entry gin (INF 244, clause 7.4.8.6.2).
func (c *Client) ReadEntry(ctx context.Context, gin iec103.GIN, kod iec103.KOD) (*GenericResult, error) {
	return c.GenericRead(ctx, iec103.InfReadEntry, iec103.Item{GIN: gin, KOD: kod})
}

// GenericWrite sends a generic write command (ASDU 10, cause 40, clause
// 7.4.8.6.3) with information number inf and the data sets, split over
// several ASDUs with the same RII when they need it:
//
//   - 248 (write entry) has no application answer; it returns nil, nil
//     once the link layer confirmed the last ASDU;
//   - 249 (write with confirmation) waits for the echo with cause 44;
//   - 250 (write with execution) waits for cause 40 or 41;
//   - 251 (write abort) waits for cause 40.
//
// Cause 41 returns the answer with an error wrapping [iec103.ErrNegative];
// the data sets of the answer carry the generic reply codes. Clause
// 7.4.8.4 allows one generic write at a time.
func (c *Client) GenericWrite(ctx context.Context, inf iec103.InfoNumber, sets ...iec103.DataSet) (*GenericResult, error) {
	var causes []iec103.Cause
	switch inf {
	case iec103.InfWriteEntry:
	case iec103.InfWriteConfirm:
		causes = []iec103.Cause{iec103.CauseGenericWriteConf, iec103.CauseGenericWriteNack}
	case iec103.InfWriteExecute, iec103.InfWriteAbort:
		causes = []iec103.Cause{iec103.CauseGenericWriteAck, iec103.CauseGenericWriteNack}
	default:
		return nil, fmt.Errorf("%w: INF %d is not a generic write", iec103.ErrInvalid, inf)
	}
	if err := lock(&c.writeMu); err != nil {
		return nil, err
	}
	defer c.writeMu.Unlock()
	ctx, cancel := c.bound(ctx)
	defer cancel()
	rii := c.next()
	bodies, err := iec103.PackGenericData(rii, sets)
	if err != nil {
		return nil, err
	}
	var w *waiter
	if causes != nil {
		if w, err = c.watch(genericMatch(c.ca, inf, rii, causes...)); err != nil {
			return nil, err
		}
		defer c.unwatch(w)
	}
	for _, b := range bodies {
		if err := c.Send(ctx, iec103.New(iec103.CauseGenericWrite, c.ca, iec103.FunGeneric, inf, b)); err != nil {
			return nil, err
		}
	}
	if w == nil {
		return nil, nil
	}
	res := &GenericResult{}
	if err := c.collect(ctx, w, res); err != nil {
		return res, fmt.Errorf("iec103: generic write INF %d: %w", inf, err)
	}
	if res.Cause == iec103.CauseGenericWriteNack {
		return res, fmt.Errorf("%w: generic write INF %d", iec103.ErrNegative, inf)
	}
	return res, nil
}

// GenericInterrogation runs the general interrogation of generic data
// (clause 7.4.8.7): ASDU 21 with INF 245 and cause 9, then collects the
// data sets of the ASDUs 10 with INF 244, cause 9 and the same RII until
// the end (ASDU 10, INF 245, cause 10). Spontaneous generic data in
// between reaches the Handler only.
func (c *Client) GenericInterrogation(ctx context.Context) ([]iec103.DataSet, error) {
	if err := lock(&c.ggiMu); err != nil {
		return nil, err
	}
	defer c.ggiMu.Unlock()
	ctx, cancel := c.bound(ctx)
	defer cancel()
	rii := c.next()
	data := genericMatch(c.ca, iec103.InfReadEntry, rii, iec103.CauseGI)
	end := genericMatch(c.ca, iec103.InfGenericGI, rii, iec103.CauseGITermination)
	w, err := c.request(ctx, iec103.New(iec103.CauseGI, c.ca, iec103.FunGeneric, iec103.InfGenericGI, iec103.GenericCommand{RII: rii}),
		func(a *iec103.ASDU) bool { return data(a) || end(a) })
	if err != nil {
		return nil, err
	}
	defer c.unwatch(w)
	var out []iec103.DataSet
	for {
		a, err := c.wait(ctx, w)
		if err != nil {
			return out, fmt.Errorf("iec103: general interrogation of generic data: %w", err)
		}
		b, _ := a.Body.(iec103.GenericData)
		if a.Inf == iec103.InfGenericGI {
			return out, nil
		}
		out = append(out, b.Sets...)
	}
}
