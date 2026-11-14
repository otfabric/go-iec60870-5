// SPDX-License-Identifier: MIT

package ree

import (
	"context"
	"sync"

	"github.com/otfabric/go-iec60870-5/iec102"
	"github.com/otfabric/go-iec60870-5/iec102/client"
	"github.com/otfabric/go-iec60870-5/iec102/server"
)

// Login starts a session with the access key of the measuring point:
// C_AC_NA_2 with cause 6 (activation), record address 0, confirmed with
// cause 7. The recorded traffic (innoWatt log, Actaris SL762B) starts its
// session this way, and gisce/iec870ree and daiiniel/IEC60870-5-102 do the
// same.
func Login(ctx context.Context, c *client.Client, key uint32) error {
	_, err := c.Request(ctx, iec102.New(iec102.CauseActivation, c.CommonAddr(), 0, AccessKey{Key: key}), client.UntilConfirmation)
	return err
}

// Logout finishes the session: C_FS_NA_2 with cause 6, confirmed with
// cause 7 (gisce/iec870ree, which ignores a missing confirmation).
func Logout(ctx context.Context, c *client.Client) error {
	_, err := c.Request(ctx, iec102.New(iec102.CauseActivation, c.CommonAddr(), 0, Request{T: C_FS_NA_2}), client.UntilConfirmation)
	return err
}

// SetTime sets the station's clock: C_CS_TA_2 with cause 6, confirmed with
// cause 7 (gisce/iec870ree set_datetime).
func SetTime(ctx context.Context, c *client.Client, t iec102.TimeB) error {
	_, err := c.Request(ctx, iec102.New(iec102.CauseActivation, c.CommonAddr(), 0, SetClock{Time: t}), client.UntilConfirmation)
	return err
}

// Session answers the REE session requests on a server: C_AC_NA_2 is
// confirmed (cause 7) when the key matches and refused with the P/N bit
// otherwise, C_FS_NA_2 is confirmed and closes the session, C_CS_TA_2 is
// passed to OnSetTime and confirmed. Every other ASDU goes to Next, or is
// refused with cause 14 when Next is nil. The server does not make the
// standard reads depend on an open session.
//
// That a wrong key is answered with the P/N bit is an interpretation: none
// of our sources shows a refused login. A C_AC_NA_2 with a wrong key also
// closes a session that is open: the session state follows the last
// access key received.
type Session struct {
	// Key is the access key of the measuring point.
	Key uint32
	// OnSetTime receives the time of a C_CS_TA_2; nil ignores it (the
	// request is still confirmed).
	OnSetTime func(t iec102.TimeB)
	// Next handles the ASDUs that are not session requests.
	Next server.Handler

	mu   sync.Mutex
	open bool
}

// Open reports whether a session is open.
func (h *Session) Open() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.open
}

// HandleASDU implements server.Handler.
func (h *Session) HandleASDU(s *server.Server, a *iec102.ASDU) {
	switch b := a.Body.(type) {
	case AccessKey:
		h.mu.Lock()
		h.open = b.Key == h.Key
		ok := h.open
		h.mu.Unlock()
		c := iec102.CauseActivationCon
		if !ok {
			c |= iec102.CauseNegative
		}
		_ = s.Mirror(a, c)
	case Request:
		if b.T != C_FS_NA_2 {
			h.next(s, a)
			return
		}
		h.mu.Lock()
		h.open = false
		h.mu.Unlock()
		_ = s.Mirror(a, iec102.CauseActivationCon)
	case SetClock:
		if h.OnSetTime != nil {
			h.OnSetTime(b.Time)
		}
		_ = s.Mirror(a, iec102.CauseActivationCon)
	default:
		h.next(s, a)
	}
}

func (h *Session) next(s *server.Server, a *iec102.ASDU) {
	if h.Next != nil {
		h.Next.HandleASDU(s, a)
		return
	}
	_ = s.Mirror(a, iec102.CauseUnknownType)
}
