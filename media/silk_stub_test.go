//go:build !silk || !cgo

package media

import (
	"errors"
	"testing"
)

func TestSILKReportsUnavailableWithoutBuildTag(t *testing.T) {
	if SILKAvailable() {
		t.Fatal("SILK must not be advertised without its codec backend")
	}

	if _, err := NewSILK(16000, 20000); !errors.Is(err, ErrCodecUnavailable) {
		t.Fatalf("NewSILK() error = %v", err)
	}
}
