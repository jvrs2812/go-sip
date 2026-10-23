package media

import "testing"

func TestILBCModesEncodeDecodeAndConceal(t *testing.T) {
	for _, mode := range []int{20, 30} {
		codec, err := NewILBC(mode, true)
		if err != nil {
			t.Fatal(err)
		}

		samples := make([]int16, codec.FrameSamples())
		for index := range samples {
			samples[index] = int16((index%40 - 20) * 200)
		}

		payload, err := codec.EncodePCM(samples)
		if err != nil {
			t.Fatal(err)
		}

		if len(payload) != codec.FrameBytes() {
			t.Fatalf("mode %d payload length = %d, want %d", mode, len(payload), codec.FrameBytes())
		}

		decoded, err := codec.DecodePCM(payload)
		if err != nil {
			t.Fatal(err)
		}

		if len(decoded) != codec.FrameSamples() {
			t.Fatalf("mode %d decoded samples = %d, want %d", mode, len(decoded), codec.FrameSamples())
		}

		concealed, err := codec.Conceal()
		if err != nil {
			t.Fatal(err)
		}

		if len(concealed) != codec.FrameSamples() {
			t.Fatalf("mode %d concealed samples = %d, want %d", mode, len(concealed), codec.FrameSamples())
		}
	}
}
