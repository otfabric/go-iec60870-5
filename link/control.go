// SPDX-License-Identifier: MIT

// Package link implements the link transmission procedures of
// IEC 60870-5-2 over FT 1.2 frames (package ft12): the control field,
// unbalanced transmission with a [Primary] (controlling) station polling
// one or more [Secondary] (controlled) stations, and [Balanced]
// transmission between two combined stations.
//
// It is shared by IEC 60870-5-101, -102 and -103. Those companion
// standards restrict the procedures (which function codes, address sizes,
// single character use); the restrictions are configured by the caller
// through [Config], not built in.
//
// The procedures implemented (IEC 60870-5-2 clause 4):
//
//   - S1 send/no reply: [Primary.SendNoReply], used for broadcast
//   - S2 send/confirm: [Primary.SendConfirm], [Primary.ResetLink],
//     [Primary.ResetUser], [Balanced.Send], [Balanced.Test]
//   - S3 request/respond: [Primary.RequestStatus], [Primary.RequestData]
//
// with the frame count bit (FCB/FCV) protocol: a primary toggles FCB for
// every new message with FCV set and repeats a message with the same FCB
// after a time-out; a secondary that receives a repeated FCB resends its
// previous response without processing the message again.
package link

import "fmt"

// Control is the link control field C.
type Control byte

// Bits of the control field.
const (
	bitDIR Control = 0x80 // balanced: direction; unbalanced: RES (0)
	bitPRM Control = 0x40 // primary message
	bitFCB Control = 0x20 // primary: frame count bit; secondary: ACD
	bitFCV Control = 0x10 // primary: FCB valid; secondary: DFC
)

// Function codes of primary messages (PRM = 1).
const (
	FuncResetLink      byte = 0  // reset of remote link
	FuncResetUser      byte = 1  // reset of user process
	FuncTestLink       byte = 2  // test function for link (balanced)
	FuncUserDataConf   byte = 3  // user data, confirm expected
	FuncUserDataNoRepl byte = 4  // user data, no reply expected
	FuncResetFCB       byte = 7  // reset of frame count bit (IEC 60870-5-103 6.3)
	FuncAccessDemand   byte = 8  // request for access demand
	FuncRequestStatus  byte = 9  // request status of link
	FuncRequestClass1  byte = 10 // request user data class 1
	FuncRequestClass2  byte = 11 // request user data class 2
)

// Function codes of secondary messages (PRM = 0).
const (
	FuncAck            byte = 0  // ACK: positive acknowledgement
	FuncNack           byte = 1  // NACK: message not accepted, link busy
	FuncUserData       byte = 8  // user data
	FuncNoData         byte = 9  // NACK: requested data not available
	FuncStatus         byte = 11 // status of link or access demand
	FuncNotFunctioning byte = 14 // link service not functioning
	FuncNotImplemented byte = 15 // link service not implemented
)

// Primary returns the control field of a primary message.
func PrimaryControl(fn byte, fcb, fcv, dir bool) Control {
	c := bitPRM | Control(fn&0x0F)
	if fcb {
		c |= bitFCB
	}
	if fcv {
		c |= bitFCV
	}
	if dir {
		c |= bitDIR
	}
	return c
}

// SecondaryControl returns the control field of a secondary message.
func SecondaryControl(fn byte, acd, dfc, dir bool) Control {
	c := Control(fn & 0x0F)
	if acd {
		c |= bitFCB
	}
	if dfc {
		c |= bitFCV
	}
	if dir {
		c |= bitDIR
	}
	return c
}

// PRM reports a primary message.
func (c Control) PRM() bool { return c&bitPRM != 0 }

// DIR reports the direction bit (balanced) or RES bit (unbalanced).
func (c Control) DIR() bool { return c&bitDIR != 0 }

// FCB reports the frame count bit of a primary message.
func (c Control) FCB() bool { return c.PRM() && c&bitFCB != 0 }

// FCV reports the frame count bit valid flag of a primary message.
func (c Control) FCV() bool { return c.PRM() && c&bitFCV != 0 }

// ACD reports the access demand bit of a secondary message: class 1 data
// is available.
func (c Control) ACD() bool { return !c.PRM() && c&bitFCB != 0 }

// DFC reports the data flow control bit of a secondary message: further
// messages may overflow the secondary.
func (c Control) DFC() bool { return !c.PRM() && c&bitFCV != 0 }

// Func returns the function code.
func (c Control) Func() byte { return byte(c & 0x0F) }

// String describes the control field, for example "PRM FCB FCV fn=3" or
// "ACD fn=8".
func (c Control) String() string {
	s := ""
	if c.DIR() {
		s += "DIR "
	}
	if c.PRM() {
		s += "PRM "
		if c.FCB() {
			s += "FCB "
		}
		if c.FCV() {
			s += "FCV "
		}
	} else {
		if c.ACD() {
			s += "ACD "
		}
		if c.DFC() {
			s += "DFC "
		}
	}
	return s + fmt.Sprintf("fn=%d", c.Func())
}

// fcvRequired reports whether a primary function code is sent with FCV set
// (IEC 60870-5-2 table 1 and 3).
func fcvRequired(fn byte) bool {
	switch fn {
	case FuncTestLink, FuncUserDataConf, FuncRequestClass1, FuncRequestClass2:
		return true
	}
	return false
}
