//go:build gsm && cgo

package media

import "testing"

func TestGSMEncodeDecodeFrame(t *testing.T) {
	codec, err := NewGSM()
	if err != nil {
		t.Fatal(err)
	}
	defer codec.Close()

	samples := make([]int16, GSMFrameSamples)
	for index := range samples {
		samples[index] = int16((index - 80) * 200)
	}

	payload, err := codec.EncodePCM(samples)
	if err != nil {
		t.Fatal(err)
	}

	decoded, err := codec.DecodePCM(payload)
	if err != nil {
		t.Fatal(err)
	}

	if len(payload) != GSMFrameBytes || len(decoded) != GSMFrameSamples {
		t.Fatalf("payload bytes = %d, decoded samples = %d", len(payload), len(decoded))
	}
}
