# IEC 60870-5-103: implementation and verification

Clause by clause, what `iec103`, `iec103/client` and `iec103/server`
implement and how it is verified. Clause numbers are those of
IEC 60870-5-103:1997, read in its identical adoption GOST R
IEC 60870-5-103-2005.

## Evidence levels

| Level | Meaning |
|---|---|
| unit | tests of the package against octets written from the clause (`iec103/asdu_test.go`, `conformance_test.go`, `info_test.go`, `link/ext_test.go`) |
| annex | the octet example of annex B, encoded and decoded |
| e2e | `iec103/client` against `iec103/server` over `net.Pipe`, also over a line that drops frames, or one of the two against a bare `link.Primary`, a scripted handler or a hostile raw peer, and both over a faulty line (`iec103/e2e`) |
| fuzz | `FuzzDecode` in `iec103`: decode, encode, decode, encode is stable; `server.FuzzDispatch` and `client.FuzzReceive` for the stations' receive paths |
| independent | a passing run against an independent implementation: the reference images of otfabric/iec60870-5-interop v0.5.0 (contract 2.3), pinned by digest, `make interop-iec103` ([below](#independent-implementations)) |
| recorded | frames of other implementations captured by others ([iec103/testdata/captures](../../iec103/testdata/captures/README.md)): three SEL RTAC serial-line excerpts (level B) decoded, re-encoded byte for byte and compared with Wireshark; not replayed against the client or server |

## Independent implementations

`iec103/interop` (build tag `interop`, `make interop-iec103`) runs this
module against the IEC 103 images of otfabric/iec60870-5-interop
v0.4.0 (contract 2.3), pinned by multi-arch index digest in
`iec103/interop/harness.go` (`IEC103_INTEROP_<STACK>_IMAGE` selects another
build). Expected outcomes are the
harness's own: its case table (`testdata/cases.tsv`), its value and
session checks (`scripts/interop-iec103.sh`) and the fixture the images
print; known bugs of the references (`testdata/known-bugs.tsv` and
`testdata/known-bugs-consumer.tsv`, the bugs no harness pairing shows; since
harness v0.5.0 an entry may name a role, `<adapter>@client` or
`<adapter>@server`, which the suite checks against its own table of client
bugs and then strips) are skipped with their id, never counted as passes.

| Direction | References | Result (2026-11-11, linux/arm64 images; the whole suite repeated 2026-11-12 on the v0.4.0 images and again on the pinned v0.5.0 images, each time 4 top-level tests, 75 subtests passed, 38 skipped as known bugs or undeclared features, 0 failed) |
|---|---|---|
| reference client against `iec103/server` | vitusdong/iec103-dotnet v0.1.0, masarray/ARIEC103 v1.2.31-3-gec08dd7, XVincentX/Open103 887ff5a5, riclolsen/go-iecp5 v0.4.7 | 9 cases × 4 clients and 6 value/session checks × 4: 53 pass, 0 fail, 7 skipped (6 known client differences, all ARIEC103: 5 `ariec103-command-no-rii`, 1 `ariec103-clock-sync-cp32`; 1 feature the client does not declare: vitusdong measurands). Per client: goiecp5 15/15, Open103 15/15, vitusdong 14 + 1 undeclared, ARIEC103 9 + 6 known |
| `iec103/client` against a reference server | vitusdong/iec103-dotnet v0.1.0 (the only reference server) | 4 pass (link start-up, clock synchronization, GI termination with the scan number, an idle line of 3 s and a second synchronization), 4 skipped as known server bugs (identification, GI answers, command, refused command) |
| contract 2.3, reference client against `iec103/server` (2026-11-11 on the `:dev` images, repeated 2026-11-12 on the pinned v0.4.0 images with the same results, linux/arm64) | vitusdong (generic services, list, transfer), Open103 (transfer); ARIEC103 and go-iecp5 have neither | 7 cases × the clients that declare them and 5 value checks: Open103 `case.disturbance-transfer` pass, `values.disturbance-transfer` description, tags and channel ratings pass, its values a known bug (`open103-asdu30-nfe`); vitusdong `case.disturbance-list` pass, its list values a known bug (`vitusdong-sof-bits`), the 5 generic cases, the transfer and 3 value checks known bugs (`vitusdong-generic-command-inf`, `vitusdong-disturbance-procedure`) |
| contract 2.3, `iec103/client` against a reference server | vitusdong | generic read, generic write and the list: known server bugs (`vitusdong-generic-answer-inf`, `vitusdong-disturbance-list-asdu23`), asserted on the octets; the transfer skipped (vitusdong cannot serve the fixture's records: `disturbanceDataServer` false) |

The server side has the real evidence: four independent clients (three
independent engines in C#, C++ and Go, and vitusdong, whose test oracle is
a copy of ARIEC103) start the link (reset CU; ARIEC103 adds reset FCB,
Open103 a status of link), decode the identification with COL and
software, run the GI and get every indication with the scan number,
synchronize the clock, poll the class 2 measurands (ARIEC103, Open103,
go-iecp5 decode ASDU 3 and 9 with OV and ER) and get general commands
acknowledged with cause 20/21 and the RII, followed by the change of state
with cause 12 (goiecp5, Open103, vitusdong), and the new state in the next GI.

Known differences of the clients, seen on the wire against this server:
ARIEC103 sends ASDU 6 with a CP32Time2a (harness bug
`ariec103-clock-sync-cp32`) and ASDU 20 without the RII (`14 81 14 01 A0
63 02`, harness bug `ariec103-command-no-rii`). This server drops the
short ASDU 6 and ASDU 20 (no acknowledgement), as their length is not the
standard's. Open103 sends a general command only for an INF that table 17
lists as typical for the identified function type (harness bug
`open103-command-typical-fun`); the harness cases now use only such pairs
(160/16..19), so it no longer shows here.

The client side has no conformant reference server: vitusdong answers the
GI with cause 11 and a 12-octet ASDU 1, acknowledges commands with cause
11 or 139 and identifies without COL and software (harness bugs
`vitusdong-gi-cause-and-asdu1`, `vitusdong-command-confirmation`,
`vitusdong-identification`). The client drops these ASDUs as their length
is not the standard's; the suite asserts the octets and skips. What the
client shows against vitusdong: link start-up, addressed clock
synchronization, the GI termination with the scan number, an idle line.

Generic services and disturbance data (contract 2.3). Of the four
references, two have them (the harness's docs/CAPABILITIES.md cites the
sources): vitusdong as master and slave of both (but with INF 0 in every
generic ASDU and a disturbance procedure of its own), Open103 as master of
the disturbance data only; ARIEC103 and go-iecp5 have neither (go-iecp5
names the type identifications only). Open103's control system ran the
whole procedure of clause 7.4.7 against this module's server: it took the
ASDU 23 the server sends after the GI, selected FAN 7 (ASDU 24 TOO 1), got
ASDU 26 and requested the data (TOO 2), got ASDU 28 and requested the tags
(TOO 16), got the ASDUs 29 and the end of the tags and acknowledged them
(ASDU 25 TOO 68), got each channel offered (ASDU 27), requested it (TOO 8),
got its ASDUs 30 and the end of the channel and acknowledged it (TOO 66),
and after the end of the disturbance data acknowledged it (TOO 64). What it
decoded matches the fixture for the description (FAN, NOC, NOE, INT, the
time of the first value), the four tags with their positions and the
ratings of both channels; of the values only the first ASDU 30 of channel 1
is where it belongs, because Open103 reads NFE through an unpacked struct
(known bug `open103-asdu30-nfe`; it stores the first ASDU's 25 values at
0..24, which also shows it numbers the values from 0, as this module does).
So the server's procedure, ASDUs 23, 26 to 29 and 31, and the layout of ASDU
30 apart from NFE are independently evidenced; NFE and the values are not.
The generic services and the disturbance list have no conforming
independent peer: vitusdong's client and server use INF 0 (the client's
ASDU 21 and 10 go unanswered, its server's ASDU 10 do not match the
command), its ASDU 23 has VSQ 81, FUN 255 and TP in bit 8, its client reads
SOF from bits 8 to 5 and acknowledges the whole transfer (TOO 64) right
after ASDU 26. This module's client side of both functions is evidenced by
no reference.

Not exercised by any reference: ASDU 2, 4, 11, the general interrogation of
generic data, reads of headings, groups and directories, writes with
confirmation or abort, aborts of a disturbance transfer, the client's
disturbance transfer, broadcast synchronization (the
contract has `clock-sync --broadcast`, the case table no case with it),
test mode, DFC.
The client's reception of the cause 12 report has no reference server that
sends it (vitusdong does not).

## Clause 6: link layer

| Clause | Requirement | Status | Evidence |
|---|---|---|---|
| 6.1 | FT 1.2 only, fixed and variable frames, single character E5h | implemented (`ft12`; the primary accepts E5h, the server sends fixed frames) | unit (ft12), e2e, recorded |
| 6.1 note 2 | at most 255 octets of user data | `iec103.MaxSize` = 253 (L minus C and A) | unit |
| 6.2.2 | procedures S1, S2, S3 | implemented (`link.Primary`) | e2e, independent (server: 4 clients; client: vitusdong) |
| 6.2.3 | unbalanced, control system primary; one-octet address; broadcast 255 send/no reply; RES not used | implemented; the address size is forced to one octet | e2e (`TestTimeSync` broadcast), recorded (RTAC primary, several link addresses on one line), independent (addressed only: server with 4 clients, client with vitusdong) |
| 6.2.3 | PRM=1 functions 0, 3, 4, 9, 10, 11 (+7); others answered with 15 | server: `link.FunctionFilter` | unit (`TestSecondaryExtensions`), e2e (`TestServerLink`) |
| 6.2.4 | loop delay, 9.6/19.2 kbit/s | not applicable to the library: `link.Config.Timeout` and the transport | - |
| 6.3 | function 7 reset FCB: next FCV message carries FCB = 1, nothing else reset | `link.Primary.ResetFCB`, `link.Secondary`; the server learns of it through `link.FCBResetHandler` | unit, e2e (`TestInitializationResetFCB`), recorded (the RTAC resets the FCB of each address), independent (server: ARIEC103 resets CU then FCB) |
| 6.3 | functions 14 and 15 toggle the FCB | the secondary advances its expected FCB before answering 15; the primary toggles for every new message | unit (`TestSecondaryExtensions`) |
| 6.3 | DFC while the equipment cannot take commands; a command anyway is not processed and answered NACK (function 1); broadcast excepted | server sets DFC through `link.FlowControlHandler` while its handler queue is more than half full and answers user data NACK while DFC is set, broadcasts excepted; the client holds back queued ASDUs while DFC is set, broadcasts go out | unit, e2e (`TestServerLink`, `TestDFCRefusesUserData`, `TestDFCHeldBack`) |
| 6.3 | DFC at most 15 s | not enforced by the server: the application must keep its handler moving | - |
| - | hostile peer: noise, broken and foreign frames, other addresses and the broadcast address, unused functions before the reset, stuck FCB, malformed ASDUs of every length, command floods against a slow handler, half-open line, slow drip, reset CU in the middle of a GI; a hostile equipment against the client (silence, noise, wrong functions, DFC and ACD floods, GI answers without end, acknowledgements of other commands, disturbance data out of order) | the station stays silent or answers as clause 6 says, keeps its queues bounded and serves a peer that behaves afterwards; every request ends at its deadline; a broadcast ends with `iec103.ErrLinkDown` when the link goes down before it was sent | e2e (`iec103/e2e/hostile_test.go`) |
| - | faulty line (drop, duplicate, corrupt, delay, split, truncate, reorder, noise) | no general command executed twice, every request ends, the link comes up again | e2e (`TestChaos`, `make chaos`), fuzz (`server.FuzzDispatch`, `client.FuzzReceive`) |

## Clause 7.1 to 7.3: ASDUs

| Clause | Item | Status | Evidence |
|---|---|---|---|
| 7.1 | one ASDU per frame, one information object | implemented | unit |
| 7.2.1 | type identification 1..31 compatible, 32..255 private | tables 3, 4: typed bodies; others `Raw` | unit, fuzz, recorded (private 45, 46, 205 as `Raw`, byte for byte) |
| 7.2.2 | VSQ: number and SQ | written from the body (81H for one information object, SQ = 0 for ASDUs 3, 9, 23); on decode the number is checked, and 01H (SQ = 0) is accepted for a single object, as ARIEC103 and Wireshark's generator send it (interpretation: robustness) | unit (`TestVSQChecked`), recorded |
| 7.2.3 | causes, tables 5 and 6 | constants and names | unit |
| 7.2.4 | common address, 255 global | client default = link address; server accepts own and 255 | e2e |
| 7.2.5.1 | function type ranges, table 7 | `FunctionType.Compatible`, constants | unit |
| 7.2.5.2 | information numbers, tables 8 to 18 | `StandardInfo`, `Catalog` (extensible for private FUN/INF) | unit |
| 7.2.6.1 to 7.2.6.37 | information elements | all, as fields or types (`MEA`, `GDD`, `Counted` for NGD/NDE, `SOF`, `TOO`, `GIN`, `KOD`, `GRC`, CP32/CP56 via `cp`) | unit, annex |
| 7.2.6.4, table 17 | DCO 1 or 2; COM column: only ON for INF 19, 23 to 26 | `CommandAllowed`; client refuses, server acknowledges negatively | unit (`TestCommandAllowed`), e2e (`TestCommandDCO`), independent (server refuses DCO 3 with cause 21: vitusdong, Open103, go-iecp5) |
| 7.2.5.2, table 17 | ASDU 20 with a compatible function type: only INF 16 to 19, 23 to 26 (32..239 not used in the control direction; the FUN column is typical) | `CommandAllowed`; client refuses (`ErrInvalid`), server acknowledges negatively | unit (`TestCommandAllowed`), e2e (`TestCommandDCO`: INF 27, 99) |
| 7.2.6.28, 7.2.6.29 | the bit after the minutes is RES1 | ignored on decode, written as 0 (`cp` keeps its 101/104 GEN meaning) | unit (`TestTimeRES1`) |
| 7.2.6.32 | GDD: DATASIZE 1..255, NUMBER 1..127; data types 1 to 24 | ranges checked on encode and decode; every data type round-trips | unit (`TestGDDRanges`; `TestGenericDataTypes` for types 5, 6, 8 to 12, 15 to 20), annex (types 1, 3, 7, 14, 21 to 24) |
| 7.2.6.35 | NDE 1..63 | checked on encode and decode; `PackGenericIdent` refuses no elements | unit (`TestNDERange`) |
| 7.3.1 | monitor direction ASDUs 1, 2, 3, 4, 5, 6, 8, 9, 10, 11, 23, 26, 27, 28, 29, 30, 31 | encode and decode; ASDU 3 and 9 with more measurands than 7.3.1.3 and 7.3.1.8 allow (four, nine) are carried up to `MaxMeasurandsWire` (127; recorded: ASDU 9 with 14 and 16), a deviation of the equipment the codec accepts | unit, annex (10: B.1.2, B.2.2, B.3.2, B.5.2, B.6.2, B.7.2, B.8.2; 11: B.4.2), e2e, recorded, independent (server sends 1, 3, 5, 6, 8, 9, decoded by 4 clients; 3 and 9 by ARIEC103, Open103, go-iecp5) |
| 7.3.2 | control direction ASDUs 6, 7, 10, 20, 21, 24, 25 | encode and decode | unit, annex (21: B.1.1, B.2.1, B.3.1, B.4.1, B.5.1, B.6.1; 10: B.7.1, B.8.1), e2e, recorded (6, 7, 20), independent (server receives 6, 7, 20 from 4 clients; client sends 6, 7, 20 to vitusdong) |
| 7.2 note | private combinations of compatible type and private cause | decoded as `Raw`; `DecodeTyped` to interpret them | unit |

## Clause 7.4: application functions

| Clause | Function | Client | Server | Evidence |
|---|---|---|---|---|
| 7.4.1 | initialization: reset CU or reset FCB, identification with cause 4 or 3 first, then power on (6) or start/restart (5) | `Config.ResetFCB`, `Identification` | built in; `StartCause` | e2e (`TestInitialization`, `TestInitializationResetFCB`, `TestServerLink`), independent (server: identification values checked by vitusdong, ARIEC103, Open103, go-iecp5; client: link start-up with vitusdong, whose identification is a known bug) |
| 7.4.1 | reset CU clears the transmit buffer; reset FCB does not | - | built in | e2e |
| 7.4.1 | re-initialization after no answer | yes (link time-out, also when only answers to something else came until the last time-out: `link.ErrUnexpected` wrapping `link.ErrTimeout`); `WaitUp` returns after `OnState` for the change to up returned | - | unit (`link` `TestStrayAnswersAreATimeOut`), e2e (`TestLinkLoss`, `TestWaitUpAfterOnState`) |
| 7.4.1 | time synchronization and GI after initialization | yes, unless disabled | - | e2e |
| 7.4.2 | time synchronization, addressed (send/confirm) and broadcast (send/no reply) | `ClockSync`, `BroadcastClockSync` | built in: offset, ASDU 6 answer to an addressed one (to a broadcast only with `AnswerBroadcastSync`) | e2e (`TestTimeSync`, `TestBroadcastSyncAnswer`), independent (addressed: server with vitusdong, Open103, go-iecp5; client with vitusdong) |
| 7.4.2 | the command carries the time its first bit is sent; the equipment corrects by length times bit time | the ASDU 6 is encoded when it is transmitted | `SyncDelay`, default 21 x 11 bits at `BitRate` (9600) | e2e (`TestClockSyncStampedAtSend`, `TestSyncDelay`) |
| 7.4.2 | IV until the first synchronization and after 23 h without | - | `Now`, `SyncTimeout` | e2e (`TestInitializationResetFCB` before the first; `TestSyncTimeout` with a shortened `SyncTimeout`; the 23 h default itself is not run) |
| 7.4.2 | a synchronization time flagged IV, or outside the years 1970 to 2069 of CP56Time2a (IEC 60870-5-4) | - | not applied: the clock stays; an addressed one is answered with ASDU 6 carrying the station's time with IV set (interpretation 13); the offset is held within ±36525 days | unit (`TestTimeSyncValidity`, `TestTimeSyncRefusedKeepsTheClock`, `TestTimeSyncOffsetClamped`) |
| 7.4.3 | GI with SCN, answers cause 9 with SIN = SCN, end ASDU 8 with SCN | `Interrogate` (drops answers of another SCN) | `AnswerGI` | e2e (`TestGeneralInterrogation`, `TestStaleGIAnswers`), independent (server: all 4 clients, with the scan number and the fixture's DPIs; client: termination with the scan number from vitusdong, its answers are a known bug) |
| 7.4.3 | a GI during a cycle aborts it without termination and starts again | - | a new ASDU 7 drops the queued rest of the cycle | e2e (`TestNewGIAbortsCycle`) |
| 7.4.4 | general command with RII; acknowledgement ASDU 1 cause 20/21 with SIN = RII; change of state with cause 12; one command at a time | `Command` (`ErrBusy`) | `AckCommand`, `Event`; refuses a second command before the first is acknowledged | e2e (`TestCommands`), independent (server: acknowledgement 20/21 with SIN = RII for vitusdong, Open103, go-iecp5; cause 12 after the acknowledgement for vitusdong, Open103, go-iecp5; client: none, vitusdong's acknowledgement is a known bug) |
| 7.4.5 | test mode (cause 7) | cause delivered to the handler | not a feature: the application sends INF 21 and cause 7 | e2e (`TestTestModeAndBlocking`, application-driven) |
| 7.4.6 | monitor direction blocked | - | not a feature: the application sends INF 20 and answers a GI with its termination only | e2e (`TestTestModeAndBlocking`, application-driven) |
| 7.4.7 | disturbance data: list, select, data, tags, channels, acknowledgements, end, updated list | `DisturbanceList`, `Disturbance` (NFE/NOE check, initial tags at position 0, NOC check, negative ack; the record holds at most the NOC channels of NOE values the ASDU 26 announced (7.2.6.10, 7.2.6.11): a value or channel beyond them ends the transfer at once with `ErrInvalid`, no further order sent) | built in from a `DisturbanceSource` | e2e (`TestDisturbanceTransfer`, `TestDisturbanceIncomplete`, `TestDisturbanceOrders`, `TestDisturbanceClientAcks`, `TestDisturbanceWithinAnnounced`, lossy line), independent (server: the whole procedure with Open103, description, tags and ratings as the fixture; the values not, Open103 known bug; client: none) |
| 7.4.7 | ASDU 23 after the start of a GI (cause 31) and after a completed or aborted transfer | - | list with the function type of the equipment after the GI termination, after the final acknowledgement and after TOO 33/34 | e2e (`TestListAfterGIHasEquipmentFun`, `TestDisturbanceAbortByEquipment`), independent (after the GI: Open103 starts its transfer from it) |
| 7.4.7 | abortion by the control system (TOO 3, 9, 17) | not sent | answered with TOO 33, 36, 39 | e2e (`TestDisturbanceOrders`) |
| 7.4.7 | abortion by the equipment (TOO 34, 37, 40); every ASDU 31 acknowledged | ASDU 25 TOO 65, 67, 69; after 37 and 40 the transfer goes on, 34 ends it (`ErrAborted`) | TOO 34 for implausible orders ends the transfer and the list follows; the selection of a record whose NOC (> 255) or NOE (> 65535) does not fit its field is answered with TOO 34; TOO 37 for an unknown channel | e2e (`TestDisturbanceClientAcks`, `TestDisturbanceAbortByEquipment`, `TestDisturbanceTooLarge`) |
| 7.4.8.5 | read headings (240), read group (241) | `ReadHeadings`, `ReadGroup` | `GenericAnswer` | annex, e2e |
| 7.4.8.6 | read directory (243, ASDU 11), read entry (244), write entry 248/249/250/251 | `ReadDirectory`, `ReadEntry`, `GenericWrite` | `GenericIdentAnswer`, `GenericAnswer` | annex, e2e (`TestGenericServices`) |
| 7.4.8.6.3 | write over several ASDUs: INF 248 with CONT = 1, the last INF 250 with CONT = 0, one RII, COUNT toggling (figure 17) | `GenericWrite` (250, 251), `BeginWrite` / `StagedWrite` | application (`GenericAnswer`) | e2e (`TestGenericWriteExecuteSplit`) |
| 7.4.8.4 | one generic read and one generic write at a time; RII matching; COUNT/CONT over several ASDUs | `ErrBusy`; collects until CONT = 0 | `PackGenericData`, `PackGenericIdent` | e2e (80 data sets in one read, 70 in one write) |
| 7.4.8.7 | general interrogation of generic data (245); a new one during a cycle aborts it without its end | `GenericInterrogation` | `AnswerGenericGI`; a new request drops the queued rest | e2e (`TestGenericServices`, `TestNewGIAbortsCycle`) |

## Interpretations

Points where the text leaves a choice; each is also in the doc comment of
the code.

1. **Packed bit string length** (7.2.6.32): DATASIZE counts bits, so the
   GID length is DATASIZE × NUMBER bits rounded up to octets (from the
   "seven and two elements take two octets" example).
2. **Data structure** (type 23): DATASIZE is the total length of the nested
   GDD+GID fields and NUMBER is 1, as annex B.5.2 and B.8.2 show (31 and 34
   octets).
3. **ASDU 6 answer to a broadcast synchronization** (7.4.2, figure 5): the
   broadcast branch of figure 5 says broadcast messages need no answer,
   while the text has a time message sent between the messages before and
   after a synchronization. The server answers an addressed
   synchronization with ASDU 6, a broadcast one only with
   `Config.AnswerBroadcastSync` (default off).
4. **Implausible disturbance orders** (7.4.7): answered with ASDU 31
   TOO 34 (end of disturbance data aborted by the protection equipment),
   also for an unknown FAN.
5. **Private cause with a compatible type** (7.2 note): decoded as `Raw`
   because the note allows another structure; `DecodeTyped` overrides.
6. **"Not used" octets** of ASDUs 23 to 31: written as 0, not checked on
   receive. The INF position is kept in `ASDU.Inf`.
7. **INF 73** (table 12): the text lists type 2, although ASDU 4 is the
   only ASDU that carries the fault location SCL (7.2.6.20, 7.3.1.4).
   `StandardInfo` lists types 2 and 4.
8. **TOV in the selection order** (7.4.7: "not essential"): the client
   sends 1 (instantaneous values) and then the TOV of ASDU 26.
9. **Server time synchronization**: the received time plus
   `Config.SyncDelay` becomes the clock. By default the delay is the
   length of the FT 1.2 frame of the ASDU 6 (21 characters of 11 bits) at
   `Config.BitRate` (9600 bit/s, the lower rate of 6.2.4); internal
   processing delays, which 7.4.2 leaves to the manufacturer, are not
   added.
10. **Implausible order without a transfer in progress** (7.4.7): answered
    with TOO 34 and no updated list, since no transfer was aborted.
11. **Write abort over several ASDUs** (7.4.8.6.3): `GenericWrite` with
    INF 251 and data sets sends them with INF 248 and the 251 last, as for
    250; figure 17 shows only 250.
12. **Numbering of the disturbance values** (7.2.6.9, 7.2.6.25): NFE
    counts the values of a channel from 0, as TAP counts the tag positions
    ("the position of the first tag is zero"). Open103 stores the values of
    an ASDU 30 at NFE onwards from 0 (seen in the interop run); vitusdong
    numbers them from 1 (its source; no run reaches that point).
13. **Time synchronization that cannot be applied** (7.4.2): the clause
    defines no negative answer to ASDU 6. A synchronization whose time is
    flagged invalid (IV), or lies outside the years 1970 to 2069 that
    CP56Time2a carries, is not applied; the server answers an addressed
    one with ASDU 6 carrying its own time with IV set, a broadcast one only
    with `Config.AnswerBroadcastSync`, as for an applied one. The link
    layer still confirms the frame: a negative link acknowledgement would
    say the station is busy, not that the time is wrong.

## Not implemented

- One primary polling several items of equipment on a shared line (the
  client owns its transport and serves one link address).
- Test mode and monitor-direction blocking as server features (7.4.5,
  7.4.6); the application can send the causes and INFs itself.
- Splitting one generic data element over several ASDUs (the CONT bit of
  GDD); `PackGenericData` fails with `ErrTooLong` for a data set larger
  than one ASDU.
- Abortion of a disturbance transfer by the client (TOO 3, 9, 17 are not
  sent; an expired context leaves the transfer to the equipment).
- Enforcement of the 15 s limit of DFC (6.3) and of the 15 min GI interval
  recommendation (7.4.3).
- Character-timing checks of FT 1.2 (see the module LIMITATIONS.md).
