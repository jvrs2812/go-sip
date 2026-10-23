# Changelog

All notable changes to this project are documented here. The project follows [Semantic Versioning](https://semver.org/).

## v0.3.0-rc.2 — 2026-10-23

### Fixed

- Synchronize the nested audio-player module dependencies with the expanded root module.

## v0.3.0-rc.1 — 2026-10-23

### Added

- Typed SIP message parser and serializer with configurable size limits.
- Typed SIP/SIPS URI and name-address parsing with IPv6, parameters and embedded headers.
- Isolated TCP, UDP, and TLS signaling transports.
- Digest MD5 and SHA-256 authentication with qop support.
- Context-aware registration, renewal, unregister, and `423 Min-Expires` handling.
- Inbound and outbound basic call flows with ACK, BYE, CANCEL, and OPTIONS handling.
- SDP audio negotiation and per-call RTP sessions.
- PCMU and PCMA encode/decode helpers.
- Pure-Go G.722 encoding, decoding, SDP negotiation, and RTP payload support.
- Pure-Go iLBC 20/30 ms encoding, decoding, packet-loss concealment, and mode-aware SDP negotiation.
- Optional build-tagged GSM-FR, Opus, and standalone SILK encoders/decoders, conditional SDP advertisement, Opus PLC, and native-backend CI.
- DNS NAPTR/SRV discovery and ordered target failover.
- Injected A/AAAA resolution, RFC 2782 weighted SRV ordering, DNS TTL cache, and a total failover deadline.
- WebSocket and secure WebSocket SIP transports.
- Experimental kernel SCTP signaling transport on supported Linux architectures.
- ICE candidate gathering, STUN/TURN server configuration, restart, and connectivity checks.
- Authenticated relay-only TURN allocation and traffic through a local integration server.
- DTLS-SRTP fingerprint validation and bidirectional RTP/RTCP protection.
- RTP jitter buffering, reception metrics, and RTCP receiver reports.
- DTMF telephone events, hold/resume, PRACK, and reliable provisional responses.
- MESSAGE, INFO, REFER, SUBSCRIBE, NOTIFY, and PUBLISH request APIs and callbacks.
- Injected OAuth token provider and experimental ES256 SIP Identity support.
- Provider-based AKAv2 authentication and RFC 4169 password/session-key derivation.
- SDP capability attributes, History-Info parsing, and a PCM conference bridge.
- Stateful subscription, publication, presence, and transfer lifecycles.
- Laboratory-only emergency calling profiles using RFC 5031 service URNs.
- Fixed-version Asterisk registration and outbound-call interoperability plus prolonged stability jobs.
- Goroutine-leak verification for client, transport, NAT, and media packages.
- Cross-platform CI, security checks, and tag-driven GitHub Releases.

### Changed

- Examples are independent commands; the native audio player is a nested module.
- The README now distinguishes tested, partial, and planned behavior.
- Signaling reconnects with bounded exponential backoff and restores an intended registration.
- RTP binds to the route-selected or explicitly configured local address while SIP and SDP use the separately advertised address.
- The project is available under the MIT License, including commercial use.

### Fixed

- Removed competing consumers from the SIP response path.
- Removed process-wide RTP socket, sequence, timestamp, and first-packet state.
- Added response correlation by Call-ID, CSeq, and method.

## v0.2.3 and earlier

Historical releases predate this changelog. Git tags remain the authoritative source for their contents.
