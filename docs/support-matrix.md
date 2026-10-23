# Support matrix

| Capability | Supported scope | Validation |
| --- | --- | --- |
| Go | 1.22.2 and current stable | Linux, Windows, and macOS CI |
| Signaling | TCP, UDP, TLS, WS, WSS | Unit and loopback integration tests |
| SCTP | Linux amd64 and arm64, experimental | Build and focused tests |
| Address families | IPv4 and IPv6 | Loopback signaling, SDP, and RTP tests; IPv6 test skips when the host has no IPv6 loopback |
| SIP authentication | Digest MD5/SHA-256, qop auth/auth-int; injected OAuth and AKA providers | Challenge vectors and registration scenarios |
| Calls | INVITE, provisional/final responses, PRACK, ACK, CANCEL, BYE, re-INVITE, hold/resume, early media and forks | Simulated-peer integration tests |
| Events | SUBSCRIBE/NOTIFY, PUBLISH, MESSAGE, INFO and REFER | Lifecycle, validation, timeout and cleanup tests |
| Presence | PIDF subscriber and publisher roles | XML round-trip and SIP lifecycle tests |
| RTP | RTP v2, CSRC, extension, padding, pacing, per-call SSRC/sequence/timestamp | Packet, session, metrics and race tests |
| RTCP | Receiver reports and loss/jitter metrics | Deterministic unit tests |
| Audio | PCMU, PCMA, G.722 and iLBC by default | Codec vectors and round trips |
| Optional audio | GSM-FR, Opus and standalone SILK | Tagged Linux CI with native dependencies |
| NAT | ICE host candidates, STUN and authenticated TURN relay | Host and local TURN integration tests |
| Media security | DTLS fingerprint and SRTP/SRTCP contexts | Bidirectional protection, mismatch and closure tests |
| PBX interoperability | Asterisk 22.11.0 registration and outbound call lifecycles over UDP | Fixed container image in the integration workflow |
| Releases | Semantic tags `vX.Y.Z` and prereleases | Tag/SHA validation, CI, integration, GitHub Release and Go proxy smoke test |

The fixed Asterisk scenario validates registration authentication/removal and an outbound INVITE/ACK/BYE dialog with SDP. Interoperability with other PBXs or public network services still depends on each deployment and is not a universal compatibility claim.
