// SPDX-License-Identifier: MIT

package iec103

import "github.com/otfabric/go-iec60870-5/cp"

// Disturbance is one recorded disturbance as the transmission of
// disturbance data carries it (clause 7.4.7): the description of ASDU 26,
// the tags of ASDU 29 and the channels of ASDUs 27 and 30.
type Disturbance struct {
	Fun FunctionType
	TOV uint8
	FAN uint16
	NOF uint16
	// Interval is INT: the time between two values in µs.
	Interval uint16
	// Start is the time tag of the first value (ASDU 26).
	Start cp.Timestamp
	// Tags are the states and changes of the binary signals, in the order
	// of transmission: first the initial states at position 0, then the
	// changes (clause 7.4.7).
	Tags []TagChange
	// Channels are the analogue channels in ascending ACC.
	Channels []Channel
}

// TagChange is the state of a tag from value Pos on (TAP, clause
// 7.2.6.25).
type TagChange struct {
	Pos uint16
	Tag
}

// Channel is one analogue channel of a disturbance record: ASDU 27 and the
// values of its ASDUs 30. All channels of a record have the same number of
// values (NOE, clause 7.2.6.11).
type Channel struct {
	ACC    uint8
	RPV    float32
	RSV    float32
	RFA    float32
	Values []int16
}
