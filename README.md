# go-sip

`go-sip` is a Go library for building SIP user agents. The current development version provides typed SIP parsing, TCP/UDP/TLS/WebSocket transports, REGISTER with Digest or injected OAuth authentication, inbound and outbound calls, SDP negotiation, per-call RTP sessions, ICE helpers, and DTLS-SRTP packet protection.

The public API is still evolving in the `v0` series. See [PLANO_IMPLEMENTACAO.md](PLANO_IMPLEMENTACAO.md) for the complete implementation plan and acceptance criteria.

## Requirements

- Go 1.22.2 or newer
- TCP, UDP, TLS, WebSocket, or secure WebSocket connectivity to a SIP server
- An ALSA development package on Linux only when building the optional audio-player example
- Native development packages only when enabling the optional `gsm` or `opus` build tags

## Install

```bash
go get github.com/jvrs2812/go-sip@latest
```

## Register a client

```go
package main

import (
    "context"
    "log"

    "github.com/jvrs2812/go-sip/client"
)

func main() {
    sipClient, err := client.NewClient(client.Config{
        ServerHost: "sip.example.com",
        ServerPort: 5060,
        Username:   "1001",
        Password:   "secret",
    })
    if err != nil {
        log.Fatal(err)
    }

    defer sipClient.Close()

    if err := sipClient.Register(context.Background()); err != nil {
        log.Fatal(err)
    }
}
```

`Register` handles 401 and 407 challenges, MD5 and SHA-256 Digest, `qop=auth`, `423 Min-Expires`, renewal, connection recovery, and explicit removal through `Unregister`. OAuth challenges use an application-provided `auth.TokenProvider`; the library does not store client secrets or fetch tokens on its own. Give calls a context with a deadline in production.

## Receive a call

```go
sipClient.OnInviteReceived = func(current *client.Client, invite types.InviteData) {
    log.Printf("call from %s", invite.From)
    current.AcceptInvite(invite)
}

sipClient.OnAudioReceived = func(_ *client.Client, packet types.AudioData) {
    log.Printf("RTP payload=%d bytes=%d", packet.PayloadType, len(packet.Payload))
}

if err := sipClient.Register(ctx); err != nil {
    log.Fatal(err)
}
```

The client sends `180 Ringing`, negotiates G.722, PCMU, PCMA, or iLBC from the remote SDP offer, opens the RTP socket before sending `200 OK`, replies to BYE, and releases media resources when the call ends.

## Place a call

```go
call, err := sipClient.Invite(ctx, "sip:2002@sip.example.com")
if err != nil {
    log.Fatal(err)
}

defer call.Hangup(context.Background())

if err := call.SendAudio(ctx, encodedG711Payload, false); err != nil {
    log.Fatal(err)
}
```

Outbound calls advertise G.722, PCMU, PCMA, and both iLBC frame modes, process the SDP answer, send ACK, and create an isolated RTP session.

## Examples

- `go run ./example/register`: register and unregister cleanly on shutdown.
- `go run ./example/receive`: accept calls and log RTP packets.
- `cd example/player && go run .`: accept a call and play G.711 audio.

The player is a nested Go module so native audio dependencies do not affect normal builds. On Debian or Ubuntu, install them with:

```bash
sudo apt-get install libasound2-dev pkg-config
```

All examples read `SIP_HOST`, `SIP_USERNAME`, `SIP_PASSWORD`, and optionally `SIP_ADVERTISED_ADDRESS`. Run an example with `-help` for its flags.

## Current implementation status

| Area | Status | Notes |
| --- | --- | --- |
| SIP parser and serializer | Implemented and tested | Typed request/response and SIP/SIPS URI models, IPv6, compact headers, framing limits, fragmented/concatenated messages, binary bodies and fuzz target |
| TCP transport | Implemented and tested | One reader per connection, complete writes, context deadlines, idempotent close |
| UDP transport | Implemented and tested | Retransmission with exponential timers, response correlation, and server-response deduplication |
| TLS and SIPS | Implemented and tested | TLS 1.2 minimum, certificate-chain and hostname validation by default |
| WebSocket and WSS | Implemented and tested | Required `sip` subprotocol, frame limits, ping keep-alive, rejection paths, and TLS policy for WSS |
| REGISTER | Implemented and tested | Digest/OAuth challenge, renewal, unregister, 423 handling, reconnect and registration restoration |
| Digest MD5 and SHA-256 | Implemented and tested | `auth` and `auth-int`; legacy no-qop challenges |
| Inbound INVITE / ACK / BYE / CANCEL | Implemented, interoperability pending | Single-dialog flow with G.711 SDP negotiation |
| Outbound INVITE / ACK / BYE | Implemented and tested | G.722/PCMU/PCMA/iLBC offer and answer; fixed Asterisk 22.11.0 dialog integration |
| OPTIONS | Implemented | Answers with currently recognized methods and capabilities |
| RTP and RTCP | Implemented and tested | Per-call RTP state, CSRC/extensions/padding, jitter buffer, reception metrics, RTCP receiver reports |
| PCMU and PCMA | Implemented | Encoding and decoding through `media.PCMU` and `media.PCMA` |
| G.722 | Implemented and tested | Pure-Go stateful 64 kbit/s encoder/decoder and static RTP payload 9 negotiation |
| iLBC | Implemented and tested | Pure-Go 20 ms/30 ms encode/decode, packet-loss concealment, and mode-aware SDP negotiation |
| PRACK and reliable provisional responses | Implemented, interoperability pending | `100rel`, `RSeq`, `RAck`, and provisional callback paths are covered locally |
| SUBSCRIBE, NOTIFY, PUBLISH, presence | Implemented and tested | Stateful refresh, expiry, cancellation/removal, dialog validation, entity tags, and PIDF |
| MESSAGE, INFO, REFER | Implemented and tested | MIME and size validation, DTMF INFO, blind/attended transfer, and refer NOTIFY tracking |
| DNS NAPTR/SRV and failover | Implemented and tested | Injected NAPTR/SRV/A/AAAA resolver, RFC 2782 weighted ordering, TTL cache and total failover deadline |
| ICE, STUN and TURN | Implemented and tested locally | Candidate gathering, restart, authenticated relay-only TURN allocation and selected-pair traffic; external NAT interoperability remains |
| DTLS-SRTP | Implemented and tested locally | Ephemeral identity, SDP fingerprint validation, key export, bidirectional SRTP; call-level interoperability remains |
| GSM-FR, Opus, and SILK | Optional and tested | Independent CGO backends behind matching build tags; each codec is advertised only when compiled in |
| SIP Identity | Experimental and tested | ES256 PASSporT signing/verification with an injected certificate resolver |
| OAuth 2.0 | Experimental and tested | Injected token provider and Bearer challenge handling; token acquisition remains application-owned |
| RFC 5939 / RFC 7044 | Implemented and tested | SDP capability selection and History-Info parsing, serialization, and privacy policy |
| Conference bridge | Implemented and tested | Bounded participants, PCM mix-minus, gain, mute and clipping |
| SCTP | Experimental and tested on Linux | Kernel SCTP backend on Linux amd64/arm64; other platforms return an explicit unsupported error |
| AKA v2 | Experimental and tested | Injected SIM/ISIM provider, RFC 4169 password/session-key derivation, challenge preference and vectors |
| Emergency profiles | Implemented and tested | RFC 5031 service URNs, priority, routing and location headers restricted to laboratory destinations |

“Implemented” means the code exists and has focused automated tests. “Integration pending” means protocol interoperability still needs validation against external SIP peers before the feature is considered stable.

## Packages

- `sip`: typed messages, SIP/SIPS URIs, name-address values, headers, parsing, and serialization.
- `auth`: Digest, OAuth Bearer, and provider-based AKAv2 authorization support.
- `transport`: TCP, UDP, TLS, WebSocket, WSS, and an experimental Linux SCTP signaling transport.
- `sdp`: SDP parsing, serialization, audio negotiation, ICE/DTLS attributes, and capability selection.
- `media`: RTP/RTCP, G.711, G.722, iLBC, optional GSM-FR/Opus/SILK, DTMF, jitter buffering, DTLS-SRTP, and conference mixing.
- `nat`: ICE candidate gathering and connectivity using STUN or TURN servers.
- `identity`: experimental ES256 SIP Identity signing and verification.
- `client`: registration, request dispatch, dialogs, DNS failover, separate bind/advertised addresses, and the compatibility API.
- `types`: callback data retained for compatibility with earlier `v0.2` releases.

## Development

```bash
go test ./...
go test -race ./...
go vet ./...
```

The player is validated separately because it needs native audio headers:

```bash
cd example/player
go test ./...
```

Optional native codec builds and their distribution terms are documented in [docs/codecs.md](docs/codecs.md).
The separation between signaling TLS, ICE/TURN, and DTLS-SRTP is documented in [docs/security.md](docs/security.md).
Examples for calls, presence, messaging, transfer, ICE, protected media, and emergency laboratory profiles are in [docs/api-examples.md](docs/api-examples.md).

Pull requests and pushes to `main` run the same checks on Linux, Windows, macOS, Go 1.22.2, and the current stable Go release. The weekly integration workflow adds prolonged fuzzing, repeated concurrent transactions, reconnect stress, lifecycle stress, and goroutine-leak detection. Pushing a semantic-version tag runs validation, fixed-version Asterisk interoperability, local TURN/DTLS-SRTP scenarios, automatic Go module publication through the public proxy, and an external-consumer smoke test before creating a GitHub Release.

## Versioning and releases

The module follows semantic versioning. Tags must use `vX.Y.Z` or a valid prerelease such as `v0.3.0-rc.1`. Do not move published tags; release a new version instead.

See [docs/releasing.md](docs/releasing.md) for the release process and [CHANGELOG.md](CHANGELOG.md) for notable changes.

## License

This project is licensed under the [MIT License](LICENSE). It permits commercial and private use, modification, distribution, and sublicensing, subject to preserving the copyright and license notices.
