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
//     IEC 60870-5-1, the serial framing of IEC 60870-5-101, -102 and -103
//   - [github.com/otfabric/go-iec60870-5/link]: the link transmission
//     procedures of IEC 60870-5-2 over FT 1.2, unbalanced and balanced
//   - [github.com/otfabric/go-iec60870-5/telecontrol]: the request
//     procedures IEC 60870-5-101 and -104 share
//   - [github.com/otfabric/go-iec60870-5/iec101]: IEC 60870-5-101 over a
//     serial line or byte stream, with a controlling station
//     ([github.com/otfabric/go-iec60870-5/iec101/client]) and a controlled
//     station ([github.com/otfabric/go-iec60870-5/iec101/server])
//   - [github.com/otfabric/go-iec60870-5/iec102]: IEC 60870-5-102 (integrated
//     totals), its own ASDU model, client, server and the opt-in REE profile
//     ([github.com/otfabric/go-iec60870-5/iec102/ree])
//   - [github.com/otfabric/go-iec60870-5/iec103]: IEC 60870-5-103 (protection
//     equipment), its own ASDU model, client and server
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
// Docs: README.md, ARCHITECTURE.md, docs/STANDARDS.md,
// docs/FEATURE-STATUS.md, docs/ROADMAP.md.
package iec60870
