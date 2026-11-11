// SPDX-License-Identifier: MIT

// Package iec103 implements the application layer of IEC 60870-5-103, the
// companion standard for the informative interface of protection equipment:
// its own ASDU model (clause 7.1 to 7.3), the information elements of
// clause 7.2.6, the function type and information number semantics of
// clause 7.2.5 as data, and what the controlling and the controlled station
// share ([State], the sentinel errors).
//
// IEC 60870-5-103 does not use the telecontrol ASDU of IEC 60870-5-101 and
// -104 (package asdu): every field of the data unit identifier is one
// octet, and the information object is addressed by a function type (FUN)
// and an information number (INF) instead of an information object address
// (clause 7.1, figure 2):
//
//	type identification | variable structure qualifier | cause of transmission | common address
//	function type | information number | information elements ... | [CP32Time2a]
//
// Every type identification of the compatible range (clause 7.2.1, tables 3
// and 4) has a [Body] of its own: [TimeTagged] (1), [RelTimeTagged] (2),
// [Measurands] (3 and 9), [RelTimeMeasurand] (4), [Identification] (5),
// [TimeSync] (6), [GIInit] (7), [GITermination] (8), [GenericData] (10),
// [GenericIdent] (11), [GeneralCommand] (20), [GenericCommand] (21),
// [DisturbanceList] (23), [DisturbanceOrder] (24 and 25),
// [DisturbanceReady] (26), [ChannelReady] (27), [TagsReady] (28), [Tags]
// (29), [DisturbanceValues] (30) and [EndOfTransmission] (31). A type
// identification of the private range (32..255), a reserved one, or a
// private cause of transmission (64..255; clause 7.2, note: "other
// combinations may be used in the private range") decodes as [Raw], which
// keeps every octet: such an ASDU encodes back exactly as it was received.
//
// The serial transport is FT 1.2 (package ft12, clause 6.1) and the
// unbalanced link procedures of IEC 60870-5-2 (package link, clause 6.2),
// with a one-octet link address and function code 7 "reset FCB" (clause
// 6.3). Applications use [github.com/otfabric/go-iec60870-5/iec103/client]
// (the controlling system) and [github.com/otfabric/go-iec60870-5/iec103/server]
// (the protection equipment).
//
// Clause numbers in this package refer to IEC 60870-5-103:1997, read in its
// identical Russian adoption GOST R IEC 60870-5-103-2005, which keeps the
// IEC numbering.
package iec103
