//go:build interop

// SPDX-License-Identifier: MIT

// Package interop runs the IEC 60870-5-101 stations of this module against
// the independent implementations published by otfabric/iec60870-5-interop
// (contract 2.0): this module's client against each reference server, and
// each reference client against this module's server, in unbalanced and
// balanced mode.
//
// The references are Docker images, one per stack. Expected values come
// from the images themselves (print-fixture) and from the harness's case
// table (testdata/cases.tsv), never from this module.
//
// Environment:
//
//	IEC101_INTEROP_<STACK>_IMAGE  image of a stack (LIB60870, CBSIGMA); empty drops it
//	IEC101_INTEROP_ADAPTERS       comma-separated subset of stacks
//	IEC101_REQUIRE_INTEROP=1      fail instead of skip when Docker or an image is missing
//
// Run with: make interop-iec101
package interop
