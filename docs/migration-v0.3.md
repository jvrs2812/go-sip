# Migration to the current v0.3 API

The current tree keeps the earlier `Client` fields and helpers where source compatibility is practical, while the context-based API is the supported path for new code.

## Client construction

Replace a value assembled from public legacy fields with `NewClient(Config)`. Configuration validation then happens before network activity.

```go
sipClient, err := client.NewClient(client.Config{
    ServerHost: "sip.example.com",
    ServerPort: 5060,
    Username:   "1001",
    Password:   "secret",
})
```

`IpServer`, `PortServer`, `PortForRtp`, `Ramal`, `Password`, `RegisterSip`, and `HandleAuth` remain for v0.2 source compatibility. They may be removed when the API is stabilized for v1.

## Lifecycle and errors

Use `Start`, `Register`, `Unregister`, and `Close`. Supply deadlines through `context.Context`; network and protocol failures are returned rather than only logged. `OnError` remains useful for asynchronous transport, callback-queue, and reconnect errors.

The client owns its signaling connection and media sessions. Call `Close` once the account is no longer used. Independent accounts use independent clients and connections.

## Calls

Use `Invite` for outbound calls and the `Call` methods `Accept`, `Reject`, `Hangup`, `Hold`, `Resume`, `SendAudio`, `SendDTMF`, and `SendDTMFInfo`. Dialog requests now validate both tags, preserve route sets, and maintain CSeq.

`OnCallEstablished` means the SIP dialog was confirmed. `OnEarlyMedia` reports a provisional response with usable SDP. `OnFirstMedia` means the first RTP packet arrived. The legacy `OnStartCall` callback is retained and has the same first-RTP meaning as `OnFirstMedia`; it never means that ACK was received.

## Events and presence

The stateless `Subscribe` and `Publish` methods remain available. New code should use `NewSubscription` and `NewPublication` to obtain lifecycle objects with refresh, expiry, cancellation or removal, dialog validation, and entity-tag control. PIDF types moved into the public `presence` package.

## Addressing and transports

`LocalAddress` selects the bind address. `AdvertisedAddress` controls Via, Contact, and SDP advertisement. DNS discovery now resolves NAPTR, SRV, A, and AAAA records and caches results. TLS and WSS validate certificates and hostnames unless the caller explicitly supplies a different `tls.Config` policy.

Optional GSM-FR, Opus, and SILK support is selected at build time. Applications should not assume those codecs are advertised unless the matching build tag and native dependency are present.
