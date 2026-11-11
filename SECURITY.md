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

IEC 62351-5 authenticates application messages themselves, on top of or instead of TLS; IEC TS 60870-5-7 says how it applies to IEC 60870-5-101 and -104. Package `sec` implements the A-Profile of the 2023 edition with IEC TS 60870-5-7:2025: `client.WithSecurity` and `server.WithSecurity` switch it on, and [examples/secure](examples/secure/main.go) shows it end to end.

- **Station Association**: the stations exchange X.509 certificates; each checks the other's against its `sec.Trust` (pinned fingerprints or public keys of self-signed certificates, or Central Authorities with an optional list of subjects). ECDH on secp256r1 or Curve25519 and HKDF give the update keys
- **Session Key Change**: the controlling station sends fresh session keys, wrapped with AES-256 key wrap, at every connection, after `Settings.KeyLifetime` or `Settings.KeyUses`, and on `Endpoint.Renew`. A controlled station whose keys are not valid asks for new ones
- **Secure Data**: every application ASDU travels in `S_SD_NA_1` with a sequence number, under HMAC-SHA-256 (16 octets of MAC over IEC 104; 8 octets is the serial-link profile, also accepted), HMAC-SHA3-256 or AES-256-GCM, which also encrypts it. An unprotected ASDU, a MAC that does not verify and a sequence number that does not increase are discarded
- **Role-based access control**: `sec.Config.Roles` gives the controlling station roles of IEC TS 60870-5-7 at the station; a request they do not permit is answered with a negative confirmation with cause `asdu.CauseNotAuthorized` and never reaches the handler. The client reports it as `*iec104.NegativeError`
- **Statistics and events**: the counters of IEC 62351-5 (`Endpoint.Statistics`) and its security events with their mnemonics (`Config.OnEvent`). They are not reported on the wire (see below)
- **Keys at rest**: `sec.FileStore` keeps the association in a file with mode 0600, so that a restart does not need a new Station Association. The file holds the keys as they are: put it where only the station's account can read it, on encrypted storage, or implement `sec.KeyStore` against a key vault or HSM
- **One association per controlling station**: every security message names its controlling station by the AIM, the controlling station's half of the association ID. `server.WithSecurity` makes the station's association for an AIM when that station is first heard, or refuses it; a connection is bound to its association by the first security message on it. The connections of one controlling station (a client redundancy group, which shares one `Endpoint`) share its association, carried by the connection that is started; other controlling stations, in the same redundancy group or not, have their own and take nothing over
- **An AIM is a claim**: any station the `Trust` accepts can claim any AIM, and with it the association and roles of that AIM. Give each AIM a `Trust` that accepts only its own station (`Trust.Subjects` with an authority, or the pinned certificate), and the `Roles` of that station

What it does not have: the curves secp256k1 and X448 (both mandatory in the IEC TS 60870-5-7 PICS) and brainpool, BLAKE2s, the broadcast session key of IEC 60870-5-101, access tokens of IEC 62351-8 (the roles are configured, not carried), and certificate revocation lists (keep `Trust` current instead).

#### Provenance and interoperability

The IEC documents are not freely available, and this implementation was not written from them. The message formats, the algorithms and the state machines were derived from [cB-Sigma/iec60870](https://github.com/cB-Sigma/iec60870) (Apache-2.0), the only open-source implementation of this edition we found, and implemented anew; no code was copied. The unit tests check our encodings against octet strings that its encoder produced, and differential tests run complete associations, in both roles and with every data protection algorithm, between the two implementations. Where the two differ, the difference is deliberate and listed in [docs/iec104/INTEROPERABILITY.md](docs/iec104/INTEROPERABILITY.md#application-layer-security). Neither implementation has been tested against a third, independent one or certified: treat the A-Profile as interoperable with cB-Sigma, not as conformance-tested, and keep TLS with client certificates where both ends support it.

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

Gaps against the TS, open:

- **Security statistics are not reported on the wire** (TS 5.4.2.3, 9.7):
  the counters exist (`Endpoint.Statistics`), but there is no spontaneous
  M_IT_TB_1 report at configured information object addresses, no
  thresholds, the counters wrap at 2^32 instead of 2^31, and five counters
  (remote certificate expiry, remote and local revocation, the two key
  invalidations after revocation) are never incremented.
- **Mandatory curves missing** (TS 9.2.1, 9.2.2): Curve448 and secp256k1
  are mandatory in the PICS and not implemented (the Go standard library
  has neither). A peer whose certificate uses them is refused.
- **File deletion needs only "file read"** under role-based access control
  (TS 8.2 FILEMNGT): F_SC_NA_1 is mapped to file read whatever its
  qualifier. Do not implement file deletion in a handler of a secured
  station that gives VIEWER or SECAUD roles.
- **MONITOR DATA covers requests only** (TS 8.2): roles without it are
  refused interrogation and read, but still receive spontaneous and
  cyclic data.
- **Key usage defaults** (TS 9.6, Table 9) follow IEC 62351-5 as cB-Sigma
  does (15 minutes / 1000 messages at the controlling station, twice that
  at the controlled station), below the TS's recommended range (24 hours
  to a week, 2^16-1 to 2^31-1 messages). Set `Settings` explicitly against
  peers configured to the TS.
- **Open reading, field width**: the TS contradicts itself on the length
  of the controlled station's random data (CGL) in S_AP_NA_1, S_SI_NA_1
  and S_SP_NA_1: Figures 8, 11 and 14 draw two octets, the text of
  5.4.3.8.4 defines UI8. The code uses one octet, as cB-Sigma does
  (citing IEC 62351-5); the shared reading means our differential tests
  cannot decide it.
- Over IEC 104 a controlled station also accepts the 8-octet MAC lengths
  of the serial-link profile (TS 9.3.3 lists only 16-octet ones for
  TCP/IP).
- A Session Initiation Request received while waiting for a Session
  Response is accepted (crossing requests), where TS 5.3.4.3.3 discards
  it; the first Session Initiation Request of a restarted controlled
  station waits a third of the request timeout.


### Hardening a Server

- `server.WithAccept` restricts which addresses may connect; it is checked before any protocol exchange. Treat it as defence in depth: a source address is not an identity
- `server.WithMaxSessions` bounds the number of simultaneous connections
- `server.WithCommonAddrs` rejects ASDUs for stations the server does not represent
- Validate every command in the handler (address, value range, select-before-operate state, interlocks) before acting on the process. The library checks the encoding, not the meaning

### Input Handling

- Every received APDU and ASDU is length- and structure-checked before use; malformed input closes the connection (APDU) or is dropped (ASDU) and cannot cause a panic. The decoders are fuzzed
- Memory per connection is bounded: frames are at most 255 octets, and a slow handler stops acknowledgements and ultimately reading instead of queueing without limit
- A TLS handshake must complete within `t0`; a peer that connects and stays silent does not hold a session
- A panic in a server handler closes that session only
