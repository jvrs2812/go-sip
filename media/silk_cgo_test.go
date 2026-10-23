//go:build silk && cgo

package media

import "testing"

func TestSILKEncodeDecodeFrame(t *testing.T) {
	codec, err := NewSILK(16000, 20000)
	if err != nil {
		t.Fatal(err)
	}

	samples := make([]int16, codec.FrameSamples())
	for index := range samples {
		samples[index] = int16((index%80 - 40) * 250)
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
}
