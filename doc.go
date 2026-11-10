// SPDX-License-Identifier: MIT

// Package iec60870 is the module root of a pure Go implementation of the
// IEC 60870-5 telecontrol protocol family.
//
// Applications import the subpackages:
//
//   - [github.com/otfabric/go-iec60870-5/asdu]: the application service data
//     unit of IEC 60870-5-101 and IEC 60870-5-104 (the telecontrol companion
//     standards), with configurable field sizes
//   - [github.com/otfabric/go-iec60870-5/cp]: the time tags of
//     IEC 60870-5-4 (CP24Time2a, CP32Time2a, CP56Time2a), shared by the
//     companion standards that use them
//   - [github.com/otfabric/go-iec60870-5/ft12]: the FT 1.2 frame format of
//     IEC 60870-5-1/-2, the serial framing of IEC 60870-5-101, -102 and -103
//   - [github.com/otfabric/go-iec60870-5/iec104]: IEC 60870-5-104 over
//     TCP/IP: APCI framing ([github.com/otfabric/go-iec60870-5/iec104/apci]),
//     the controlling station
//     ([github.com/otfabric/go-iec60870-5/iec104/client]) and the controlled
//     station ([github.com/otfabric/go-iec60870-5/iec104/server])
//   - [github.com/otfabric/go-iec60870-5/sec]: IEC 62351-5 application layer
//     security as profiled by IEC TS 60870-5-7
//
// This package holds what every companion standard shares: the [Logger]
// interfaces.
//
// Docs: README.md, docs/ARCHITECTURE.md, docs/STANDARDS.md,
// docs/ROADMAP.md.
package iec60870
