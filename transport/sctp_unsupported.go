//go:build !linux || 386

package transport

import (
	"context"
	"fmt"
	"runtime"
)

func dialSCTP(_ context.Context, _ Config) (Transport, error) {
	return nil, fmt.Errorf("transport: SCTP is unsupported on %s/%s", runtime.GOOS, runtime.GOARCH)
}
