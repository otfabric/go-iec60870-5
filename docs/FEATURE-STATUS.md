# Feature status

What go-iec60870-5 implements per companion standard and which evidence
backs it. Evidence levels: **unit** (codec/procedure tests), **e2e** (this
library against itself), **independent** (pinned reference images),
**recorded** (real captured traffic). Clause-level detail: IEC 104 in
[iec104/FEATURE-STATUS.md](iec104/FEATURE-STATUS.md) and
[iec104/INTEROPERABILITY.md](iec104/INTEROPERABILITY.md); IEC 101 in
[../INTEROPERABILITY.md](../INTEROPERABILITY.md).

## Capability matrix

| Capability | IEC 101 | IEC 102 | IEC 103 | IEC 104 |
|---|---|---|---|---|
| Controlling station | yes: unit, e2e | no | no | yes: unit, e2e, independent (5 stacks), recorded |
| Controlled station | yes: unit, e2e | no | no | yes: unit, e2e, independent, recorded |
| Transport | any io.ReadWriter (serial, terminal server TCP stream) | - | - | TCP, TLS |
| Link: unbalanced | yes | - | - | n/a |
| Link: balanced | yes | - | - | n/a |
| Standard ASDU types | all of `asdu` (configurable sizes) | - | - | 67 + 2 (101-only) |
| Interrogation, counters, read, clock, commands, SBO, test, reset | yes: e2e | - | - | yes: independent |
| File transfer | no | - | - | yes: independent |
| TLS (IEC 62351-3) | n/a | n/a | n/a | yes: independent |
| IEC 62351-5 / TS 60870-5-7 | no | n/a | n/a | yes: independent (cB-Sigma) |
| Redundancy (104 cl. 10) | n/a | n/a | n/a | yes: independent |
| Fuzzed decoders | ft12, asdu, cp | - | - | apci, asdu, sec, e2e |

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
