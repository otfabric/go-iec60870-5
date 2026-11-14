// SPDX-License-Identifier: MIT

package server

import (
	"testing"

	"github.com/otfabric/go-iec60870-5/internal/testutil/leakcheck"
)

// TestMain fails the run when a goroutine of this module is still running
// after the last test.
func TestMain(m *testing.M) { leakcheck.Main(m) }
