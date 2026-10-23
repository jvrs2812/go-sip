//go:build !gsm || !cgo

package media

import (
	"errors"
	"testing"
)

func TestGSMReportsUnavailableWithoutBuildTag(t *testing.T) {
	if GSMAvailable() {
		t.Fatal("GSM-FR must not be advertised without its native backend")
	}

	if _, err := NewGSM(); !errors.Is(err, ErrCodecUnavailable) {
		t.Fatalf("NewGSM() error = %v", err)
	}
}
