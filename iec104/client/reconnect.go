// SPDX-License-Identifier: MIT

package client

import (
	"context"
	"time"

	iec104 "github.com/otfabric/go-iec60870-5/iec104"
)

// reconnectLoop re-establishes a lost connection with exponential backoff
// and equal jitter. It runs until a connection is established or the client
// is closed. The caller has set c.dialing and added to c.wg.
func (c *Client) reconnectLoop() {
	defer c.wg.Done()

	delay := c.opts.reconnect.MinDelay
	timer := time.NewTimer(c.opts.jitter(delay))
	defer timer.Stop()
	for {
		select {
		case <-c.closeCh:
		case <-timer.C:
		}

		var err error
		if !c.isClosed() {
			if err = c.connect(context.Background()); err == nil {
				return
			}
		}
		if c.isClosed() {
			c.mu.Lock()
			c.dialing = false
			c.mu.Unlock()
			c.setState(iec104.StateDisconnected, nil)
			return
		}
		// Still connecting: report the failed attempt.
		c.setState(iec104.StateConnecting, err)
		delay = min(delay*2, c.opts.reconnect.MaxDelay)
		pause := c.opts.jitter(delay)
		c.log.Warn("reconnect failed", "retry_in", pause.String(), "error", err)
		timer.Reset(pause)
	}
}
