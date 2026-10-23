# Signaling and media protection

SIP signaling and media use separate security layers.

- `TLS` and `WSS` protect SIP messages between signaling hops. The client validates certificate chains and hostnames and does not downgrade a `sips:` route to plaintext.
- `DTLS-SRTP` authenticates the media peer through the SDP fingerprint, exports SRTP keys from the DTLS handshake, and protects RTP and RTCP packets.
- ICE, STUN, and TURN discover or relay a viable media path. TURN authentication protects allocation access; TURN by itself does not provide end-to-end media confidentiality.

Enabling signaling TLS does not encrypt RTP. A protected media path must negotiate DTLS-SRTP in SDP and fail when the fingerprint or handshake is invalid. `media.EstablishDTLSSRTP` owns the supplied packet connection after it starts, multiplexes DTLS and SRTP according to RFC 5764, and closes that connection with the secure session. `media.NewConnectedPacketConn` adapts a connected ICE path, including a TURN-selected path, for this API.

The automated integration test establishes ICE through an authenticated relay-only TURN server, completes DTLS over the selected pair, and transfers an encrypted RTP packet. It also covers expired TURN credentials, fingerprint mismatch, and idempotent closure. The high-level `Call` API currently negotiates ordinary RTP; an application that requires protected media must drive the ICE/DTLS media path explicitly from the parsed SDP attributes. The implementation does not treat SIP over DTLS as a supported signaling transport.
