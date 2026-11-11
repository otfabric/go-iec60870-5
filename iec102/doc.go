// SPDX-License-Identifier: MIT

// Package iec102 implements the application layer of IEC 60870-5-102, the
// companion standard for the transmission of integrated totals in electric
// power systems: its own ASDU model and codec, the time formats "time a"
// (CP40Time2a) and "time b" (CP56Time2b), and what the controlling and the
// controlled station share ([State], the sentinel errors).
//
// # Sources
//
// The text of IEC 60870-5-102:1996 is not held by this project (see
// docs/STANDARDS.md). Every layout and procedure here is traced to one of
// these sources, and docs/iec102/INTEROPERABILITY.md classifies each type
// identification, cause and field by source and confidence:
//
//   - the Beckhoff "Manual TwinCAT PLC IEC 60870-5-101, -102, -103, -104"
//     (TS650x, version 1.3, 2023), vendor documentation of a commercial
//     IEC 102 master, cited as "Beckhoff TS650x manual, section x": the
//     telegram structure (3.2), the type identifications (4.2, 9.1.10),
//     the read commands (4.22.6 to 4.22.29), the integrated totals and
//     single-point layouts (4.25.1 to 4.25.13), the causes of transmission
//     (9.1.11), the record addresses (9.1.8) and the time formats (9.1.12,
//     9.1.13);
//   - recorded traffic of an Actaris SL762B meter (innoWatt
//     icra-tarifes-optic, GPL-3.0, testdata/innowatt) and the test vectors
//     of gisce/iec870ree (AGPL-3.0, used as observed protocol data only);
//   - the independent implementations gisce/iec870ree and
//     daiiniel/IEC60870-5-102, read for comparison only.
//
// None of them is the standard. Behaviour supported only by them is an
// interpretation.
//
// # ASDU
//
// IEC 60870-5-102 does not use the telecontrol ASDU of IEC 60870-5-101 and
// -104 (package asdu). Its data unit identifier is followed by a record
// address and the information objects (Beckhoff TS650x manual, section
// 3.2):
//
//	type identification | VSQ | cause | common address (1 or 2 octets) | record address
//	information objects ... | [common time tag]
//
// The bodies of this package: [IntegratedTotals] (M_IT_TA_2 ..
// M_IT_TM_2, with the optional signature octet), [SinglePoints]
// (M_SP_TA_2), [ProductSpec] (P_MP_NA_2), [SystemTime] (M_TI_TA_2) and
// [Read] (C_RD_NA_2 .. C_CI_NU_2). Any other type identification, and the
// end of initialization M_EI_NA_2 whose layout no source gives, decodes as
// [Raw], which keeps every octet so that the ASDU encodes back exactly as
// received. A [Profile] interprets the types of a national or vendor
// profile; the Spanish REE profile is package
// [github.com/otfabric/go-iec60870-5/iec102/ree] and is never applied
// unless the caller sets it.
//
// # Link layer
//
// The serial transport is FT 1.2 (package ft12) with fixed and variable
// frames and the single character E5h, and the unbalanced procedures of
// IEC 60870-5-2 (package link) with a link address of 0, 1 or 2 octets
// (Beckhoff TS650x manual, section 3.2). Applications use
// [github.com/otfabric/go-iec60870-5/iec102/client] (the controlling
// station) and [github.com/otfabric/go-iec60870-5/iec102/server] (the
// integrated total station).
package iec102
