# Emergency-call laboratory profile

Emergency-call tests must use an isolated laboratory proxy. The library rejects `InviteEmergency` when the route is not loopback, private IP space, `.test`, or `.invalid`. This prevents automated examples and CI from targeting a public emergency service.

The supported service identifiers are RFC 5031 URNs such as:

- `urn:service:sos`
- `urn:service:sos.ambulance`
- `urn:service:sos.fire`
- `urn:service:sos.police`

The request uses the service URN as the Request-URI, routes it through the configured laboratory SIP URI, adds `Priority: emergency`, and optionally carries an RFC 6442 `Geolocation` reference with `Geolocation-Routing: yes` or `no`. Location-by-reference supports `https:` and `cid:` values. The application owns location acquisition, authorization, lifetime, and the referenced document.

```go
call, err := sipClient.InviteEmergency(ctx, client.EmergencyProfile{
    ServiceURN:              client.EmergencyFire,
    LaboratoryRoute:         "sips:esrp@192.168.50.10:5061;lr",
    GeolocationURI:          "https://location.invalid/session/1234",
    AllowLocationForRouting: true,
})
```

The laboratory proxy should be configured to return deterministic provisional and final responses, record the Request-URI and routing headers, and send media only to the test network. Recommended scenarios are successful routing, unknown service (`404`), policy rejection (`403`), no route (`380` or locally selected failure), cancellation, TLS validation failure, and location-reference rejection.

This profile supplies SIP routing and location-reference headers. It does not implement a jurisdiction-specific emergency architecture, civic-location validation, LoST, PIDF-LO generation, or a promise that a public safety answering point will accept the call. Those responsibilities require deployment-specific components and certification outside this library.
