# Contributing

Contributions are welcome while the public API is being stabilized.

## Local checks

Use Go 1.22.2 or newer, then run:

```bash
go mod tidy
go test ./...
go test -race ./...
go vet ./...
```

Run `gofmt` on changed Go files. Keep a blank line between logical blocks so control flow and error paths remain easy to scan.

The optional player has its own module and native Linux dependency:

```bash
sudo apt-get install libasound2-dev pkg-config
cd example/player
go mod tidy
go test ./...
```

## Pull requests

- Add a focused regression test for a bug or a protocol test for new behavior.
- Update the README status table and changelog when behavior changes.
- Keep protocol parsing bounded and avoid logging credentials or authorization headers.
- Preserve compatibility wrappers when practical during the `v0` series.
- Document interoperability peers and versions for external integration tests.

## Protocol work

State the RFC and section that define new behavior. Tests should cover success, malformed input, timeout or rejection, and resource cleanup. A generated header alone does not complete a protocol feature; the corresponding state transition and peer behavior must also be verified.
