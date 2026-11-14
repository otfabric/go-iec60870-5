// SPDX-License-Identifier: MIT

package client

import (
	"testing"

	"github.com/otfabric/go-iec60870-5/internal/testutil/leakcheck"
)

// TestMain fails the run when a test leaves a goroutine of this module
// running.
func TestMain(m *testing.M) { leakcheck.Main(m) }
