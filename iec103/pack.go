// SPDX-License-Identifier: MIT

package iec103

import "fmt"

// genericBudget is the room for data sets in an ASDU 10 (and description
// elements in an ASDU 11, which has two octets of GIN more).
const genericBudget = MaxSize - headerSize - 2

// PackGenericData splits data sets over as many ASDU 10 bodies with the
// same RII as they need: each at most [MaxCounted] sets and [MaxSize]
// octets, COUNT toggling from one to the next starting at 0, CONT set on all
// but the last (clauses 7.2.6.30 and 7.4.8.5, figures 13 to 18). No sets
// give one body with NGD = 0. A data set that does not fit one ASDU fails
// with [ErrTooLong]: splitting one GID over several ASDUs (the CONT bit of
// GDD) is not supported.
func PackGenericData(rii uint8, sets []DataSet) ([]GenericData, error) {
	var out []GenericData
	cur := GenericData{RII: rii}
	size := 0
	for _, s := range sets {
		n := 6 + len(s.Value.Data)
		if n > genericBudget {
			return nil, fmt.Errorf("%w: data set %s of %d octets", ErrTooLong, s.GIN, n)
		}
		if size+n > genericBudget || len(cur.Sets) == MaxCounted {
			out = append(out, cur)
			cur = GenericData{RII: rii}
			size = 0
		}
		cur.Sets = append(cur.Sets, s)
		size += n
	}
	out = append(out, cur)
	for i := range out {
		out[i].NGD = Counted{Count: i%2 == 1, Cont: i < len(out)-1}
	}
	return out, nil
}

// PackGenericIdent splits the description elements of one GIN over as
// many ASDU 11 bodies as they need, like [PackGenericData] with NDE
// (clause 7.2.6.35). NDE counts 1..63 elements, so no elements fail with
// [ErrInvalid].
func PackGenericIdent(rii uint8, gin GIN, elems []DescElement) ([]GenericIdent, error) {
	if len(elems) == 0 {
		return nil, fmt.Errorf("%w: no description elements, NDE counts 1..%d (clause 7.2.6.35)", ErrInvalid, MaxCounted)
	}
	var out []GenericIdent
	cur := GenericIdent{RII: rii, GIN: gin}
	size := 0
	for _, e := range elems {
		n := 4 + len(e.Value.Data)
		if n > genericBudget-2 {
			return nil, fmt.Errorf("%w: description element of %d octets", ErrTooLong, n)
		}
		if size+n > genericBudget-2 || len(cur.Elements) == MaxCounted {
			out = append(out, cur)
			cur = GenericIdent{RII: rii, GIN: gin}
			size = 0
		}
		cur.Elements = append(cur.Elements, e)
		size += n
	}
	out = append(out, cur)
	for i := range out {
		out[i].NDE = Counted{Count: i%2 == 1, Cont: i < len(out)-1}
	}
	return out, nil
}
