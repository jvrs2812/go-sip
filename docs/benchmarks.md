# Reproducible benchmarks

Run the benchmark suite from the module root:

```bash
go test -run='^$' -bench=. -benchmem -count=3 ./auth ./sip ./sdp ./media
```

The current baseline was measured on 23 October 2026 with Go `1.26.8-X:nodwarf5`, Linux `6.19.10-300.fc44.x86_64`, `linux/amd64`, and an AMD Ryzen 7 5700X. The table reports the middle result from three consecutive runs. These are local microbenchmarks and are useful for detecting regressions; they are not network-capacity guarantees.

| Scenario | Workload per operation | Time | Memory | Allocations |
| --- | --- | ---: | ---: | ---: |
| REGISTER Digest SHA-256 | Build one authenticated REGISTER authorization value | 3.02 µs | 2,241 B | 36 |
| SIP parser | Parse one 261-byte INVITE | 2.73 µs | 5,872 B | 41 |
| SIP serializer | Serialize one six-header INVITE | 2.15 µs | 1,200 B | 33 |
| Call SDP negotiation | Select audio from five offered payload formats | 1.57 µs | 1,152 B | 22 |
| G.711 audio | Encode and decode one 20 ms, 8 kHz mono PCM frame | 0.76 µs | 480 B | 2 |
| RTP | Serialize and parse one packet with a 160-byte payload | 0.106 µs | 336 B | 2 |

CI can retain comparable output with:

```bash
go test -run='^$' -bench=. -benchmem ./auth ./sip ./sdp ./media | tee benchmarks.txt
```

Compare results only when the Go version, CPU policy, architecture, and benchmark payload are equivalent. End-to-end registration and call latency depend on the signaling peer, DNS, transport, authentication challenge count, media path, and network delay; integration tests measure correctness rather than publishing a fixed latency claim.
