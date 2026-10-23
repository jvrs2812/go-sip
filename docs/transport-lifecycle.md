# Signaling transport lifecycle

Each `Client` owns one active signaling transport and one reader goroutine. The transport is identified by its network, local bind address, remote target, TLS policy, WebSocket path, and headers. Responses are then routed to transactions by Call-ID, CSeq method, and top Via branch.

The library deliberately does not share a TCP, TLS, or WebSocket connection between independent `Client` values. Sharing would require a common dispatcher plus explicit ownership of registrations, dialogs, authentication state, callbacks, and shutdown. A pool that only reuses `net.Conn` values would allow one client to consume another client's messages and recreate the isolation defect fixed in this release.

Within one client, the connection is reused by all requests. Writes are serialized, there is one reader, and `Close` releases the connection and waits for owned goroutines. Transport loss fails current waiters, reconnects with bounded exponential backoff, and restores registration when registration remains desired. DNS targets are cached separately and tried within one total failover deadline.

Applications that need several accounts should create one client per account. This gives every account an independent connection lifetime and bounded request maps. A future shared connection feature would need a multiplexing owner above `Client`, an explicit maximum-idle policy, reference counting for every dialog and registration, and integration tests proving that messages cannot cross account boundaries.
