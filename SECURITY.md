# Security Policy

## Reporting a Vulnerability

If you discover a security vulnerability in `go-iec60870-5`, please report it responsibly.

**Do not open a public GitHub issue for security vulnerabilities.**

Instead, please email **security@otfabric.io**, or open a private advisory on GitHub (the [Security](https://github.com/otfabric/go-iec60870-5/security) tab → **Advisories** → **Report a vulnerability**), with:

- A description of the vulnerability
- Steps to reproduce the issue, ideally a minimal program or test
- The affected version(s)
- Any potential impact you've identified

We will acknowledge your report within 48 hours and aim to provide a fix or mitigation within 7 days for critical issues.

## Supported Versions

| Version | Supported |
|---------|-----------|
| Latest release | Yes |
| Older releases | Best effort |

## Security Considerations

### IEC 60870-5-101, -102 and -103 have no security

The serial companion standards carry no authentication and no encryption,
and this module adds none: the FT 1.2 checksum detects line errors, not
tampering. IEC TS 60870-5-7 profiles security for 101 (and 104) only; `sec`
is wired into IEC 104 only. Keep serial lines and the terminal servers
that carry them on trusted, isolated networks.

What the serial stations do against a faulty or hostile peer: every frame
and ASDU is checked before use and the decoders and receive paths are
fuzzed; the send queues are bounded (`QueueSize`, `server.ErrQueueFull`);
the hostile-peer suites (`iec101/e2e`, `iec102/e2e`, `iec103/e2e`,
`hostile_test.go`) check that a station stays bounded and serves a peer
that behaves afterwards ([TESTING.md](TESTING.md)). After a pause longer
than `link.Config.FrameGap` the FT 1.2 decoder neither completes an
unfinished frame nor waits for the rest of a rejected one, so the frames
that follow the pause are read. The IEC 103 client keeps a disturbance
record within the NOC channels of NOE values its ASDU 26 announced. The
IEC 103 server does not apply a time synchronization flagged invalid or
outside the CP56Time2a years 1970 to 2069, and holds its clock offset
within ±36525 days. The IEC 102 client takes as an answer only what
repeats the request's information object, and drains the rest of an
unfinished answer before the next request.

### IEC 60870-5-104 Has No Built-In Security

Plain IEC 60870-5-104 carries no authentication and no encryption: anyone who can reach a station on port 2404 can interrogate it and send it commands, and anyone on the path can read or alter the traffic. The commands operate physical equipment.

- **Keep plain IEC 104 traffic on isolated, trusted networks**; never expose port 2404 to the Internet
- **Use TLS across untrusted networks** (`client.WithTLS`, `server.WithTLS`), the application-layer security of IEC 62351-5 (`client.WithSecurity`, `server.WithSecurity`), or both, or put a VPN or secured gateway in front of the station

### TLS (IEC 62351-3)

- `server.WithTLS` and `client.WithTLS` take a standard `*tls.Config`; the library does not weaken or override it. Set `MinVersion` to TLS 1.2 or later
- For mutual authentication set `ClientAuth: tls.RequireAndVerifyClientCert` and `ClientCAs` on the server and `Certificates` on the client. Without it a TLS server still accepts commands from any client
- Authorization is the handler's job: `Session.Conn()` returns the `*tls.Conn`, whose `ConnectionState().PeerCertificates` identifies the controlling station
- Keep private keys out of source control and give key files restrictive permissions (`chmod 600`)
- TLS protects the connection; the application-layer security of IEC 62351-5, below, protects the ASDUs end to end and authorizes per request. The two combine

### Application-layer security (IEC 62351-5)

IEC 62351-5 authenticates application messages themselves, on top of or instead of TLS; IEC TS 60870-5-7 says how it applies to IEC 60870-5-101 and -104. Package `sec` implements the A-Profile of the 2023 edition with IEC TS 60870-5-7:2025: `client.WithSecurity` and `server.WithSecurity` switch it on, and [examples/secure](iec104/examples/secure/main.go) shows it end to end.

- **Station Association**: the stations exchange X.509 certificates; each checks the other's against its `sec.Trust` (pinned fingerprints or public keys of self-signed certificates, or Central Authorities with an optional list of subjects). ECDH on secp256r1 or Curve25519 and HKDF give the update keys
- **Session Key Change**: the controlling station sends fresh session keys, wrapped with AES-256 key wrap, at every connection, after `Settings.KeyLifetime` or `Settings.KeyUses`, and on `Endpoint.Renew`. A controlled station whose keys are not valid asks for new ones
- **Secure Data**: every application ASDU travels in `S_SD_NA_1` with a sequence number, under HMAC-SHA-256 or HMAC-SHA3-256 (16 octets of MAC over TCP/IP; 8 octets on serial links, `sec.Config.Serial`, TS 9.3 and 9.5) or AES-256-GCM, which also encrypts it. A controlled station accepts only the MAC lengths of its link unless `AcceptMAC` and `AcceptDataProtection` list others. An unprotected ASDU, a MAC that does not verify and a sequence number that does not increase are discarded
- **Role-based access control**: `sec.Config.Roles` gives the controlling station roles of IEC TS 60870-5-7 at the station; a request they do not permit is answered with a negative confirmation with cause `asdu.CauseNotAuthorized` and never reaches the handler. The client reports it as `*iec104.NegativeError`. Deleting a file (F_SC_NA_1 with the delete qualifier) needs FILEMNGT (TS 8.2); a role without MONITOR DATA receives no monitor-direction data (types 1 to 44): `Session.Send` fails with `sec.ErrNotPermitted`, `Server.Broadcast` skips the session and `Server.Enqueue` drops the event for it
- **Statistics and events**: the counters of IEC 62351-5 (`Endpoint.Statistics`, 0 to 2^31-1) and its security events with their mnemonics (`Config.OnEvent`). A controlled station reports the statistics listed in `sec.Config.Reports` spontaneously in M_IT_TB_1 at their configured IOAs once they grew by their threshold (TS 5.4.2.3, 9.7; the default thresholds of Table 10). The reports are Secure Data and go to every role: they are security information, not process data (an interpretation: TS 8.2 does not say)
- **Key usage**: the session keys are changed after 24 hours or 65535 messages by default (the lower ends of the ranges of TS Table 9); the controlled station allows twice that
- **Keys at rest**: `sec.FileStore` keeps the association in a file with mode 0600, so that a restart does not need a new Station Association. The file holds the keys as they are: put it where only the station's account can read it, on encrypted storage, or implement `sec.KeyStore` against a key vault or HSM
- **One association per controlling station**: every security message names its controlling station by the AIM, the controlling station's half of the association ID. `server.WithSecurity` makes the station's association for an AIM when that station is first heard, or refuses it. A connection is bound to that association only once a message on it is authenticated for it: a Station Association or a Session Key Change that completes, or Secure Data that verifies under the current session keys. Until then the station answers the connection's key management on a copy of the association and changes nothing of it: the connection that carries the association keeps it, `Session.Security` of the other is nil, and what its messages fail is counted in the association's statistics and events. An association that no message has authenticated is kept only while a connection waits for it; `newEndpoint` is called again when that station is heard next. The connections of one controlling station (a client redundancy group, which shares one `Endpoint`) share its association, carried by the connection that is started; other controlling stations, in the same redundancy group or not, have their own and take nothing over
- **Reassembly is bounded**: the segments of one security message are joined up to the longest message the A-Profile defines, the Association Response with a certificate of 65535 octets (IEC TS 60870-5-7:2025, Figure 8; 65797 octets after the data unit identifier). A series that would grow beyond it is abandoned and counted in `StatDiscardedMessages`, as any other abandoned series
- **An AIM is a claim**: any station the `Trust` accepts can claim any AIM, and with it the association and roles of that AIM. Give each AIM a `Trust` that accepts only its own station (`Trust.Subjects` with an authority, or the pinned certificate), and the `Roles` of that station

What it does not have: the curves secp256k1 and X448 (both mandatory in the IEC TS 60870-5-7 PICS) and brainpool, BLAKE2s, the broadcast session key of IEC 60870-5-101, access tokens of IEC 62351-8 (the roles are configured, not carried), and certificate revocation lists (keep `Trust` current instead).

#### Provenance and interoperability

The IEC documents are not freely available, and this implementation was not written from them (IEC TS 60870-5-7:2025 was obtained afterwards and the framing checked against it, below). The message formats, the algorithms and the state machines were derived from [cB-Sigma/iec60870](https://github.com/cB-Sigma/iec60870) (Apache-2.0), the only open-source implementation of this edition we found, and implemented anew; no code was copied. The unit tests check our encodings against octet strings that its encoder produced, and differential tests run complete associations, in both roles and with every data protection algorithm, between the two implementations. Where the two differ, the difference is deliberate and listed in [docs/iec104/INTEROPERABILITY.md](docs/iec104/INTEROPERABILITY.md#application-layer-security). Neither implementation has been tested against a third, independent one or certified: treat the A-Profile as interoperable with cB-Sigma, not as conformance-tested, and keep TLS with client certificates where both ends support it.

#### Checked against IEC TS 60870-5-7:2025

On 2026-11-11 the framing was checked clause by clause against the text of
IEC TS 60870-5-7 Edition 2.0 (2025-03). Conforming: the type
identifications 81..89 and 91, the causes 14..17, the ASDU layouts (no IOA,
the ASC octet with FIN/FIR/ASN), segmentation and reassembly, the PRI and
BK bits, the MAC and AEAD input layouts the TS gives, the role table, the
handling of redundancy groups and link failure. What the TS delegates to
IEC 62351-5:2023 (algorithm code numbers, HKDF inputs, the GCM nonce,
the unsolicited key-management MAC inputs, replay rules) is still not
checked against a standard text: we do not hold IEC 62351-5.

Fixed after the check (v0.2.0): the security statistics are reported on
the wire (TS 5.4.2.3, 9.7, `Config.Reports`), wrap at 2^31 and count an
expired remote certificate as Remote Station's Certificate Expirations;
file deletion needs FILEMNGT and monitor-direction data needs MONITOR
DATA (TS 8.2); the key usage defaults are 24 hours / 65535 messages (TS
Table 9); over TCP/IP a controlled station accepts only the 16-octet MACs
(TS 9.3.3, 9.5.3).

Gaps against the TS, open:

- **Revocation statistics stay 0**: Remote and Local Station's Certificate
  Revocations and the two Cryptographic Keys Invalidations after a
  revocation are never incremented, since certificate revocation is not
  checked (keep `Trust` current instead).
- **Mandatory curves missing** (TS 9.2.1, 9.2.2): Curve448 and secp256k1
  are mandatory in the PICS and not implemented (the Go standard library
  has neither). A peer whose certificate uses them is refused.
- **Open reading, field width**: the TS contradicts itself on the length
  of the controlled station's random data (CGL) in S_AP_NA_1, S_SI_NA_1
  and S_SP_NA_1: Figures 8, 11 and 14 draw two octets, the text of
  5.4.3.8.4 defines UI8. The code uses one octet, as cB-Sigma does
  (citing IEC 62351-5); the shared reading means our differential tests
  cannot decide it.
- A Session Initiation Request received while waiting for a Session
  Response is accepted (crossing requests), where TS 5.3.4.3.3 discards
  it; the first Session Initiation Request of a restarted controlled
  station waits a third of the request timeout.


### Hardening a Server

- `server.WithAccept` restricts which addresses may connect; it is checked before any protocol exchange. Treat it as defence in depth: a source address is not an identity
- `server.WithMaxSessions` bounds the number of simultaneous connections, TLS handshakes in progress included: a connection beyond the limit is closed before its handshake starts
- `Serve` keeps serving through accept errors such as running out of file descriptors: each is logged (`accept failed`) and retried after a pause that doubles from 5 ms up to 1 s and ends when the server is closed
- `server.WithCommonAddrs` rejects ASDUs for stations the server does not represent
- Validate every command in the handler (address, value range, select-before-operate state, interlocks) before acting on the process. The library checks the encoding, not the meaning

### Input Handling

- Every received APDU and ASDU is length- and structure-checked before use; malformed input closes the connection (APDU) or is dropped (ASDU) and cannot cause a panic. The decoders are fuzzed
- Memory per connection is bounded, except for the files a `FileServer` with a `Sink` accepts, whose number per session is not capped: frames are at most 255 octets, a security message is reassembled up to the longest the A-Profile defines (above), and a slow handler stops acknowledgements and ultimately reading instead of queueing without limit
- A file section that a controlling station announces (F_SR_NA_1) reserves no memory before its data arrives: an upload grows only with the segments received
- A TLS handshake must complete within `t0`; a peer that connects and stays silent does not hold a session
- A panic in a server handler closes that session only
