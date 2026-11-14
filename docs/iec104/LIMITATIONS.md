# Limitations: IEC 60870-5-104 (iec104 packages)

What the IEC 60870-5-104 packages (`iec104/...`, with `asdu` and `sec`) do not do, or do only partly. The interoperability list, clause by clause, is in [INTEROPERABILITY.md](INTEROPERABILITY.md).

## Scope not implemented

- **IEC 60870-5-101** is in this module (`iec101`), without application-layer security.
- **File deletion and the query log**: their types are in the codec, with no procedure.
- **IEC 62351-5 / IEC TS 60870-5-7**: no broadcast key, no IEC 62351-8 tokens, no secp256k1 or X448 (mandatory in the TS PICS), no brainpool, no certificate revocation (the four revocation statistics stay 0). See [SECURITY.md](../../SECURITY.md#checked-against-iec-ts-60870-5-72025).
- **Event queue**: in memory. It survives an absent control centre, not a restart of the station.

## Verification

- Independently verified against lib60870-C, OpenMUC j60870, wendy512/iec104, cB-Sigma/iec60870 and pascaldekloe/part5, at the versions and with the results in [INTEROPERABILITY.md](INTEROPERABILITY.md#verification-status).
- IEC 62351-5 was derived from cB-Sigma/iec60870, not from the IEC documents, and is verified against that one implementation only.
- File upload, the file directory, TLS, interrogation groups, a quality on bitstrings and server redundancy against another implementation: the `interop` suite has the tests, for every reference that declares each feature (container contract 1.3). They run against the pinned IEC 104 images of iec60870-5-interop v0.3.0; wendy512 has no TLS, upload or redundancy station, so the client tests skip it there, and part5 has none of these features, so both directions skip it. Redundancy over TLS has no case. See [INTEROPERABILITY.md](INTEROPERABILITY.md#verification-status).
- Tested against recorded traffic of other implementations ([iec104/testdata/captures](../../iec104/testdata/captures/README.md)): software testbeds, generated captures, recordings of unknown origin and one documented physical RTU (MIO8, level B, make and firmware not stated).
- Not verified against certified test equipment or field devices.
- Certification: none.

## Known deviations

Checked clause by clause against IEC 60870-5-104 edition 1 (the only text held), with the result in [INTEROPERABILITY.md](INTEROPERABILITY.md#checked-against-iec-60870-5-104-edition-1). Behaviour of edition 2 (STOPDT con after acknowledgement, closing on an I frame in STOPDT, redundancy groups) is not verifiable from it.

- **Acknowledgements withheld under back-pressure.** While the application lags 256 received events behind, the link sends no S frame after `w` I frames or at `t2`, so that the peer stops at `k`; a lag longer than the peer's `t1` ends the connection.
- **Timers.** Any positive duration and `t3 = 0` are accepted; edition 1 gives 1..255 s in steps of 1 s. The defaults are those of the standard.
- **The `default` redundancy group is not one in the sense of IEC 60870-5-104 edition 2, clause 10 (redundant connections).** Without `WithRedundancyGroups`, every client is an independent session that can be started next to the others: a STARTDT closes nothing, the one that is not active still gets answers, and when the active one stops the queue moves to another started one. A group named with `WithRedundancyGroups` has one started connection: a takeover closes the other.

- The client's activation confirmation wait does not extend to the activation termination of a command; the termination reaches the `Handler`.
- **No transaction identifier.** IEC 60870-5-104 (like IEC 60870-5-101) has none: the client matches an answer to its request by type identification, common address, information object address and the kind of answer (confirmation of an activation or a deactivation, termination). Two identical requests at the same time are refused (`iec104.ErrBusy`), but a confirmation that arrives after its request has timed out completes the next identical request, whose own confirmation then reaches only the `Handler`. A late positive confirmation can so stand in for the negative confirmation of a command issued again; check the station's state when a command timed out before repeating it.
- A received ASDU that cannot be decoded is dropped without a negative answer. The recorded IEC TestServer answers some such requests (a C_RD_NA_1 with an extra octet) with a negative confirmation; the server drops them and serves on, and the client drops such answers (IEC TestServer's C_RP_NA_1 confirmations without the qualifier octet).
- A CP56Time2a keeps only whether its day of week is used (`Timestamp.NoWeekday`); a day of week that does not match the date is encoded again as the date's.
- Reserved bits (in quality descriptors, SCO, protection events) are decoded and written as zero.
