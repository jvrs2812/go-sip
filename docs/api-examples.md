# Public API examples

These examples use only exported packages. The CI also compiles a clean external module through `scripts/test-external-module.sh` so accidental dependencies on `internal` packages fail validation.

## Registration and calls

Create one client for each SIP account and close it when that account is no longer needed.

```go
sipClient, err := client.NewClient(client.Config{
	ServerHost: "pbx.example.test",
	ServerPort: 5060,
	Username:   "1001",
	Password:   "secret",
	Network:    transport.UDP,
})
if err != nil {
	return err
}

defer sipClient.Close()

if err := sipClient.Register(ctx); err != nil {
	return err
}

call, err := sipClient.Invite(ctx, "sip:1002@pbx.example.test")
if err != nil {
	return err
}

defer call.Hangup(context.Background())
```

`OnEarlyMedia` reports a provisional response with usable SDP. `OnCallEstablished` reports the confirmed SIP dialog. `OnFirstMedia` and the legacy `OnStartCall` report the first RTP packet.

## Presence subscription and publication

Build PIDF through the public `presence` package and keep the returned lifecycle objects for refresh and cleanup.

```go
document := presence.Document{
	Entity: "sip:1001@example.test",
	Tuples: []presence.Tuple{
		{
			ID:    "phone",
			Basic: presence.BasicOpen,
		},
	},
}

body, err := presence.MarshalPIDF(document)
if err != nil {
	return err
}

publication, err := sipClient.NewPublication(
	ctx,
	"sip:1001@example.test",
	"presence",
	presence.PIDFContentType,
	body,
	10*time.Minute,
)
if err != nil {
	return err
}

defer publication.Remove(context.Background())

subscription, err := sipClient.NewSubscription(
	ctx,
	"sip:1002@example.test",
	"presence",
	10*time.Minute,
)
if err != nil {
	return err
}

defer subscription.Cancel(context.Background())
```

Read `subscription.Notifications()` for validated NOTIFY events. A publication update sends its current SIP entity tag automatically and returns `client.ErrPublicationVersionMismatch` after a `412` response.

## Messaging, DTMF, and transfer

```go
if err := sipClient.SendMessage(
	ctx,
	"sip:1002@example.test",
	"text/plain; charset=utf-8",
	[]byte("hello"),
); err != nil {
	return err
}

if err := call.SendDTMF(ctx, '5', 160*time.Millisecond); err != nil {
	return err
}

transfer, err := call.Transfer(ctx, "sip:1003@example.test")
if err != nil {
	return err
}

for transfer.Status() == client.TransferPending || transfer.Status() == client.TransferActive {
	event := <-transfer.Notifications()
	_ = event
}
```

`SendDTMFInfo` is available when the peer negotiates SIP INFO instead of RTP telephone events. `AttendedTransfer` generates a `Replaces` parameter from another established call.

## ICE and protected media

Exchange ICE credentials and candidates through SDP, connect the selected pair, then perform DTLS-SRTP over that connection.

```go
agent, err := nat.NewICEAgent(nat.ICEConfig{
	Servers: []nat.ICEServer{
		{
			URL:        "turn:turn.example.test:3478?transport=udp",
			Username:   turnUsername,
			Credential: turnPassword,
		},
	},
})
if err != nil {
	return err
}

defer agent.Close()

candidates, err := agent.Gather(ctx)
if err != nil {
	return err
}

credentials, err := agent.Credentials()
if err != nil {
	return err
}

_ = candidates
_ = credentials
```

After `agent.Connect`, adapt the connected ICE socket with `media.NewConnectedPacketConn`. Pass that packet connection to `media.EstablishDTLSSRTP` together with the remote SDP fingerprint. Closing `SecureRTP` releases the DTLS/SRTP session and its packet connection.

## Emergency laboratory profile

Emergency helpers reject public routes. Use loopback, private address space, or reserved test domains.

```go
call, err := sipClient.InviteEmergency(ctx, client.EmergencyProfile{
	ServiceURN:              client.EmergencySOS,
	LaboratoryRoute:         "sip:emergency@127.0.0.1:5060;lr",
	GeolocationURI:          "cid:location@example.test",
	AllowLocationForRouting: true,
})
if err != nil {
	return err
}

defer call.Hangup(context.Background())
```

The profile exists for controlled interoperability tests. It must not target a real emergency service.
