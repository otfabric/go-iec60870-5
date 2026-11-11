# Requirements and traceability

Requirements of the shared layers and IEC 60870-5-101, each with its basis
and the tests that verify it. IEC 60870-5-104 requirements are traced in
[docs/iec104/INTEROPERABILITY.md](iec104/INTEROPERABILITY.md) (clause by
clause, with test names and interop cases). "Interpretation" marks a
requirement whose standard text we do not hold ([STANDARDS.md](STANDARDS.md)).

## FT 1.2 framing (`ft12`)

| ID | Requirement | Basis | Tests |
|---|---|---|---|
| FT-1 | Fixed frame 10h C A CS 16h; variable frame 68h L L 68h C A data CS 16h; single character E5h | 103 cl. 6.1 (quoting 60870-5-1 6.2.4.2) | `ft12.TestKnownFrames`, `TestRoundTrip` |
| FT-2 | CS is the sum modulo 256 of C, A and user data; a mismatch rejects the frame | idem | `TestDecodeErrors`, `FuzzDecode` |
| FT-3 | Both length octets equal; L counts C, A and user data; at most 255 | idem, 103 cl. 6.1 note 2 | `TestDecodeErrors`, `TestEncodeErrors` |
| FT-4 | Link address field of 0, 1 or 2 octets (system parameter) | interpretation (101); 103 cl. 6.2.3 fixes 1 | `TestRoundTrip`, `TestEncodeErrors` |
| FT-5 | After an invalid frame the receiver resynchronizes without losing a following valid frame (101:2003 figures 6/9 use a 33-bit line idle; on an io.Reader we hunt for the next start character, a documented deviation) | 101 cl. 6, figures 6 and 9 | `TestDecoderResync` |
| FT-6 | Malformed input never panics | engineering | `FuzzDecode` |

## Link procedures (`link`)

| ID | Requirement | Basis | Tests |
|---|---|---|---|
| LK-1 | Control field: PRM, FCB/FCV (primary), ACD/DFC (secondary), DIR/RES, function code | 103 cl. 6.2 | `link.TestControl` |
| LK-2 | Link initialization: request status of link (9) answered by status (11), then reset of remote link (0) answered by ACK | 103 cl. 6.2.3 | `TestUnbalanced`, `TestBalanced` |
| LK-3 | Primary toggles FCB for each new FCV message; after reset, the first FCV message carries FCB = 1 | 60870-5-2 (interpretation); 103 cl. 6.3 | `TestUnbalanced`, `TestRepetitionNotProcessedTwice` |
| LK-4 | On time-out the primary repeats the identical frame up to the retry limit, then reports the link failed | 103 cl. 6.2.4 (time-out interval) | `TestWrongAddressTimesOut`, `iec101/e2e.TestLinkRecovery` |
| LK-5 | A secondary answers a repeated FCB with its previous response without processing the message again | 60870-5-2 (interpretation) | `TestRepetitionNotProcessedTwice` |
| LK-6 | ACD announces class 1 data; the primary then polls class 1 | 103 cl. 6.2.3 | `TestUnbalanced`, `iec101/e2e.TestServices` |
| LK-7 | A secondary that cannot take user data answers NACK (1) with DFC set | 103 cl. 6.3 | `TestNackAndDFC`, `iec101/e2e.TestServerUnbalancedEdges` |
| LK-8 | Answers 14 (not functioning) and 15 (not implemented) are reported; a secondary answers 15 to undefined functions | 103 cl. 6.3 | `TestPrimaryNegativeAnswers`, `TestSecondaryOddMessages` |
| LK-9 | Broadcast address (all ones) accepts send/no reply only, without answer | 103 cl. 6.2.3 | `TestUnbalanced` |
| LK-10 | E5h may replace ACK and the no-data NACK when ACD and DFC are clear; a primary always accepts it | 103 cl. 6.1; interpretation | `TestUnbalanced` (single char variant) |
| LK-11 | Reset of FCB (function 7), IEC 60870-5-103 only (not a 101 function, 101 table 3) | 103 cl. 6.3 | `TestUnbalanced` |
| LK-12 | Balanced: each station initializes its own direction; DIR set by station A; test function for link | interpretation | `TestBalanced`, `iec101/e2e.TestClientBalancedSupervision` |
| LK-13 | A secondary ignores frames for other addresses and other secondaries' responses | interpretation | `TestWrongAddressTimesOut`, `TestSecondaryOddMessages` |

## IEC 60870-5-101 application (`iec101`, `telecontrol`)

| ID | Requirement | Basis | Tests |
|---|---|---|---|
| 101-1 | ASDU field sizes configurable: COT 1/2, CA 1/2, IOA 1/2/3; ASDU at most one frame | interpretation (101 cl. 7) | `iec101/e2e.TestConfigErrors`, asdu tests |
| 101-2 | Controlled station sends end of initialization after link reset (and after reset of user process, COI remote reset) | 60870-5-5 station initialization (interpretation) | `TestServices`, `TestServerUnbalancedEdges` |
| 101-3 | Interrogation: confirmation, data with the "interrogated by" cause, termination | 104 cl. 9 / 60870-5-5 (held for 104) | `TestServices`, `telecontrol.TestRequests` |
| 101-4 | Select-before-operate: select, execute, deactivate; execute without select refused negatively | idem | `TestServices` |
| 101-5 | Test command C_TS_NA_1 with the fixed test pattern 55AAh; delay acquisition C_CD_NA_1: activation measures, spontaneous sets the delay | 101 cl. 7.2.6.14, 7.3.4.7, 7.4.12 | `TestServices`, `telecontrol.TestRequests` |
| 101-9 | A counter freeze or reset command (FRZ 1..3) is confirmed without readings or termination | 101 cl. 7.4.8, figures 95, 96 | `TestServices`, `telecontrol.TestRequests` |
| 101-6 | Answers are recognised by type, CA, IOA and cause; a termination before the confirmation belongs to an earlier request | 104 practice, carried over | `telecontrol.TestRequests`, `TestMatching` |
| 101-7 | Requests fail promptly while the link is down; the link is re-initialized automatically | engineering | `TestLinkRecovery`, `TestWaitUpTimeout` |
| 101-8 | Undecodable user data is acknowledged at link level and dropped | engineering | `TestServerUnbalancedEdges`, `TestClientBalancedSupervision` |

## IEC 60870-5-103 (`iec103`)

Traced clause by clause in
[iec103/INTEROPERABILITY.md](iec103/INTEROPERABILITY.md). Tests:

| Area | Basis | Tests |
|---|---|---|
| ASDU layouts of every compatible type, private types kept raw | cl. 7.1-7.3 | `iec103.TestCompatibleTypes`, `TestRaw`, `FuzzDecode` |
| Generic services octet examples | annex B.1-B.8 | `iec103.TestAnnexB` |
| Link: FC 7 reset FCB, DFC, FC 15 for unlisted functions with FCB advancing | cl. 6.2.3, 6.3 | `link/ext_test.go` |
| Initialization, identification (ASDU 5), time sync, GI, general commands, generic services, disturbance transfer, lossy line, link loss | cl. 7.4 | `iec103/e2e` (`TestInitialization*`, `TestGeneralInterrogation`, `TestTimeSync`, `TestCommands`, `TestGenericServices`, `TestDisturbance*`, `TestLossyLine`, `TestLinkLoss`, `TestServerLink`) |
| Element ranges and layouts: VSQ, GDD DATASIZE/NUMBER, NDE, DCO and table 17 COM, RES1 of the time tags, generic data types 5, 6, 8-12, 15-20, INF 73 | cl. 7.2.2, 7.2.6.4, 7.2.6.28-7.2.6.35, table 12 | `iec103` (`TestVSQChecked`, `TestGDDRanges`, `TestNDERange`, `TestCommandAllowed`, `TestTimeRES1`, `TestGenericDataTypes`, `TestInf73RelTimeMeasurand`) |
| DFC refusal, new GI/GGI during a cycle, time correction and stamping, IV after SyncTimeout, broadcast sync answer, staged write 248..250, client acknowledgement of every ASDU 31, list after GI and abort, DCO refusal, test mode and blocking (application-driven) | cl. 6.3, 7.4.2-7.4.8 | `iec103/e2e` (`TestDFCRefusesUserData`, `TestDFCHeldBack`, `TestNewGIAbortsCycle`, `TestSyncDelay`, `TestClockSyncStampedAtSend`, `TestSyncTimeout`, `TestBroadcastSyncAnswer`, `TestGenericWriteExecuteSplit`, `TestDisturbanceClientAcks`, `TestDisturbanceAbortByEquipment`, `TestListAfterGIHasEquipmentFun`, `TestCommandDCO`, `TestTestModeAndBlocking`, `TestFunRequired`) |

