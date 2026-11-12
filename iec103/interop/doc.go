//go:build interop

// SPDX-License-Identifier: MIT

// Package interop runs the IEC 60870-5-103 stations of this module against
// the independent implementations published by otfabric/iec60870-5-interop
// (contract 2.3; the images of the harness release v0.5.0, pinned by digest
// in harness.go): every reference client against this module's server
// (iec103/server, serving the harness's baseline fixture), and this
// module's client (iec103/client) against the one reference server.
//
// The references are Docker images, one per stack: vitusdong/iec103-dotnet
// (server and client), masarray/ARIEC103, XVincentX/Open103 and
// riclolsen/go-iecp5 (clients only). Expected values come from the images
// themselves (print-fixture) and from the harness's case table and known
// bugs (testdata/cases.tsv, testdata/known-bugs.tsv and
// testdata/known-bugs-consumer.tsv), never from this module. Contract 2.3
// adds the generic services and the transmission of disturbance data
// (services.go, services_test.go).
//
// A pairing whose failure the harness lists as a known bug of the
// reference is skipped with the bug id; it never counts as a pass.
//
// Environment:
//
//	IEC103_INTEROP_<STACK>_IMAGE  image of a stack (VITUSDONG, ARIEC103, OPEN103, GOIECP5); empty drops it
//	IEC103_INTEROP_ADAPTERS       comma-separated subset of stacks
//	IEC103_REQUIRE_INTEROP=1      fail instead of skip when Docker or an image is missing
//
// Run with: make interop-iec103
package interop
