//go:build interop

// SPDX-License-Identifier: MIT

// Package interop runs the IEC 60870-5-102 integrated total station of this
// module (iec102/server with the REE profile of iec102/ree) against the
// independent controlling station published by otfabric/iec60870-5-interop
// (contract 2.2): gisce/iec870ree, a Python master of the Spanish REE
// profile, in a Docker image. The station serves the harness's baseline
// fixture, which the suite reads from the image (print-fixture); every
// result document is checked against the fixture's values, never against
// this module.
//
// There is no open IEC 102 controlled station, so this module's client
// (iec102/client) has no independent peer here; its evidence is the
// recorded meter traffic (docs/iec102/INTEROPERABILITY.md).
//
// A failure that the harness lists as a known bug of gisce
// (testdata/known-bugs.tsv) is skipped with the bug id; it never counts as
// a pass.
//
// Environment:
//
//	IEC102_INTEROP_GISCE_IMAGE  the gisce image; empty skips
//	IEC102_REQUIRE_INTEROP=1    fail instead of skip when Docker or the image is missing
//	IEC102_INTEROP_TRACE=1      log every frame of the station
//
// Run with: make interop-iec102
package interop
