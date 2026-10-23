//go:build !opus || !cgo

package media

import (
	"errors"
	"testing"
	"time"
)

func TestOpusReportsUnavailableWithoutBuildTag(t *testing.T) {
	if OpusAvailable() {
		t.Fatal("Opus must not be advertised without its native backend")
	}

	if _, err := NewOpus(48000, 1, 20*time.Millisecond); !errors.Is(err, ErrCodecUnavailable) {
		t.Fatalf("NewOpus() error = %v", err)
	}
}
