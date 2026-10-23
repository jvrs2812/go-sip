package media

import "testing"

func TestG722EncodeDecodeFrame(t *testing.T) {
	codec := NewG722()
	samples := make([]int16, codec.FrameSamples())

	for index := range samples {
		samples[index] = int16((index - len(samples)/2) * 100)
	}

	payload, err := codec.EncodePCM(samples)
	if err != nil {
		t.Fatal(err)
	}

	if len(payload) != 160 {
		t.Fatalf("G.722 payload length = %d, want 160", len(payload))
	}

	decoded, err := codec.DecodePCM(payload)
	if err != nil {
		t.Fatal(err)
	}

	if len(decoded) != len(samples) {
		t.Fatalf("decoded sample count = %d, want %d", len(decoded), len(samples))
	}

	if codec.PayloadType() != 9 || codec.RTPClockRate() != 8000 || codec.SampleRate() != 16000 {
		t.Fatal("unexpected G.722 RTP parameters")
	}
}
