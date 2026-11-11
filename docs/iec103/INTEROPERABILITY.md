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
| e2e | `iec103/client` against `iec103/server` over `net.Pipe`, also over a line that drops frames, or one of the two against a bare `link.Primary` or a scripted handler (`iec103/e2e`) |
| fuzz | `FuzzDecode` in `iec103`: decode, encode, decode, encode is stable |
| independent | a passing run against an independent implementation: the reference images of otfabric/iec60870-5-interop (contract 2.1), `make interop-iec103` ([below](#independent-implementations)) |
| recorded | frames of other implementations captured by others ([iec103/testdata/captures](../../iec103/testdata/captures/README.md)): three SEL RTAC serial-line excerpts (level B) decoded, re-encoded byte for byte and compared with Wireshark; not replayed against the client or server |

## Independent implementations

`iec103/interop` (build tag `interop`, `make interop-iec103`) runs this
module against the IEC 103 images of otfabric/iec60870-5-interop
(contract 2.1; local `:dev` builds of branch feat/iec103 at 42164d2, to be
pinned by digest at the harness v0.2.0 release). Expected outcomes are the
harness's own: its case table (`testdata/cases.tsv`), its value and
session checks (`scripts/interop-iec103.sh`) and the fixture the images
print; known bugs of the references are skipped with their id, never
counted as passes.

| Direction | References | Result (2026-11-11, linux/arm64 images) |
|---|---|---|
| reference client against `iec103/server` | vitusdong/iec103-dotnet v0.1.0, masarray/ARIEC103 v1.2.31-3-gec08dd7, XVincentX/Open103 887ff5a5, riclolsen/go-iecp5 v0.4.7 | 9 cases × 4 clients and 6 value/session checks × 4: 51 pass, 0 fail, 9 skipped (8 known client differences, 1 feature the client does not declare: vitusdong measurands) |
| `iec103/client` against a reference server | vitusdong/iec103-dotnet v0.1.0 (the only reference server) | 4 pass (link start-up, clock synchronization, GI termination with the scan number, an idle line of 3 s and a second synchronization), 4 skipped as known server bugs (identification, GI answers, command, refused command) |

The server side has the real evidence: four independent clients (three
independent engines in C#, C++ and Go, and vitusdong, whose test oracle is
a copy of ARIEC103) start the link (reset CU; ARIEC103 adds reset FCB,
Open103 a status of link), decode the identification with COL and
software, run the GI and get every indication with the scan number,
synchronize the clock, poll the class 2 measurands (ARIEC103, Open103,
go-iecp5 decode ASDU 3 and 9 with OV and ER) and get general commands
acknowledged with cause 20/21 and the RII, and the new state in the next GI.

Known differences of the clients, seen on the wire against this server:
ARIEC103 sends ASDU 6 with a CP32Time2a (harness bug
`ariec103-clock-sync-cp32`) and ASDU 20 without the RII (`14 81 14 01 A0
63 02`, `ariec103-command-no-rii`, not yet in the harness); Open103 sends
a general command only for the typical function types of table 17, so
nothing for FUN 160 INF 23 or 99 (`open103-command-typical-fun`, not yet
in the harness). This server drops the short ASDU 6 and ASDU 20 (no
acknowledgement), as their length is not the standard's.

The client side has no conformant reference server: vitusdong answers the
GI with cause 11 and a 12-octet ASDU 1, acknowledges commands with cause
11 or 139 and identifies without COL and software (harness bugs
`vitusdong-gi-cause-and-asdu1`, `vitusdong-command-confirmation`,
`vitusdong-identification`). The client drops these ASDUs as their length
is not the standard's; the suite asserts the octets and skips. What the
client shows against vitusdong: link start-up, addressed clock
synchronization, the GI termination with the scan number, an idle line.

Not exercised by any reference: ASDU 2, 4, 10, 11, 21, 23 to 31 (generic
services, disturbance data), broadcast synchronization, the cause 12 report
after a command (the harness fixture has the station send none), test mode,
DFC.

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
| 7.2.6.28, 7.2.6.29 | the bit after the minutes is RES1 | ignored on decode, written as 0 (`cp` keeps its 101/104 GEN meaning) | unit (`TestTimeRES1`) |
| 7.2.6.32 | GDD: DATASIZE 1..255, NUMBER 1..127; data types 1 to 24 | ranges checked on encode and decode; every data type round-trips | unit (`TestGDDRanges`; `TestGenericDataTypes` for types 5, 6, 8 to 12, 15 to 20), annex (types 1, 3, 7, 14, 21 to 24) |
| 7.2.6.35 | NDE 1..63 | checked on encode and decode; `PackGenericIdent` refuses no elements | unit (`TestNDERange`) |
| 7.3.1 | monitor direction ASDUs 1, 2, 3, 4, 5, 6, 8, 9, 10, 11, 23, 26, 27, 28, 29, 30, 31 | encode and decode; ASDU 9 with more than nine measurands (recorded: 14 and 16) is refused, 7.3.1.8 | unit, annex (10: B.1.2, B.2.2, B.3.2, B.5.2, B.6.2, B.7.2, B.8.2; 11: B.4.2), e2e, recorded, independent (server sends 1, 3, 5, 6, 8, 9, decoded by 4 clients; 3 and 9 by ARIEC103, Open103, go-iecp5) |
| 7.3.2 | control direction ASDUs 6, 7, 10, 20, 21, 24, 25 | encode and decode | unit, annex (21: B.1.1, B.2.1, B.3.1, B.4.1, B.5.1, B.6.1; 10: B.7.1, B.8.1), e2e, recorded (6, 7, 20), independent (server receives 6, 7, 20 from 4 clients; client sends 6, 7, 20 to vitusdong) |
| 7.2 note | private combinations of compatible type and private cause | decoded as `Raw`; `DecodeTyped` to interpret them | unit |

## Clause 7.4: application functions

| Clause | Function | Client | Server | Evidence |
|---|---|---|---|---|
| 7.4.1 | initialization: reset CU or reset FCB, identification with cause 4 or 3 first, then power on (6) or start/restart (5) | `Config.ResetFCB`, `Identification` | built in; `StartCause` | e2e (`TestInitialization`, `TestInitializationResetFCB`, `TestServerLink`), independent (server: identification values checked by vitusdong, ARIEC103, Open103, go-iecp5; client: link start-up with vitusdong, whose identification is a known bug) |
| 7.4.1 | reset CU clears the transmit buffer; reset FCB does not | - | built in | e2e |
| 7.4.1 | re-initialization after no answer | yes (link time-out) | - | e2e (`TestLinkLoss`) |
| 7.4.1 | time synchronization and GI after initialization | yes, unless disabled | - | e2e |
| 7.4.2 | time synchronization, addressed (send/confirm) and broadcast (send/no reply) | `ClockSync`, `BroadcastClockSync` | built in: offset, ASDU 6 answer to an addressed one (to a broadcast only with `AnswerBroadcastSync`) | e2e (`TestTimeSync`, `TestBroadcastSyncAnswer`), independent (addressed: server with vitusdong, Open103, go-iecp5; client with vitusdong) |
| 7.4.2 | the command carries the time its first bit is sent; the equipment corrects by length times bit time | the ASDU 6 is encoded when it is transmitted | `SyncDelay`, default 21 x 11 bits at `BitRate` (9600) | e2e (`TestClockSyncStampedAtSend`, `TestSyncDelay`) |
| 7.4.2 | IV until the first synchronization and after 23 h without | - | `Now`, `SyncTimeout` | e2e (`TestInitializationResetFCB` before the first; `TestSyncTimeout` with a shortened `SyncTimeout`; the 23 h default itself is not run) |
| 7.4.3 | GI with SCN, answers cause 9 with SIN = SCN, end ASDU 8 with SCN | `Interrogate` (drops answers of another SCN) | `AnswerGI` | e2e (`TestGeneralInterrogation`, `TestStaleGIAnswers`), independent (server: all 4 clients, with the scan number and the fixture's DPIs; client: termination with the scan number from vitusdong, its answers are a known bug) |
| 7.4.3 | a GI during a cycle aborts it without termination and starts again | - | a new ASDU 7 drops the queued rest of the cycle | e2e (`TestNewGIAbortsCycle`) |
| 7.4.4 | general command with RII; acknowledgement ASDU 1 cause 20/21 with SIN = RII; change of state with cause 12; one command at a time | `Command` (`ErrBusy`) | `AckCommand`, `Event`; refuses a second command before the first is acknowledged | e2e (`TestCommands`), independent (server: acknowledgement 20/21 with SIN = RII for vitusdong, Open103, go-iecp5; cause 12 not exercised; client: none, vitusdong's acknowledgement is a known bug) |
| 7.4.5 | test mode (cause 7) | cause delivered to the handler | not a feature: the application sends INF 21 and cause 7 | e2e (`TestTestModeAndBlocking`, application-driven) |
| 7.4.6 | monitor direction blocked | - | not a feature: the application sends INF 20 and answers a GI with its termination only | e2e (`TestTestModeAndBlocking`, application-driven) |
| 7.4.7 | disturbance data: list, select, data, tags, channels, acknowledgements, end, updated list | `DisturbanceList`, `Disturbance` (NFE/NOE check, initial tags at position 0, NOC check, negative ack) | built in from a `DisturbanceSource` | e2e (`TestDisturbanceTransfer`, `TestDisturbanceIncomplete`, `TestDisturbanceOrders`, `TestDisturbanceClientAcks`, lossy line) |
| 7.4.7 | ASDU 23 after the start of a GI (cause 31) and after a completed or aborted transfer | - | list with the function type of the equipment after the GI termination, after the final acknowledgement and after TOO 33/34 | e2e (`TestListAfterGIHasEquipmentFun`, `TestDisturbanceAbortByEquipment`) |
| 7.4.7 | abortion by the control system (TOO 3, 9, 17) | not sent | answered with TOO 33, 36, 39 | e2e (`TestDisturbanceOrders`) |
| 7.4.7 | abortion by the equipment (TOO 34, 37, 40); every ASDU 31 acknowledged | ASDU 25 TOO 65, 67, 69; after 37 and 40 the transfer goes on, 34 ends it (`ErrAborted`) | TOO 34 for implausible orders ends the transfer and the list follows; TOO 37 for an unknown channel | e2e (`TestDisturbanceClientAcks`, `TestDisturbanceAbortByEquipment`) |
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
