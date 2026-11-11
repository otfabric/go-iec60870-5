// SPDX-License-Identifier: MIT

// Package e2e runs the IEC 60870-5-103 controlling system (iec103/client)
// against the protection equipment (iec103/server) over in-memory and
// lossy lines, each of them against a hostile raw peer (hostile_test.go),
// and both over a line that drops, duplicates, corrupts, delays, splits,
// truncates and reorders frames (chaos_test.go, make chaos).
package e2e
