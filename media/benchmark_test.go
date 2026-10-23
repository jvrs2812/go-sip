package media

import "testing"

func BenchmarkPCMUFrameRoundTrip(b *testing.B) {
	codec := PCMU{}
	pcm := make([]byte, 320)

	b.ReportAllocs()
	b.SetBytes(int64(len(pcm)))

	for index := 0; index < b.N; index++ {
		encoded := codec.Encode(pcm)
		_ = codec.Decode(encoded)
	}
}

func BenchmarkRTPPacketMarshalParse(b *testing.B) {
	packet := RTPPacket{
		Version:        2,
		PayloadType:    0,
		SequenceNumber: 42,
		Timestamp:      160,
		SSRC:           7,
		Payload:        make([]byte, 160),
	}

	b.ReportAllocs()
	b.SetBytes(int64(len(packet.Payload)))

	for index := 0; index < b.N; index++ {
		encoded, err := packet.MarshalBinary()
		if err != nil {
			b.Fatal(err)
		}

		if _, err := ParseRTPPacket(encoded); err != nil {
			b.Fatal(err)
		}
	}
}
