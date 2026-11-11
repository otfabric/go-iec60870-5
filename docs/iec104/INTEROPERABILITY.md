# Interoperability: IEC 60870-5-104 (iec104 packages)

What the library implements, following the structure of the
interoperability list in IEC 60870-5-104 clause 9. Use it to compare against
the interoperability list of the device or control centre on the other side.

Legend: **yes** implemented and tested · **raw** carried as undecoded payload
(`ASDU.Raw`) · **no** not implemented · **app** the library carries it, the
behaviour is the application's.

## Checked against IEC 60870-5-104 edition 1

The `iec104` packages (`iec104/apci`, `iec104/internal/link`,
`iec104/client`, `iec104/server`) and `asdu` as IEC 104 uses it were checked
clause by clause on 2026-11-11 against the only text of IEC 60870-5-104 we
hold: GOST R IEC 60870-5-104-2004, the full authentic text of
IEC 60870-5-104:2000 (**edition 1**) in Russian (see
[docs/STANDARDS.md](../STANDARDS.md)). Edition 2 (2006) and Amendment 1
(2016) are **not held**. The code, carried over from go-iec104, was written against
edition 2; where it follows edition 2 and edition 1 is silent or differs,
the row says **ed. 2** (edition 2 behaviour, not verifiable from the held
text) and the code was left as it is.

Verdicts: **conforms** (the code does what the clause says; evidence named)
· **fixed** (a deviation found by this check, fixed with a test that cites
the clause) · **interpretation** (the clause leaves it open, or the scan is
not legible; our reading) · **extension** (the library allows more than the
clause, never by default) · **ed. 2** · **deviation** (documented, not
fixed) · **app** (the clause describes the station application's behaviour;
the library carries it) · **n/a**.

### Clauses 4 and 5: protocol structure and APCI

| Clause | Requirement (paraphrased) | Verdict | Evidence |
|---|---|---|---|
| 4, fig. 2-3 | Application layer over TCP/IP (RFC 793/791); layers 5 and 6 unused | conforms | `client`, `server` on `net` |
| 5, fig. 4-5 | APDU: start octet 68H, length octet, four control octets, ASDU; the APCI alone for control functions | conforms | `apci.Parse`, `apci.ReadFrame`; `TestParseErrors` |
| 5 | The length octet counts the control field and the ASDU, at most 253; ASDU at most 249 | conforms | `apci.MaxAPDULength`, `MaxASDULength`; `TestReadFrameErrors` (254 refused), `TestMarshalErrors` |
| 5 | An I-format APDU always contains an ASDU | **fixed** | an I frame of length 4 was accepted and its empty ASDU dropped later. Now refused on receipt (`apci.ErrInvalidLength`; the connection closes as for any framing error) and on send (`Link.Send` refuses it without ending the link). `TestParseErrors/I_without_ASDU`, `TestMarshalIWithoutASDU`, `TestProtocolViolations/I_frame_without_ASDU`, `TestWriteFailures/empty`; one frame of the recorded Wireshark test capture is now classified so (`internal/capture/oracle_test.go`) |
| 5, fig. 6 | I format: bit 1 of octet 1 = 0; N(S), N(R) of 15 bits; bit 1 of octet 3 = 0 | conforms | `parseBody`; `TestParseErrors/I_odd_N(R)` |
| 5, fig. 7 | S format: octet 1 = 01H, octet 2 = 0, N(R) in octets 3-4 | conforms | `TestParseErrors/S_malformed`, `S_odd_N(R)` |
| 5, fig. 8 | U format: bits 1-2 of octet 1 = 11; TESTFR, STOPDT or STARTDT, act or con; octets 2-4 zero | conforms | `UFunction.Valid`; `TestParseErrors/U_*` |
| 5 | Only one of TESTFR, STOPDT, STARTDT is active at a time | interpretation | read as one function per U frame (enforced above). A TESTFR may be outstanding next to a STARTDT or STOPDT, as in edition 2 implementations |
| 5.1 | N(S)/N(R) as in X.25: both incremented per I frame; N(R) acknowledges every frame before it; the sender keeps frames until acknowledged; both 0 after the TCP connection is established | conforms | `Link.onFrame`, `acknowledge`, `apci.SeqAcks`; `TestStartSendStop`, `TestSendWindowK`, `invariants_test.go` |
| 5.1, fig. 11 | An N(S) out of sequence ends the connection (active close) | conforms | `TestProtocolViolations/wrong_N(S)`, `N(R)_ahead_*` |
| 5.1, fig. 10 | t2: an S frame acknowledges at the latest t2 after the first unacknowledged I frame when no own I frame carries N(R); t2 < t1 | conforms | `TestAckTimerT2`; `Params.Validate` |
| 5.1, fig. 12 | t1: an I frame not acknowledged within t1 ends the connection | conforms | `TestT1UnacknowledgedIFrame` |
| 5.2, fig. 13 | TESTFR act after t3 without frames, in both directions, answered by TESTFR con; every received I, S or U frame restarts t3 | conforms | `Link.onFrame` resets `idleAt`; `TestTestFR`, `TestIdleTestT3`; interop `TestClientIdleTest`, `TestServerSessions` |
| 5.2 | Station B sends no test frames while it receives A's | conforms | a received TESTFR act restarts B's t3, so B never reaches it while A tests at least as often |
| 5.2, fig. 14 | Unconfirmed TESTFR: active close after t1 | conforms | the U frame deadline t1 (`Link.onTimer`) |
| 5.3 | After connection establishment data transfer is stopped; the controlled station sends only U frames and their confirmations | conforms | `link` starts `stopped`; `Session.Send` returns `ErrNotStarted`; `Server.Enqueue` buffers |
| 5.3, fig. 15 | STARTDT act from the controlling station, STARTDT con; unconfirmed: the controlling station closes after t1 | conforms | `client` sends STARTDT after connecting (`WithAutoStart`); `TestT1UnconfirmedStartDT` |
| 5.3 | The controlling station may send commands before STARTDT con | conforms (option not used) | `Client` sends I frames only once started; permitted, not required |
| 5.3 | Send and receive counters continue regardless of STARTDT/STOPDT | conforms | `vs`, `vr` are reset only by a new connection; `TestStartSendStop` |
| 5.3, fig. 16 | STOPDT act; the controlled station stops and answers STOPDT con; outstanding acknowledgements may be sent in between; unconfirmed: close after t1 | conforms | `TestStopDTWaitsForAcknowledgement`, `TestStopDTPendingTimesOut` |
| 5.3 | The controlled station sends STOPDT con only once its I frames are acknowledged ("unconfirmed stopped") | ed. 2 | compatible with edition 1, whose peer acknowledges within t2 anyway |
| 5.3 | An I frame received while data transfer is stopped ends the connection | ed. 2 | edition 1 only says that the controlled station sends none; the state machine of edition 2 (as lib60870-C implements it) closes. `TestProtocolViolations/I_frame_while_stopped` |
| 5.3 | Switch-over: STOPDT on the active connection, STOPDT con, then STARTDT on the other | conforms | `client.Group.Switchover`; interop `TestClientRedundancy` |
| 5.4 | Port 2404 | conforms | `apci.DefaultPort` |
| 5.5 | k: 1..32767; the sender stops at k unacknowledged I frames | conforms | `Params.Validate`; `TestSendWindowK`; interop `TestClientFlowControl` (k = w = 1) |
| 5.5 | w: 1..32767; acknowledgement at the latest after w I frames; w <= 2k/3 only recommended | **fixed** | `Params.Validate` refused w > k, a limit the clause does not have (w relates to the peer's k). Now 1..32767. `TestParamsWIndependentOfK`; `TestServerLifecycle`, `TestConnectErrors` and the e2e option test check w = 32768 instead; `TestAckWindowW` |
| 5.5 | Acknowledgement after w frames and within t2, always | deviation | while the application lags 256 events behind (`link.softQueue`), acknowledgements are withheld so that a conformant peer stops after k frames; if the lag outlasts the peer's t1, the peer closes. Back-pressure instead of an unbounded queue (`TestReceiveBackpressure`) |

### Clause 6: selection of ASDUs (tables 1-6)

| Item | Verdict | Evidence |
|---|---|---|
| Table 1, monitor direction: 1, 3, 5, 7, 9, 11, 13, 15, 20, 21, 30-40 | conforms | `asdu` codec, every type encoded and decoded (`asdu_test.go`, `hardening_test.go`) |
| CP24Time2a types (2, 4, 6, 8, 10, 12, 14, 16-19), C_TS_NA_1 (104) and C_CD_NA_1 (106), which edition 1 leaves out of IEC 104 (struck through in the clause 9 list) | interpretation | decoded on receipt, for tolerance and for IEC 101; never chosen by `asdu.New`, never sent by `client`; a `server.Mux` answers them with cause 44 unless the application registers a handler |
| Table 2: commands 45-51 and 58-64 | conforms | codec; `client.Command`; interop `TestClientCommands` |
| Table 2, note: commands with and without time tag are not mixed towards one station | app | `client.Command` sends what it is given |
| Table 3: M_EI_NA_1 (70) | app | codec; the station application sends it |
| Table 4: 100, 101, 102, 103, 105, 107 | conforms | `client.Interrogate`, `CounterInterrogate`, `Read`, `ClockSync`, `ResetProcess`, `TestCommand` |
| Table 5: 110-113 | conforms | codec; `client.Send`, `server.Mux` |
| Table 6: 120-126 | conforms | codec; `GetFile`, `PutFile`, `ListFiles`, `FileServer` |
| Type 127 (F_SC_NB_1, query log) | ed. 2 | reserved in edition 1, table 6; codec only |

### Clause 7: application functions over TCP

| Clause | Function | Verdict | Evidence |
|---|---|---|---|
| 7 | Controlling station = client, controlled station = server | conforms | packages `client`, `server` |
| 7.1, fig. 17-19 | The controlling station establishes the connection, retries, either side closes | conforms | `client` with `WithReconnect` (t0 bounds each attempt); `server` listens |
| 7.1, fig. 20 | Remote initialization: C_RP_NA_1 act/actcon, the controlled station closes, the controlling station reconnects | conforms / app | `client.ResetProcess` and reconnect; closing is the station application's (`Session.Close`) |
| 7.2 | Read: C_RD, data answered with cause 5 | conforms | `client.Read`; interop `TestClientRead`, server scenarios |
| 7.3, 7.4 | Cyclic and spontaneous transmission | app | `Server.Enqueue`, `Session.Send` |
| 7.5 | Station interrogation: C_IC act, actcon, data, actterm | conforms | `client.Interrogate`; interop `TestClientInterrogation`, `TestClientGroups` |
| 7.6 | Clock synchronization C_CS act/actcon (optional); the actcon carries the station's time before synchronization and follows the stored time-tagged data; time tags flagged until synchronized | conforms / app | `client.ClockSync`; the content and ordering of the confirmation are the station application's |
| 7.7 | Commands: select, break (deact), execute, actcon, deactcon, actterm, return information | conforms | `client.Command`, `Deactivate`; interop `TestClientSelectBeforeOperate`. The client's wait ends at actcon (Known deviations) |
| 7.8 | Counter interrogation: C_CI act/actcon, deact/deactcon, M_IT, actterm | conforms | `client.CounterInterrogate`; `server.Mux` passes a C_CI deactivation |
| 7.9 | Parameter loading: P_ME act/actcon (and spontaneous), P_AC act/actcon | **fixed** | `server.Mux` passed a deactivation of P_ME_NA/NB/NC_1 to the handler; now refused with cause 45. `TestMuxActivationOnlyTypes` |
| 7.6, 7.10, 7.1 / 8.8 | Clock synchronization, test command and reset process are activated only; for C_TS_TA_1, 8.8 lists cause 6 alone in control direction | **fixed** | `server.Mux` passed a deactivation of C_CS_NA_1, C_TS_TA_1 and C_RP_NA_1 to the handler; now refused with cause 45. `TestMuxActivationOnlyTypes` |
| 7.11 | File transfer in both directions, directory | conforms | `GetFile`, `PutFile`, `ListFiles`, `FileServer`; interop `TestClientFileTransfer`, `TestClientUpload`, `TestClientDirectory`, server tests |

### Clause 8: commands with time tag

| Clause | Requirement | Verdict | Evidence |
|---|---|---|---|
| 8.1-8.7 | C_SC_TA_1 .. C_BO_TA_1: the command element plus CP56Time2a, the time of initiation | conforms | codec; interop `TestClientCommands` where the reference declares `timeTaggedCommands` |
| 8 | A station may act on a command older than a system parameter allows | app | the handler checks the age |
| 8.8 | C_TS_TA_1: TSC (16 bits) and CP56Time2a | conforms | codec |
| 8.8 | The TSC starts at 0 | **fixed** | `client.TestCommand` sent 1 first; now 0, 1, 2, ... `TestTestCommandCounterStartsAtZero` |

### Clause 9: interoperability list

| Clause | Item | Verdict | Evidence |
|---|---|---|---|
| 9.1 | Controlling and controlled station | conforms | both packages |
| 9.2-9.4 | Network configuration, physical and link layer (IEC 101) | n/a | TCP/IP |
| 9.5 | Transmission mode 1 (least significant octet first) | conforms | `encoding/binary.LittleEndian` throughout `asdu` |
| 9.5 | Common address 2 octets, information object address 3 octets, cause 2 octets with originator address (0 when unused) | conforms | `asdu.IEC104`, the default of both packages; `client.WithOriginator` |
| 9.5 | Other field sizes | extension | `WithASDUParams`, for non-conformant devices only |
| 9.5 | Maximum APDU length 253, may be reduced for a system | conforms (send side) | `asdu.Params.MaxSize` limits what is encoded; any received ASDU up to 249 octets is accepted |
| 9.5 | Matrix of type identifications and causes of transmission | interpretation | the scan is legible only in part. Control direction: `server.Mux` accepts activation and deactivation, a read only with cause 5, the activation-only types only with 6, file transfer and private types as the handler decides. Monitor direction: causes are not checked on receipt |
| 9.6 | Timeouts: defaults t0 30 s, t1 15 s, t2 10 s, t3 20 s | conforms | `apci.DefaultParams`, `TestParams` |
| 9.6 | Timeouts range 1..255 s in steps of 1 s | extension | `Params.Validate` accepts any positive duration (shorter values for tests and peers configured so) and `T3 = 0` (no idle test) |
| 9.6 | k default 12, w default 8 | conforms | `apci.DefaultParams` |
| 9.6 | Port 2404 in all cases | conforms | `apci.DefaultPort`; another port can be configured |

Not part of edition 1 at all: redundant connections (clause 10 of
edition 2, see [Redundancy](#redundancy)), type 127, TLS (IEC 62351-3) and
the application-layer security with types 81-91 and causes 14-17
(IEC TS 60870-5-7).

Result: 63 rows. 44 conform (two of them with part of the behaviour left
to the station application), 4 are the application's, 5 deviations were
fixed in this check, 3 follow edition 2, 3 are interpretations, 2 are
extensions beyond the clause, 1 is a documented deviation and 1 does not
apply. No requirement of edition 1 is left unimplemented by the library; the
application functions marked "app" are the station's to provide.

### PICS summary (clause 9)

| Item | go-iec60870-5 |
|---|---|
| Station | controlling (`client`) and controlled (`server`); not combined on one connection |
| Field sizes | CA 2, IOA 3 (unstructured), COT 2 with originator |
| Maximum APDU length | 253 (ASDU 249), lower on send with `asdu.Params.MaxSize` |
| Monitor direction (X = standard, R = reverse, B = both) | X: 1, 3, 5, 7, 9, 11, 13, 15, 20, 21, 30-40 |
| Control direction | X: 45-51, 58-64 |
| System information | X: 70 (monitor), 100, 101, 102, 103, 105, 107 (control) |
| Parameters | X: 110-113 |
| File transfer | B: 120-125; X: 126 (monitor direction) |
| Basic functions | remote initialization, cyclic and spontaneous transmission, read, station interrogation and groups 1-16, clock synchronization, direct and select-before-operate commands and set-points, counter interrogation (modes A-D are the application's; general and groups 1-4, freeze with and without reset), parameter loading and activation, test procedure, transparent files in both directions |
| Timeouts | t0 30 s, t1 15 s, t2 10 s, t3 20 s by default; configurable |
| k, w | 12, 8 by default; 1..32767 each |
| Port | 2404 (19998 with TLS, outside edition 1) |

## System and network

| Item | Support |
|------|---------|
| Controlling station (master) | yes, `client` |
| Controlled station (slave) | yes, `server` |
| Combined station (both directions on one connection) | no; run a client and a server side by side |
| Transport | TCP, IPv4 and IPv6 |
| Port | 2404 by default, any |
| TLS (IEC 62351-3) | yes, standard `crypto/tls`, default port 19998 |
| IEC 62351-5 application-layer security (IEC TS 60870-5-7) | yes, the A-Profile of the 2023 edition, package `sec`, both roles. See [Application-layer security](#application-layer-security) |
| Redundant connections (redundancy groups) | yes, both roles. See [Redundancy](#redundancy) |

## Application layer framing

Fixed by IEC 60870-5-104 and the defaults of `asdu.IEC104`:

| Field | Size |
|-------|------|
| Cause of transmission | 2 octets (with originator address) |
| Common address of ASDU | 2 octets |
| Information object address | 3 octets |
| Maximum APDU length | 253 octets (ASDU up to 249) |
| Time tags | CP56Time2a, UTC by default (`asdu.Params.In` for another zone) |

Other field sizes (1-octet cause, 1-octet common address, 1- or 2-octet
object address) are supported by the codec for IEC 60870-5-101 use and for
non-conformant devices (`WithASDUParams`).

Both SQ = 0 (addressed objects) and SQ = 1 (sequence of elements) are
decoded for every type. Encoding uses SQ = 1 only when `ASDU.Sequence` is set.

## APCI

| Item | Support |
|------|---------|
| I, S and U formats | yes |
| STARTDT, STOPDT, TESTFR | yes, both roles |
| `k`: maximum unacknowledged I frames sent | yes, 1..32767, default 12 |
| `w`: latest acknowledgement after I frames received | yes, 1..32767 independent of `k`, default 8 (ed. 1, 5.5 recommends w <= 2k/3) |
| `t0`: connection establishment | yes, default 30s (client) |
| `t1`: send or test APDU timeout | yes, default 15s |
| `t2`: acknowledgement timeout, `t2 < t1` | yes, default 10s |
| `t3`: idle test timeout | yes, default 20s, 0 disables (extension: ed. 1 clause 9 gives 1..255 s for every timer) |
| Sequence number validation | yes; a violation closes the connection |

The parameters are not negotiated: configure both sides alike. See
[ARCHITECTURE.md](ARCHITECTURE.md#conformance-decisions) for the behaviour in
each exceptional situation.

## Type identifications

### Process information in monitor direction

| Type | ID | Description | Support | Go type |
|------|----|-------------|---------|---------|
| `M_SP_NA_1` | 1 | Single-point information | yes | `SinglePoint` |
| `M_SP_TA_1` | 2 | … with CP24Time2a ¹ | yes | `SinglePoint` |
| `M_DP_NA_1` | 3 | Double-point information | yes | `DoublePoint` |
| `M_DP_TA_1` | 4 | … with CP24Time2a ¹ | yes | `DoublePoint` |
| `M_ST_NA_1` | 5 | Step position information | yes | `StepPosition` |
| `M_ST_TA_1` | 6 | … with CP24Time2a ¹ | yes | `StepPosition` |
| `M_BO_NA_1` | 7 | Bitstring of 32 bit | yes | `Bitstring32` |
| `M_BO_TA_1` | 8 | … with CP24Time2a ¹ | yes | `Bitstring32` |
| `M_ME_NA_1` | 9 | Measured value, normalized | yes | `MeasuredNormalized` |
| `M_ME_TA_1` | 10 | … with CP24Time2a ¹ | yes | `MeasuredNormalized` |
| `M_ME_NB_1` | 11 | Measured value, scaled | yes | `MeasuredScaled` |
| `M_ME_TB_1` | 12 | … with CP24Time2a ¹ | yes | `MeasuredScaled` |
| `M_ME_NC_1` | 13 | Measured value, short floating point | yes | `MeasuredFloat` |
| `M_ME_TC_1` | 14 | … with CP24Time2a ¹ | yes | `MeasuredFloat` |
| `M_IT_NA_1` | 15 | Integrated totals | yes | `IntegratedTotal` |
| `M_IT_TA_1` | 16 | … with CP24Time2a ¹ | yes | `IntegratedTotal` |
| `M_EP_TA_1` | 17 | Event of protection equipment with CP24Time2a ¹ | yes | `ProtectionEvent` |
| `M_EP_TB_1` | 18 | Packed start events of protection equipment with CP24Time2a ¹ | yes | `ProtectionStart` |
| `M_EP_TC_1` | 19 | Packed output circuit information with CP24Time2a ¹ | yes | `ProtectionOutput` |
| `M_PS_NA_1` | 20 | Packed single-point information with status change detection | yes | `PackedSinglePoints` |
| `M_ME_ND_1` | 21 | Measured value, normalized without quality descriptor | yes | `MeasuredNormalized` |
| `M_SP_TB_1` | 30 | Single-point information with CP56Time2a | yes | `SinglePoint` |
| `M_DP_TB_1` | 31 | Double-point information with CP56Time2a | yes | `DoublePoint` |
| `M_ST_TB_1` | 32 | Step position information with CP56Time2a | yes | `StepPosition` |
| `M_BO_TB_1` | 33 | Bitstring of 32 bit with CP56Time2a | yes | `Bitstring32` |
| `M_ME_TD_1` | 34 | Measured value, normalized with CP56Time2a | yes | `MeasuredNormalized` |
| `M_ME_TE_1` | 35 | Measured value, scaled with CP56Time2a | yes | `MeasuredScaled` |
| `M_ME_TF_1` | 36 | Measured value, short floating point with CP56Time2a | yes | `MeasuredFloat` |
| `M_IT_TB_1` | 37 | Integrated totals with CP56Time2a | yes | `IntegratedTotal` |
| `M_EP_TD_1` | 38 | Event of protection equipment with CP56Time2a | yes | `ProtectionEvent` |
| `M_EP_TE_1` | 39 | Packed start events of protection equipment with CP56Time2a | yes | `ProtectionStart` |
| `M_EP_TF_1` | 40 | Packed output circuit information with CP56Time2a | yes | `ProtectionOutput` |

¹ Defined by IEC 60870-5-101 and not part of the IEC 60870-5-104 subset.
Supported by the codec so that the `asdu` package is complete for
IEC 60870-5-101 and tolerant of devices that send them over TCP. `asdu.New`
never selects these types; set `ASDU.Type` explicitly.

### Process information in control direction

| Type | ID | Description | Support | Go type |
|------|----|-------------|---------|---------|
| `C_SC_NA_1` | 45 | Single command | yes | `SingleCommand` |
| `C_DC_NA_1` | 46 | Double command | yes | `DoubleCommand` |
| `C_RC_NA_1` | 47 | Regulating step command | yes | `StepCommand` |
| `C_SE_NA_1` | 48 | Set-point command, normalized | yes | `SetpointNormalized` |
| `C_SE_NB_1` | 49 | Set-point command, scaled | yes | `SetpointScaled` |
| `C_SE_NC_1` | 50 | Set-point command, short floating point | yes | `SetpointFloat` |
| `C_BO_NA_1` | 51 | Bitstring of 32 bit | yes | `BitstringCommand` |
| `C_SC_TA_1` | 58 | Single command with CP56Time2a | yes | `SingleCommand` |
| `C_DC_TA_1` | 59 | Double command with CP56Time2a | yes | `DoubleCommand` |
| `C_RC_TA_1` | 60 | Regulating step command with CP56Time2a | yes | `StepCommand` |
| `C_SE_TA_1` | 61 | Set-point command, normalized with CP56Time2a | yes | `SetpointNormalized` |
| `C_SE_TB_1` | 62 | Set-point command, scaled with CP56Time2a | yes | `SetpointScaled` |
| `C_SE_TC_1` | 63 | Set-point command, short floating point with CP56Time2a | yes | `SetpointFloat` |
| `C_BO_TA_1` | 64 | Bitstring of 32 bit with CP56Time2a | yes | `BitstringCommand` |

### System information

| Type | ID | Description | Support | Go type |
|------|----|-------------|---------|---------|
| `M_EI_NA_1` | 70 | End of initialization | yes | `EndOfInitialization` |
| `C_IC_NA_1` | 100 | Interrogation command | yes | `Interrogation` |
| `C_CI_NA_1` | 101 | Counter interrogation command | yes | `CounterInterrogation` |
| `C_RD_NA_1` | 102 | Read command | yes | `Read` |
| `C_CS_NA_1` | 103 | Clock synchronization command | yes | `ClockSync` |
| `C_TS_NA_1` | 104 | Test command ¹ | yes | `TestCommand` |
| `C_RP_NA_1` | 105 | Reset process command | yes | `ResetProcess` |
| `C_CD_NA_1` | 106 | Delay acquisition command ¹ | yes | `DelayAcquisition` |
| `C_TS_TA_1` | 107 | Test command with CP56Time2a | yes | `TestCommand` |

### Parameters in control direction

| Type | ID | Description | Support | Go type |
|------|----|-------------|---------|---------|
| `P_ME_NA_1` | 110 | Parameter of measured value, normalized | yes | `ParameterNormalized` |
| `P_ME_NB_1` | 111 | Parameter of measured value, scaled | yes | `ParameterScaled` |
| `P_ME_NC_1` | 112 | Parameter of measured value, short floating point | yes | `ParameterFloat` |
| `P_AC_NA_1` | 113 | Parameter activation | yes | `ParameterActivation` |

### File transfer

| Type | ID | Description | Support | Go type |
|------|----|-------------|---------|---------|
| `F_FR_NA_1` | 120 | File ready | yes | `FileReady` |
| `F_SR_NA_1` | 121 | Section ready | yes | `SectionReady` |
| `F_SC_NA_1` | 122 | Call directory, select file, call file, call section | yes | `FileCall` |
| `F_LS_NA_1` | 123 | Last section, last segment | yes | `FileLastSegment` |
| `F_AF_NA_1` | 124 | Ack file, ack section | yes | `FileAck` |
| `F_SG_NA_1` | 125 | Segment | yes | `FileSegment` |
| `F_DR_TA_1` | 126 | Directory | yes | `FileDirectoryEntry` |
| `F_SC_NB_1` | 127 | Query log, request archive file ² | yes | `FileQueryLog` |

² Reserved in edition 1 (table 6); added by a later edition.

The types are encoded and decoded. The library runs the transfer procedure
for download, upload and the directory (`Client.GetFile`, `PutFile`,
`ListFiles`; `server.FileServer`); file deletion, the spontaneous directory
and the query log are the application's. See
[Basic application functions](#basic-application-functions).

### Security (IEC TS 60870-5-7:2025)

| Type | ID | Description | Support |
|------|----|-------------|---------|
| `S_AQ_NA_1` | 81 | Association request | yes, `sec` |
| `S_AP_NA_1` | 82 | Association response | yes, `sec` |
| `S_UH_NA_1` | 83 | Update key change request | yes, `sec` |
| `S_UP_NA_1` | 84 | Update key change response | yes, `sec` |
| `S_SI_NA_1` | 85 | Session initiation request | yes, `sec` |
| `S_SQ_NA_1` | 86 | Session request | yes, `sec` |
| `S_SP_NA_1` | 87 | Session response | yes, `sec` |
| `S_KH_NA_1` | 88 | Session key change request | yes, `sec` |
| `S_KP_NA_1` | 89 | Session key change response | yes, `sec` |
| `S_SD_NA_1` | 91 | Secure data | yes, `sec` |

With `WithSecurity` the library creates and checks them; without it they
are **raw**, like any type `asdu` does not decode.

### Private range

Types 128..255 are **raw** in both directions.

## Causes of transmission

All causes 1..17, 20..41 and 44..47 are defined (`asdu.Cause…`) and carried
unchanged; 14..17 are those of IEC TS 60870-5-7 (data protection, session
key, association, operation not authorized). The originator address is supported (`client.WithOriginator`).
The test bit and the P/N bit are exposed as `ASDU.Test` and `ASDU.Negative`.

## Basic application functions

| Function | Client | Server |
|----------|--------|--------|
| Station initialization (`M_EI_NA_1`) | received via `Handler` | app: send with `Session.Send` |
| Cyclic, background, spontaneous transmission | received via `Handler` | `Server.Enqueue` (queued per redundancy group, acknowledged delivery), `Session.Send`, `Server.Broadcast` |
| Station interrogation, global and groups 1..16 | `Interrogate` | app, via `Mux` |
| Counter interrogation, general and groups 1..4, freeze/reset qualifiers | `CounterInterrogate` | app, via `Mux` |
| Read procedure | `Read` | app, via `Mux` |
| Clock synchronization | `ClockSync` | app, via `Mux` |
| Command transmission, direct | `Command` | app, via `Mux` |
| Command transmission, select and execute | `Command` with `Select`, then without | app: the handler keeps the select state |
| Command deactivation | `Deactivate` | app, via `Mux` |
| Commands with time tag | set `Time` on the command | app: the handler checks the age |
| Test procedure (`C_TS_TA_1`) | `TestCommand` | app, via `Mux` |
| Reset process | `ResetProcess` | app, via `Mux` |
| Parameter loading and activation | `Send` | app, via `Mux` |
| File transfer, monitor direction (download) | `GetFile` | `FileServer`, via `Mux` |
| File transfer, control direction (upload) | `PutFile` | `FileServer` with a `FileSink`, via `Mux` |
| File directory | `ListFiles` | `FileServer.Directory`, via `Mux` |
| File deletion, spontaneous directory, query log | app: typed ASDUs with `Send` | app: typed ASDUs via `Mux` |
| Rejecting unknown type / cause / common address | reported as `NegativeError` | yes: `Mux` and `WithCommonAddrs` |
| Rejecting unknown information object address | reported as `NegativeError` | app: `Session.Reject(req, asdu.CauseUnknownIOA)` |

"app, via `Mux`" means the library routes the request to the handler
registered for the type and provides `Confirm`, `Negative`, `Terminate` and
`Reject` for the answers; what the station does is the handler's code.

## Redundancy

IEC 60870-5-104 edition 2, clause 10 ("Redundant connections"), lets a
controlled station serve several connections of one redundancy group, of
which exactly one is started. Closing the connection that is taken over, below,
is how this library keeps it at one; it is not taken from the text of clause 10.

- **Server**: `WithRedundancyGroups` assigns each connection to a group by
  the client's address. Each connection has its own STARTDT/STOPDT state; the
  connection of a group that started data transfer last is its active one, and
  the one active before is closed, so that exactly one is started and nothing
  more is sent on the other. Without that option there is one `default` group
  for everyone, which is a queue and not a redundancy group: every client is
  an independent session, and a STARTDT closes nothing, so with two clients
  started the one that is not active still gets answers, and queued events when
  the active one stops. `Server.Enqueue` delivers events to
  the active connection of every group, buffers them while there is none
  and repeats what was not acknowledged after a switchover.
- **Client**: `client.Group` manages a redundancy group: every connection
  established, exactly one started, the standbys supervised by test frames,
  automatic failover when the active one is lost and manual `Switchover`.
  A single `Client` with `WithAutoStart(false)` is the building block. A
  standby that loses its connection comes back in STOPDT, so it does not take
  over from the active one (`TestReconnectingStandbyDoesNotEvictActive`,
  `TestFailoverThenRejoinDoesNotEvict`). A plain `Client` that reconnects with
  auto-start sends STARTDT and takes over again, in a declared server group.

## Application-layer security

The A-Profile of IEC 62351-5:2023 as IEC TS 60870-5-7:2025 applies it to
IEC 60870-5-104, in package `sec` (`client.WithSecurity`,
`server.WithSecurity`). See [SECURITY.md](../../SECURITY.md#application-layer-security-iec-62351-5)
for what it protects and where it came from.

| Item | Support |
|------|---------|
| Station Association, Session Key Change, Secure Data | yes, both roles |
| Session Initiation Request (controlled station asks for keys) | yes |
| Segmentation of security messages (ASC octet) | yes, both directions |
| Protocol version | 1.0 |
| Key agreement | ECDH on secp256r1 or Curve25519 |
| Key derivation | HKDF with the hash of the MAC algorithm |
| Key wrap (KWA) | 2, AES-256 key wrap (RFC 3394) |
| MAC algorithms (MAL) | 3 HMAC-SHA-256/8, 4 HMAC-SHA-256/16 (default), 7 and 8 HMAC-SHA3-256/8 and /16. Over TCP/IP a controlled station accepts 4 and 8 by default (TS 9.3.3), on serial links (`Config.Serial`) 3 and 7 (TS 9.3.2); `AcceptMAC` lists others |
| Data protection (DPA) | the MAC algorithms above, 11 AES-256-GCM; accepted by default: the MACs of the link and 11 (TS 9.5.2, 9.5.3) |
| Key usage limits | 24 h and 65535 messages at the controlling station, twice that at the controlled station (TS Table 9, lower ends of the recommended ranges) |
| Certificates | X.509, self-signed and pinned, or issued by a trusted Central Authority, optionally restricted by subject |
| Role-based access control | the seven roles of IEC TS 60870-5-7, configured per association; refusals with cause 17; file deletion needs FILEMNGT; without MONITOR DATA no monitor-direction data (types 1 to 44) is sent to the association (TS 8.2) |
| Several controlling stations at one station | yes, one association per AIM, made on first contact or refused |
| Security statistics and events | counters (0 to 2^31-1) and mnemonics locally (`Endpoint.Statistics`, `Config.OnEvent`); M_IT_TB_1 reports, cause spontaneous, at the IOAs and thresholds of `Config.Reports` (TS 5.4.2.3, 9.7, Table 10 defaults); the four revocation counters stay 0 (no revocation checking) |
| Broadcast session key (IEC 60870-5-101) | no |
| secp256k1, X448 (mandatory in the TS 9.2 PICS), brainpool curves; BLAKE2s | no |
| IEC 62351-8 access tokens, certificate revocation lists | no |

Behaviour that goes beyond what cB-Sigma/iec60870 v0.3.0 does, and stays
compatible with it:

- The controlling station changes the session keys whenever a connection
  starts, and holds its data until the change is complete. The controlled
  station holds what it sends until new keys arrive, and accepts data under
  the previous keys meanwhile, from a controlling station that kept them;
  it asks with a Session Initiation Request after a third of
  `Settings.RequestTimeout` if the controlling station has not started a
  change by then.
- A Session Initiation Request that crosses the controlling station's
  Session Request is accepted, and the Session Response is verified in
  both of the forms the crossing allows.
- A Session Key Change that fails makes the next attempt a Station
  Association; failed attempts are repeated with a backoff from
  `Settings.ReplyTimeout` doubling to one minute.
- A Central Authority certificate is checked by its signature on the
  station certificate only, which accepts the authority certificates of
  cB-Sigma that carry no basic constraints.

Where the two differ in what a controlled station permits:

- Over TCP/IP our controlled station refuses the 8-octet MACs (MAL and DPA
  3 and 7) unless configured to accept them (TS 9.3.3, 9.5.3); the
  differential tests that run DPA 3 against cB-Sigma over TCP set
  `AcceptDataProtection` explicitly. The key usage defaults (24 h / 65535
  messages) are longer than cB-Sigma's; the differential tests do not
  depend on them.

- The acknowledgement of a download (F_AF_NA_1 from the controlling
  station) needs the file read permission, so a VIEWER downloads a file to
  its end. cB-Sigma asks for file write, and refuses a viewer's
  acknowledgement with cause 17 after sending the whole file (seen in the
  container suite); our client then reports the refusal.

## Known deviations

The deviations that remain after the edition 1 check are listed once, in
[LIMITATIONS.md](LIMITATIONS.md#known-deviations).

## Verification status

The application-layer security is checked against octet strings produced by
the encoder of cB-Sigma/iec60870 v0.3.0 in the unit tests, and by
differential tests that run complete associations between the two
implementations in memory: both roles, every data protection algorithm,
segmented messages, renewal, crossing requests and Curve25519 certificates
issued by an authority.

The container suite below runs it against cB-Sigma too, through the
security flags of container contract 1.2 (now part of contract 2.2 of
iec60870-5-interop): our
client against the cB-Sigma station and the cB-Sigma client against our
station, with certificates issued by an authority and pinned
self-signed ones, secp256r1 and X25519, HMAC-SHA-256 and AES-256-GCM, other
association identities, role-based access control (a viewer's command
refused with cause 17, an operator's executed, a viewer's download from our
station completed), a station that refuses an
AIM, untrusted certificates on either side and a client without security.
Where a reference station can do the same, the cB-Sigma client's complete
result must not tell our station from it.

Besides its own tests the library is run against five independent
implementations, as client and as server, on every push and pull request that changes more
than Markdown (Interop workflow). The reference
builds are the IEC 104 images of
[otfabric/iec60870-5-interop v0.3.0](https://github.com/otfabric/iec60870-5-interop/releases/tag/v0.3.0)
(container contract 2.2, which for IEC 104 is contract 1.3 of the earlier
otfabric/iec104-interop under new image names, entrypoint and readiness
path), pinned by the digests of the release's `manifest.json`. Until
2026-11-11 the suite ran against iec104-interop v0.5.0, which had the
first four of them.

| Reference | Version | client → reference server | reference client → server | Subtests passed / skipped (local run, 2026-11-11) |
|-----------|---------|---------------------------|---------------------------|------|
| MZ Automation lib60870-C | v2.4.1 | pass | pass | 112 / 0 |
| OpenMUC j60870 | 1.8.0 | pass ¹ ⁴ | pass ⁴ | 107 / 5 |
| wendy512/iec104 on go-iecp5 | v1.0.4 on v1.2.6 | pass ² | pass ² | 88 / 17 |
| cB-Sigma/iec60870 | v0.3.0 | pass ³ | pass ³ | 132 / 0 |
| pascaldekloe/part5 | v0.3.0 | pass ⁵ | pass ⁵ | 68 / 13 |

The run: `IEC104_REQUIRE_INTEROP=1 make interop V=1`, Docker linux/arm64,
31 top-level tests and 519 subtests passed, 35 skipped, 0 failed, 444 s.
Every skip is a feature the image does not declare or the known j60870 bug;
part5 needed no entry of its own in `known-bugs.tsv`.

¹ without file download: the j60870 reference has no file server.
² without commands with time tag and without file transfer: go-iecp5 drops
those types on reception. The suite reads what each reference declares and
skips what it lacks.
³ and the IEC 62351-5 cases above, in both directions.
⁴ without read: j60870 1.8.0 cannot decode a read command (C_RD_NA_1) and
closes the connection on one, as a station on any read and as a controlling
station on a refused read. The suite treats it as a known bug and fails when
it no longer reproduces; a fix is proposed upstream in
[gythialy/j60870#75](https://github.com/gythialy/j60870/pull/75).
⁵ without file transfer, TLS and redundancy, which part5 v0.3.0 does not
have (its image declares none of them); everything else, commands with time
tag included, in both directions.

The suite (`make interop`, package `iec104/interop`) covers the types of the
reference fixture: `M_SP`, `M_DP`, `M_ST`, `M_BO`, `M_ME_NA/NB/NC`, `M_IT`
and their CP56Time2a variants; `C_SC`, `C_DC`, `C_RC`, `C_SE_NA/NB/NC` with
and without time tag; `C_IC`, `C_CI`, `C_RD`, `C_CS`, `C_TS_TA`; the negative
answers; STARTDT/STOPDT; the `k`/`w` windows at 1; `t3` in both directions.
The other type identifications are covered by this repository's codec tests
only.

File download is verified in both roles: `GetFile` fetches the files of the
lib60870-C station with the content its fixture defines, and the lib60870-C
and j60870 clients download from `FileServer`, whose answers they cannot
tell, ASDU by ASDU, from those of the lib60870-C file server.

Container contract 1.3 (part of 2.1) adds interrogation groups, a quality
on `M_BO_NA_1`, mutual TLS (IEC 62351-3), file upload, the file directory and
redundancy groups (IEC 60870-5-104 edition 2, clause 10). The images pinned above
offer it wherever their upstream supports it. The suite reads the
capabilities from the image under test and runs in both directions:

| Feature | our client → reference server | reference client → our server |
|---------|-------------------------------------|-------------------------------------|
| Interrogation groups | `TestClientGroups`: all five | `TestServerGroups`: all five |
| Quality on `M_BO_NA_1` | `TestClientBitstringQuality`: all five (j60870 without read) | `TestServerBitstringQuality`: all five |
| Mutual TLS, with certificates made when the test runs, and the wrong authority, no TLS on either side refused | `TestTLSClient`: lib60870-C, j60870, cB-Sigma | `TestTLSServer`: lib60870-C, j60870, wendy512, cB-Sigma |
| File upload, read back, refusals | `TestClientUpload`: lib60870-C, j60870, cB-Sigma | `TestServerUpload`: lib60870-C, j60870, wendy512, cB-Sigma |
| File directory | `TestClientDirectory`: lib60870-C, j60870, wendy512, cB-Sigma | `TestServerDirectory`: lib60870-C, j60870, wendy512, cB-Sigma |
| Redundancy: STOPDT, takeover, queued event | `TestClientRedundancy`: lib60870-C, j60870, cB-Sigma | `TestServerRedundancy`: lib60870-C, j60870, wendy512, cB-Sigma |

wendy512 on go-iecp5 has no station over TLS, cannot receive a file and cannot
follow a group of connections; part5 v0.3.0 has no TLS, no file transfer and
no redundancy at all. Their images say so and the tests skip.

### Recorded traffic

Besides the live suite, the client and the server are tested against
recorded traffic of other implementations
([testdata/captures](../../iec104/testdata/captures/README.md)): CERT.LV's
spicy-iec104 traces, the University of Western Macedonia IEC 104 dataset
(QTester104 against IEC TestServer, with attacks), Wireshark's synthetic
coverage capture, and the captures added in v0.2.0 (Industroyer2, the MIO8
RTU and others attached to Wireshark issues, the Wireshark wiki, ITI, nDPI,
EmreEkin). Evidence levels: one physical RTU documented in a
Wireshark issue (B, the MIO8 recordings), recordings of unknown origin (C),
software testbeds (D), generated (E). None is level A.

- Every APDU is decoded and encoded again: those of the recorded
  implementations byte for byte, except the impossible CP56Time2a years of
  issue 3900 and the APDUs the codec refuses (see the captures README).
  The decoded fields agree with Wireshark 4.6.9 and with
  CERT.LV's Zeek analyzer; the differences are classified in
  `iec104/internal/capture/oracle_test.go` (ASDUs whose length does not match their
  type, which the codec refuses whole; types it carries raw; Wireshark not
  dissecting a reused port).
- `iec104/client/capture_test.go` plays every recorded station against the client,
  through `Send` and the request methods, including three file downloads and
  three uploads. `iec104/server/capture_test.go` plays every recorded controlling
  station against the server with `Mux` and `FileServer`.
- Classified differences: the client states the day of the week in
  `ClockSync` (the recorded clients send 0), calls a whole file with section
  number 0 and numbers the last section of a file after its last section (as
  lib60870-C v2.4.1; the recorded CERT.LV programs send 1 and the last
  section's number), and cuts file segments at 236 octets (the recorded
  programs at 233). The server confirms STOPDT only once its I frames are
  acknowledged (IEC 60870-5-104 ed. 2, 5.3); the recorded IEC TestServer
  did not wait.
- Recorded faults of IEC TestServer, on which the client closes the
  connection: an N(R) left over from an earlier connection, and I frames
  after STOPDT con. Its negative confirmations of C_RP_NA_1 without the
  qualifier octet do not decode and are dropped.

It has **not** been run against certified test equipment or field devices.
Interoperability reports are welcome.
