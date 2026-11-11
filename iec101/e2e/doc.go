// SPDX-License-Identifier: MIT

// Package e2e runs the IEC 60870-5-101 client against the server over
// in-memory and lossy transports, in unbalanced and balanced mode, and
// each of them against a hostile raw peer (hostile_test.go) and over a
// line that drops, duplicates, corrupts, delays, splits, truncates and
// reorders frames (chaos_test.go, make chaos).
package e2e
