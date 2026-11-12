# Feature status

What go-iec60870-5 implements per companion standard and which evidence
backs it. Evidence levels: **unit** (codec/procedure tests), **e2e** (this
library against itself), **independent** (pinned reference images),
**recorded** (real captured traffic). Clause-level detail: IEC 104 in
[iec104/FEATURE-STATUS.md](iec104/FEATURE-STATUS.md) and
[iec104/INTEROPERABILITY.md](iec104/INTEROPERABILITY.md); IEC 101 in
[../INTEROPERABILITY.md](../INTEROPERABILITY.md); IEC 103 in
[iec103/INTEROPERABILITY.md](iec103/INTEROPERABILITY.md); IEC 102 in
[iec102/INTEROPERABILITY.md](iec102/INTEROPERABILITY.md).

## Capability matrix

| Capability | IEC 101 | IEC 102 | IEC 103 | IEC 104 |
|---|---|---|---|---|
| Standard text held ([STANDARDS.md](STANDARDS.md)) | 2003 (Amd. 1 not) | no: vendor documentation, recorded traffic | 1997 | edition 1 (2000) only |
| Controlling station | yes: unit, e2e, independent (3 stacks; balanced not against digitalpetri, an upstream defect), recorded | yes: unit, e2e, recorded (meter responses); no independent peer (no open meter found) | yes: unit, annex B, e2e, recorded, independent (1 stack, link start-up, clock and GI termination; its other answers deviate) | yes: unit, e2e, independent (5 stacks), recorded |
| Controlled station | yes: unit, e2e, independent (3 stacks), recorded | yes: unit, e2e, independent (1 stack: gisce/iec870ree, REE profile; session, C_CI_NT_2/NU_2, clock read and set, product, tariff; image of iec60870-5-interop v0.4.0) | yes: unit, annex B, e2e, independent (4 client stacks) | yes: unit, e2e, independent, recorded |
| Transport | any io.ReadWriter (serial, terminal server TCP stream) | any io.ReadWriter | any io.ReadWriter | TCP, TLS |
| Link: unbalanced | yes: recorded, link states per 101:2003 figures 5-9 | yes (link address 0, 1, 2 octets) | yes (1-octet address, FC 7): recorded | n/a |
| Link: balanced | yes | - | - | n/a |
| Standard ASDU types | all of `asdu` (configurable sizes); recorded: 2, 4, 11, 13, 30-32, 34, 46, 100, 103, 104 | 40 standard types classified (37 with a vendor layout), 38 REE; recorded: 11, 123, 183 | all compatible types 1-11, 20-31; private types raw; recorded: 1, 5-9, 20, 45, 46, 205 (ASDU 9 with up to 127 measurands carried) | the 67 type identifications of `asdu`, less the 101-only C_TS_NA_1 and C_CD_NA_1; the security types 81..89 and 91 in `sec` |
| Interrogation, counters, read, clock, commands, SBO, test, reset | yes: e2e, independent (reset process e2e only), recorded (interrogation, SBO) | integrated totals reads, clock: e2e; independent for C_CI_NT_2, C_CI_NU_2, C_TI_NA_2, C_RD_NA_2 and the REE C_CS_TA_2 (server) | - | yes: independent |
| File transfer | no | - | - | yes: independent |
| TLS (IEC 62351-3) | n/a | n/a | n/a | yes: independent |
| IEC 62351-5 / TS 60870-5-7 | no | n/a | n/a | yes: independent (cB-Sigma); statistics reports, FILEMNGT and MONITOR DATA: unit and e2e |
| Redundancy (104 cl. 10) | n/a | n/a | n/a | yes: independent |
| Protection functions: GI, time sync, general commands, generic services, disturbance transfer | n/a | n/a | yes: e2e; independent for GI, time sync, general commands and class 2 measurands (server, 4 client stacks); disturbance transfer: independent for the server's procedure (clause 7.4.7, figure 10), ASDU 23 after the GI, ASDU 26 and 27 descriptions and the tags with their positions (Open103 client; its channel values are a known bug of Open103, `open103-asdu30-nfe`); generic services and the disturbance list: no conforming independent peer (vitusdong uses INF 0 for every generic ASDU and its own disturbance procedure; known bugs, iec60870-5-interop contract 2.3) | n/a |
| Fuzzed decoders | ft12, asdu, cp | iec102 | iec103 | apci, asdu, sec, e2e |
| Fuzzed protocol machines | link (secondary, balanced, primary), telecontrol dispatch, client and server receive paths | link, server, client answers | link, server dispatch, client receive | link, server stream |
| Hostile peer and chaos (faulty line) | yes: e2e, unbalanced and balanced | yes: e2e | yes: e2e | yes: e2e |

## Security capability matrix

| Property | IEC 101 | IEC 102 | IEC 103 | IEC 104 |
|---|---|---|---|---|
| Transport encryption | no (serial) | no | no | TLS 1.2/1.3 |
| Application authentication | no | no | no | IEC 62351-5 association |
| Message integrity | FT 1.2 checksum only (not security) | - | - | HMAC or AES-GCM per ASDU |
| Replay protection | no | - | - | IEC 62351-5 sequence numbers |
| Authorization / RBAC | no | - | - | IEC TS 60870-5-7 roles |

IEC TS 60870-5-7 profiles security for 101 and 104 only; no security
profile is claimed or planned for 102 or 103.
