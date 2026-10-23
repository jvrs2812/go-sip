package sdp

import (
	"net"
	"testing"
)

func BenchmarkCallAudioNegotiation(b *testing.B) {
	offer := Description{
		OriginID:      1,
		OriginVersion: 1,
		SessionName:   "benchmark",
		Connection:    net.ParseIP("192.0.2.20"),
		Media: []Media{{
			Type:       "audio",
			Port:       4000,
			Protocol:   "RTP/AVP",
			Formats:    []uint8{111, 9, 0, 8, 101},
			Connection: net.ParseIP("192.0.2.20"),
			Direction:  SendRecv,
			Codecs: map[uint8]Codec{
				111: {PayloadType: 111, Name: "opus", ClockRate: 48000, Channels: 2},
				9:   {PayloadType: 9, Name: "G722", ClockRate: 8000, Channels: 1},
				0:   {PayloadType: 0, Name: "PCMU", ClockRate: 8000, Channels: 1},
				8:   {PayloadType: 8, Name: "PCMA", ClockRate: 8000, Channels: 1},
				101: {PayloadType: 101, Name: "telephone-event", ClockRate: 8000, Channels: 1},
			},
		}},
	}
	supported := []Codec{
		{PayloadType: 9, Name: "G722", ClockRate: 8000, Channels: 1},
		{PayloadType: 0, Name: "PCMU", ClockRate: 8000, Channels: 1},
		{PayloadType: 8, Name: "PCMA", ClockRate: 8000, Channels: 1},
	}

	b.ReportAllocs()

	for index := 0; index < b.N; index++ {
		if _, _, err := NegotiateAudio(offer, net.ParseIP("192.0.2.10"), 5000, supported); err != nil {
			b.Fatal(err)
		}
	}
}
