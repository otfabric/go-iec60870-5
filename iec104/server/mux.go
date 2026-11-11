// SPDX-License-Identifier: MIT

package server

import (
	"sync"

	"github.com/otfabric/go-iec60870-5/asdu"
)

// Mux is a [Handler] that routes ASDUs by type identification and answers
// what the application does not serve the way the standard prescribes:
//
//   - a type without a registered handler is mirrored with cause "unknown
//     type identification"
//   - a cause of transmission that is not valid for the type in control
//     direction is mirrored with cause "unknown cause of transmission"
//     (deactivation is valid only for the commands, interrogation,
//     counter interrogation and parameter activation; C_CS_NA_1,
//     C_RP_NA_1, C_TS_TA_1 and P_ME_NA_1..P_ME_NC_1 are activated only,
//     IEC 60870-5-104 7.1, 7.6, 7.9, 7.10, 8.8)
//
// Handlers therefore only see requests they are expected to act on. A
// handler that does not know the addressed object answers with
// [Session.Reject] and [asdu.CauseUnknownIOA].
//
// The zero Mux is ready to use and is safe for concurrent use.
type Mux struct {
	mu       sync.RWMutex
	handlers map[asdu.TypeID]Handler
}

// NewMux returns an empty Mux.
func NewMux() *Mux { return &Mux{} }

// Handle registers h for the given type identifications, replacing any
// earlier registration. A nil h removes it.
func (m *Mux) Handle(h Handler, types ...asdu.TypeID) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.handlers == nil {
		m.handlers = make(map[asdu.TypeID]Handler)
	}
	for _, t := range types {
		if h == nil {
			delete(m.handlers, t)
		} else {
			m.handlers[t] = h
		}
	}
}

// HandleFunc registers f for one type identification.
func (m *Mux) HandleFunc(t asdu.TypeID, f func(s *Session, req *asdu.ASDU)) {
	m.Handle(HandlerFunc(f), t)
}

// ProcessCommands lists the type identifications of the process commands,
// with and without time tag, for registering one handler for all of them
// with [Mux.Handle].
var ProcessCommands = []asdu.TypeID{
	asdu.C_SC_NA_1, asdu.C_DC_NA_1, asdu.C_RC_NA_1, asdu.C_SE_NA_1, asdu.C_SE_NB_1, asdu.C_SE_NC_1, asdu.C_BO_NA_1,
	asdu.C_SC_TA_1, asdu.C_DC_TA_1, asdu.C_RC_TA_1, asdu.C_SE_TA_1, asdu.C_SE_TB_1, asdu.C_SE_TC_1, asdu.C_BO_TA_1,
}

// HandleASDU routes a to its handler or rejects it.
func (m *Mux) HandleASDU(s *Session, a *asdu.ASDU) {
	m.mu.RLock()
	h := m.handlers[a.Type]
	m.mu.RUnlock()
	switch {
	case h == nil:
		_ = s.Reject(a, asdu.CauseUnknownType)
	case !validCause(a):
		_ = s.Reject(a, asdu.CauseUnknownCause)
	case a.First() == nil:
		// Every type a controlling station sends carries an information
		// object; one without any (VSQ of zero) is refused like an
		// unknown type, so no handler has to check for it.
		_ = s.Reject(a, asdu.CauseUnknownType)
	default:
		h.HandleASDU(s, a)
	}
}

// validCause reports whether the cause of transmission of a is one a
// controlling station may use for its type.
//
// Clock synchronization (IEC 60870-5-104 ed. 1, 7.6), reset process (7.1,
// figure 20), test command (7.10, and for C_TS_TA_1 the causes listed in
// 8.8) and the parameters of measured values (7.9) are activated only:
// they have no deactivation.
func validCause(a *asdu.ASDU) bool {
	switch {
	case a.Type == asdu.C_RD_NA_1:
		return a.Cause == asdu.CauseRequest
	case a.Type == asdu.C_CS_NA_1, a.Type == asdu.C_RP_NA_1,
		a.Type == asdu.C_TS_NA_1, a.Type == asdu.C_TS_TA_1,
		a.Type == asdu.P_ME_NA_1, a.Type == asdu.P_ME_NB_1, a.Type == asdu.P_ME_NC_1:
		return a.Cause == asdu.CauseActivation
	case a.Type >= asdu.F_FR_NA_1 || !a.Type.InControlDirection():
		// File transfer and private types: left to the handler.
		return true
	default:
		return a.Cause == asdu.CauseActivation || a.Cause == asdu.CauseDeactivation
	}
}
