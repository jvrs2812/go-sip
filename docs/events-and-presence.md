# Events, presence, messaging, and transfers

The client supports the subscriber and publisher roles defined by SIP events. It does not act as a presence server or compose presence documents from several publishers.

## Presence subscription

`NewSubscription` creates a dialog-forming SUBSCRIBE, accepts an early NOTIFY, negotiates `423 Min-Expires`, tracks the remote target and route set, and validates the event package and both dialog tags. Refreshes preserve the Call-ID and local tag while increasing CSeq. `Cancel` sends `Expires: 0`; the subscription remains able to receive the final terminated NOTIFY.

```go
subscription, err := sipClient.NewSubscription(ctx, "sip:alice@example.com", "presence", 10*time.Minute)
if err != nil {
    return err
}

for notification := range subscription.Notifications() {
    document, err := presence.ParsePIDF(notification.Body)
    if err != nil {
        continue
    }

    fmt.Println(document.Entity, subscription.Status())
}
```

PIDF from RFC 3863 is the interoperable presence format. Package `presence` models the entity, tuples, `open`/`closed` basic state, contacts, priorities, notes, languages, and timestamps. The parser requires the standard PIDF namespace and validates tuple identifiers and contact priorities.

## Presence publication

`NewPublication` sends the initial PUBLISH and requires the server to return `SIP-ETag`. `Refresh` sends no body, `Update` sends a replacement body, and both use `SIP-If-Match`. A `412` is returned as `ErrPublicationVersionMismatch`; `423` is retried with `Min-Expires`. `Remove` sends `Expires: 0` and releases the local timer.

```go
body, err := presence.MarshalPIDF(document)
if err != nil {
    return err
}

publication, err := sipClient.NewPublication(
    ctx,
    "sip:alice@example.com",
    "presence",
    presence.PIDFContentType,
    body,
    10*time.Minute,
)
if err != nil {
    return err
}

defer publication.Remove(context.Background())
```

## MESSAGE and INFO

Inbound MESSAGE bodies default to a 64 KiB limit. `Config.MaxMessageBodyBytes` changes it, and `Config.AcceptedMessageTypes` controls accepted media types. Oversized, unsupported, and unhandled requests receive `413`, `415`, and `501` respectively. MESSAGE covers SIP page-mode messages; MSRP sessions are outside this module.

`Config.AcceptedInfoTypes` can restrict INFO media types. `Call.SendDTMFInfo` sends `application/dtmf-relay`; RTP telephone events remain available through `Call.SendDTMF` when SDP negotiated `telephone-event`.

## REFER

`Call.Transfer` starts a blind transfer. `Call.AttendedTransfer` adds a percent-encoded `Replaces` dialog identifier for an attended transfer. The returned `Transfer` validates in-dialog NOTIFY requests, parses `message/sipfrag`, and exposes pending, active, succeeded, failed, or expired state.

```go
transfer, err := call.Transfer(ctx, "sip:operator@example.com")
if err != nil {
    return err
}

for range transfer.Notifications() {
    fmt.Println(transfer.Status(), transfer.ResponseCode())
}
```

Notification channels are bounded. If an application stops consuming one, the signaling reader remains live and reports the dropped notification through `OnError`.
