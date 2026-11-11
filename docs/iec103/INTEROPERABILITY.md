# IEC 60870-5-103: implementation and verification

Clause by clause, what `iec103`, `iec103/client` and `iec103/server`
implement and how it is verified. Clause numbers are those of
IEC 60870-5-103:1997, read in its identical adoption GOST R
IEC 60870-5-103-2005.

## Evidence levels

| Level | Meaning |
|---|---|
| unit | tests of the package against octets written from the clause (`iec103/asdu_test.go`, `info_test.go`, `link/ext_test.go`) |
| annex | the octet example of annex B, encoded and decoded |
| e2e | `iec103/client` against `iec103/server` over `net.Pipe`, also over a line that drops frames (`iec103/e2e`) |
| fuzz | `FuzzDecode` in `iec103`: decode, encode, decode, encode is stable |
| interop | a passing run against an independent implementation |
| recorded | frames of other implementations captured by others ([iec103/testdata/captures](../../iec103/testdata/captures/README.md)): three SEL RTAC serial-line excerpts (level B) decoded, re-encoded byte for byte and compared with Wireshark; not replayed against the client or server |

**There is no interop evidence.** Every test runs this library against
itself or against octets written from the standard. The library is not its
own oracle: an e2e pass shows both sides agree, not that they agree with
other implementations. No otfabric 103 interop harness exists yet.

## Clause 6: link layer

| Clause | Requirement | Status | Evidence |
|---|---|---|---|
| 6.1 | FT 1.2 only, fixed and variable frames, single character E5h | implemented (`ft12`; the primary accepts E5h, the server sends fixed frames) | unit (ft12), e2e, recorded |
| 6.1 note 2 | at most 255 octets of user data | `iec103.MaxSize` = 253 (L minus C and A) | unit |
| 6.2.2 | procedures S1, S2, S3 | implemented (`link.Primary`) | e2e |
| 6.2.3 | unbalanced, control system primary; one-octet address; broadcast 255 send/no reply; RES not used | implemented; the address size is forced to one octet | e2e (`TestTimeSync` broadcast), recorded (RTAC primary, several link addresses on one line) |
| 6.2.3 | PRM=1 functions 0, 3, 4, 9, 10, 11 (+7); others answered with 15 | server: `link.FunctionFilter` | unit (`TestSecondaryExtensions`), e2e (`TestServerLink`) |
| 6.2.4 | loop delay, 9.6/19.2 kbit/s | not applicable to the library: `link.Config.Timeout` and the transport | - |
| 6.3 | function 7 reset FCB: next FCV message carries FCB = 1, nothing else reset | `link.Primary.ResetFCB`, `link.Secondary`; the server learns of it through `link.FCBResetHandler` | unit, e2e (`TestInitializationResetFCB`), recorded (the RTAC resets the FCB of each address) |
| 6.3 | functions 14 and 15 toggle the FCB | the secondary advances its expected FCB before answering 15; the primary toggles for every new message | unit (`TestSecondaryExtensions`) |
| 6.3 | DFC while the equipment cannot take commands; a command anyway is answered NACK; broadcast excepted | server sets DFC through `link.FlowControlHandler`, NACK when its queue is full; the client holds back queued ASDUs while DFC is set, broadcasts go out | unit, e2e (`TestServerLink`) |
| 6.3 | DFC at most 15 s | not enforced by the server: the application must keep its handler moving | - |

## Clause 7.1 to 7.3: ASDUs

| Clause | Item | Status | Evidence |
|---|---|---|---|
| 7.1 | one ASDU per frame, one information object | implemented | unit |
| 7.2.1 | type identification 1..31 compatible, 32..255 private | tables 3, 4: typed bodies; others `Raw` | unit, fuzz, recorded (private 45, 46, 205 as `Raw`, byte for byte) |
| 7.2.2 | VSQ: number and SQ | written from the body; received value kept | unit |
| 7.2.3 | causes, tables 5 and 6 | constants and names | unit |
| 7.2.4 | common address, 255 global | client default = link address; server accepts own and 255 | e2e |
| 7.2.5.1 | function type ranges, table 7 | `FunctionType.Compatible`, constants | unit |
| 7.2.5.2 | information numbers, tables 8 to 18 | `StandardInfo`, `Catalog` (extensible for private FUN/INF) | unit |
| 7.2.6.1 to 7.2.6.37 | information elements | all, as fields or types (`MEA`, `GDD`, `Counted` for NGD/NDE, `SOF`, `TOO`, `GIN`, `KOD`, `GRC`, CP32/CP56 via `cp`) | unit, annex |
| 7.3.1 | monitor direction ASDUs 1, 2, 3, 4, 5, 6, 8, 9, 10, 11, 23, 26, 27, 28, 29, 30, 31 | encode and decode; ASDU 9 with more than nine measurands (recorded: 14 and 16) is refused, 7.3.1.8 | unit, annex (10, 11), e2e, recorded (1, 5, 6, 8, 9) |
| 7.3.2 | control direction ASDUs 6, 7, 10, 20, 21, 24, 25 | encode and decode | unit, annex (10, 21), e2e, recorded (6, 7, 20) |
| 7.2 note | private combinations of compatible type and private cause | decoded as `Raw`; `DecodeTyped` to interpret them | unit |

## Clause 7.4: application functions

| Clause | Function | Client | Server | Evidence |
|---|---|---|---|---|
| 7.4.1 | initialization: reset CU or reset FCB, identification with cause 4 or 3 first, then power on (6) or start/restart (5) | `Config.ResetFCB`, `Identification` | built in; `StartCause` | e2e (`TestInitialization`, `TestInitializationResetFCB`, `TestServerLink`) |
| 7.4.1 | reset CU clears the transmit buffer; reset FCB does not | - | built in | e2e |
| 7.4.1 | re-initialization after no answer | yes (link time-out) | - | e2e (`TestLinkLoss`) |
| 7.4.1 | time synchronization and GI after initialization | yes, unless disabled | - | e2e |
| 7.4.2 | time synchronization, addressed (send/confirm) and broadcast (send/no reply) | `ClockSync`, `BroadcastClockSync` | built in: offset, ASDU 6 answer, `SyncDelay` | e2e (`TestTimeSync`) |
| 7.4.2 | IV until the first synchronization and after 23 h without | - | `Now` | e2e |
| 7.4.3 | GI with SCN, answers cause 9 with SIN = SCN, end ASDU 8 with SCN | `Interrogate` (drops answers of another SCN) | `AnswerGI` | e2e (`TestGeneralInterrogation`, `TestStaleGIAnswers`) |
| 7.4.4 | general command with RII; acknowledgement ASDU 1 cause 20/21 with SIN = RII; change of state with cause 12; one command at a time | `Command` (`ErrBusy`) | `AckCommand`, `Event`; refuses a second command before the first is acknowledged | e2e (`TestCommands`) |
| 7.4.5 | test mode (cause 7) | cause delivered to the handler | not a feature: the application sends cause 7 | - |
| 7.4.6 | monitor direction blocked | - | not a feature | - |
| 7.4.7 | disturbance data: list, select, data, tags, channels, acknowledgements, end, updated list | `DisturbanceList`, `Disturbance` (NFE/NOE check, negative ack) | built in from a `DisturbanceSource` | e2e (`TestDisturbanceTransfer`, `TestDisturbanceIncomplete`, `TestDisturbanceOrders`, lossy line) |
| 7.4.7 | abortion by the control system (TOO 3, 9, 17) | not sent | answered with TOO 33, 36, 39 | e2e (`TestDisturbanceOrders`) |
| 7.4.7 | abortion by the equipment (TOO 34, 37, 40) | `ErrAborted` | sent for implausible orders | e2e |
| 7.4.8.5 | read headings (240), read group (241) | `ReadHeadings`, `ReadGroup` | `GenericAnswer` | annex, e2e |
| 7.4.8.6 | read directory (243, ASDU 11), read entry (244), write entry 248/249/250/251 | `ReadDirectory`, `ReadEntry`, `GenericWrite` | `GenericIdentAnswer`, `GenericAnswer` | annex, e2e (`TestGenericServices`) |
| 7.4.8.4 | one generic read and one generic write at a time; RII matching; COUNT/CONT over several ASDUs | `ErrBusy`; collects until CONT = 0 | `PackGenericData`, `PackGenericIdent` | e2e (80 data sets in one read, 70 in one write) |
| 7.4.8.7 | general interrogation of generic data (245) | `GenericInterrogation` | `AnswerGenericGI` | e2e |

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
   server answers both forms with a monitor-direction ASDU 6.
4. **Implausible disturbance orders** (7.4.7): answered with ASDU 31
   TOO 34 (end of disturbance data aborted by the protection equipment),
   also for an unknown FAN.
5. **Private cause with a compatible type** (7.2 note): decoded as `Raw`
   because the note allows another structure; `DecodeTyped` overrides.
6. **"Not used" octets** of ASDUs 23 to 31: written as 0, not checked on
   receive. The INF position is kept in `ASDU.Inf`.
7. **INF 73** (table 12): the text lists type 2, although ASDU 4 carries
   the fault location SCL (7.3.1.4). `StandardInfo` keeps the table as
   printed.
8. **TOV in the selection order** (7.4.7: "not essential"): the client
   sends 1 (instantaneous values) and then the TOV of ASDU 26.
9. **Server time synchronization**: the received time plus
   `Config.SyncDelay` becomes the clock; the manufacturer-specific
   correction of 7.4.2 is reduced to that constant.

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
