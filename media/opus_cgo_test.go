//go:build opus && cgo

package media

import (
	"math"
	"testing"
	"time"
)

func TestOpusEncodeDecodeAndConceal(t *testing.T) {
	codec, err := NewOpus(48000, 1, 20*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}

	samples := make([]int16, codec.FrameSamples())
	for index := range samples {
		samples[index] = int16(10000 * math.Sin(2*math.Pi*440*float64(index)/48000))
	}

	payload, err := codec.EncodePCM(samples)
	if err != nil {
		t.Fatal(err)
	}

	decoded, err := codec.DecodePCM(payload)
	if err != nil {
		t.Fatal(err)
	}

	if len(payload) == 0 || len(decoded) != len(samples) {
		t.Fatalf("payload bytes = %d, decoded samples = %d", len(payload), len(decoded))
	}

	concealed, err := codec.Conceal()
	if err != nil {
		t.Fatal(err)
	}

	if len(concealed) != len(samples) {
		t.Fatalf("concealed samples = %d", len(concealed))
	}
}
