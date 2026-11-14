// SPDX-License-Identifier: MIT

// Package callback is how the stations call application code at the
// library boundary. [Call] runs a callback and turns one that does not
// return normally into an error, logged with its stack, so that the
// station that called it stops on its own and the process goes on.
// [Goroutine] tells the goroutine a call comes from, so that a station
// recognises a call made from inside one of its own callbacks.
package callback

import (
	"bytes"
	"fmt"
	"runtime"
	"runtime/debug"
	"strconv"

	"github.com/otfabric/go-iec60870-5/internal/logx"
)

// Goroutine returns the id of the calling goroutine, from the header of its
// stack trace: the runtime has no other way to tell. It is never 0 for a
// running goroutine.
func Goroutine() uint64 {
	var buf [64]byte
	b := buf[:runtime.Stack(buf[:], false)]
	b = bytes.TrimPrefix(b, []byte("goroutine "))
	if i := bytes.IndexByte(b, ' '); i > 0 {
		if id, err := strconv.ParseUint(string(b[:i]), 10, 64); err == nil {
			return id
		}
	}
	return 0
}

// Call runs f, the application callback name. It returns nil when f returns
// normally. Otherwise it logs the failure with the stack of the goroutine
// at that moment and returns an error that names the callback and the
// value it ended with; the caller then stops the station.
func Call(log *logx.Logger, name string, f func()) (err error) {
	normal := false
	defer func() {
		if normal {
			return
		}
		// recover returns nil only while runtime.Goexit ends the goroutine,
		// which nothing can stop.
		if r := recover(); r != nil {
			log.Error("callback did not return normally", "callback", name, "value", fmt.Sprint(r), "stack", string(debug.Stack()))
			err = fmt.Errorf("%s did not return normally: %v", name, r)
		}
	}()
	f()
	normal = true
	return nil
}
