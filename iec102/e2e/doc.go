// SPDX-License-Identifier: MIT

// Package e2e runs the IEC 60870-5-102 controlling station of this module
// against its integrated total station over an in-memory line, each of them
// against a hostile raw peer (hostile_test.go), both over a line that
// drops, duplicates, corrupts, delays, splits, truncates and reorders
// frames (chaos_test.go, make chaos), and the client against arbitrary
// answers (FuzzClientAnswers). It is self-consistency, not
// interoperability evidence.
package e2e
