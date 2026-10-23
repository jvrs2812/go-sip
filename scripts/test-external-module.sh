#!/usr/bin/env bash

set -euo pipefail

repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
consumer_directory="$(mktemp -d)"

cleanup() {
  rm -rf "$consumer_directory"
}

trap cleanup EXIT

cd "$consumer_directory"

go mod init example.test/go-sip-consumer
go mod edit -replace "github.com/jvrs2812/go-sip=$repository_root"
go get github.com/jvrs2812/go-sip

cat > main.go <<'EOF'
package main

import (
    "fmt"

    "github.com/jvrs2812/go-sip/client"
    "github.com/jvrs2812/go-sip/presence"
    "github.com/jvrs2812/go-sip/sip"
    "github.com/jvrs2812/go-sip/transaction"
)

func main() {
    request := sip.NewRequest("OPTIONS", "sip:service@example.test")
    state := transaction.NewClient(request.Method, false).State()
    profile := client.EmergencyProfile{
        ServiceURN:      client.EmergencySOS,
        LaboratoryRoute: "sip:lab@127.0.0.1;lr",
    }

    if err := profile.Validate(); err != nil {
        panic(err)
    }

    fmt.Println(request.Method, state, presence.PIDFContentType)
}
EOF

go mod tidy
go build ./...
