// SPDX-License-Identifier: MIT

package serialline

import (
	"io"
	"slices"
	"sync"
)

// States records the OnState calls of a station: the states in the order
// of the calls, and whether a call began while another had not returned.
// Call Enter first in OnState and defer Leave.
type States[S comparable] struct {
	mu      sync.Mutex
	states  []S
	in      int
	overlap bool
}

// Enter records the start of a call with s.
func (l *States[S]) Enter(s S) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.in++
	l.overlap = l.overlap || l.in > 1
	l.states = append(l.states, s)
}

// Leave records the end of a call.
func (l *States[S]) Leave() {
	l.mu.Lock()
	l.in--
	l.mu.Unlock()
}

// Get returns the states so far, and whether two calls ever overlapped.
func (l *States[S]) Get() ([]S, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.states), l.overlap
}

// NoCloser hides the Close method of a transport: a station on it does not
// wait for its pending Read when it closes.
type NoCloser struct{ io.ReadWriter }
