package media

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

func TestRTPPacketWithCSRCAndExtension(t *testing.T) {
	original := RTPPacket{
		Version:        2,
		Marker:         true,
		PayloadType:    111,
		SequenceNumber: 42,
		Timestamp:      9000,
		SSRC:           7,
		CSRC:           []uint32{8, 9},
		Extension:      true,
		ExtensionID:    0xbede,
		ExtensionData:  []byte{1, 2, 3, 4},
		Payload:        []byte{5, 6, 7},
	}

	encoded, err := original.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary() error = %v", err)
	}

	parsed, err := ParseRTPPacket(encoded)
	if err != nil {
		t.Fatalf("ParseRTPPacket() error = %v", err)
	}

	if parsed.SequenceNumber != 42 || parsed.Timestamp != 9000 || len(parsed.CSRC) != 2 || len(parsed.ExtensionData) != 4 || len(parsed.Payload) != 3 {
		t.Fatalf("parsed = %#v", parsed)
	}
}

func TestRTPPacketRejectsInvalidPadding(t *testing.T) {
	data := make([]byte, 13)
	data[0] = 0xa0
	data[12] = 2
	if _, err := ParseRTPPacket(data); !errors.Is(err, ErrMalformedRTP) {
		t.Fatalf("error = %v, want ErrMalformedRTP", err)
	}
}

func TestRTPPacketPaddingRoundTrip(t *testing.T) {
	original := RTPPacket{
		Version:        2,
		Padding:        true,
		PaddingSize:    4,
		PayloadType:    0,
		SequenceNumber: 7,
		Timestamp:      160,
		SSRC:           9,
		Payload:        []byte{1, 2, 3},
	}

	encoded, err := original.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}

	parsed, err := ParseRTPPacket(encoded)
	if err != nil {
		t.Fatal(err)
	}

	if !parsed.Padding || parsed.PaddingSize != 4 || string(parsed.Payload) != string(original.Payload) {
		t.Fatalf("parsed padded packet = %#v", parsed)
	}
}

func TestRTPSessionsAreIsolatedAndBidirectional(t *testing.T) {
	first, err := NewRTPSession(RTPConfig{LocalAddr: "127.0.0.1:0", PayloadType: 0, SSRC: 1, SamplesPerPacket: 160})
	if err != nil {
		t.Fatalf("NewRTPSession(first) error = %v", err)
	}

	defer first.Close()
	second, err := NewRTPSession(RTPConfig{LocalAddr: "127.0.0.1:0", PayloadType: 8, SSRC: 2, SamplesPerPacket: 160})
	if err != nil {
		t.Fatalf("NewRTPSession(second) error = %v", err)
	}

	defer second.Close()
	if err := first.SetRemote(second.LocalAddr().String()); err != nil {
		t.Fatal(err)
	}

	if err := second.SetRemote(first.LocalAddr().String()); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := first.Send(ctx, []byte{1, 2}, true); err != nil {
		t.Fatalf("first.Send() error = %v", err)
	}

	if err := second.Send(ctx, []byte{3, 4}, false); err != nil {
		t.Fatalf("second.Send() error = %v", err)
	}

	assertPacket := func(session *RTPSession, payloadType uint8, ssrc uint32) {
		t.Helper()
		select {
		case packet := <-session.Packets():
			if packet.PayloadType != payloadType || packet.SSRC != ssrc {
				t.Fatalf("packet = %#v", packet)
			}
		case <-ctx.Done():
			t.Fatal("timed out waiting for RTP")
		}
	}

	assertPacket(second, 0, 1)
	assertPacket(first, 8, 2)
}

func TestRTPSessionCanSendBeforeReceiving(t *testing.T) {
	receiver, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	defer receiver.Close()
	session, err := NewRTPSession(RTPConfig{LocalAddr: "127.0.0.1:0", RemoteAddr: receiver.LocalAddr().String(), PayloadType: 0})
	if err != nil {
		t.Fatal(err)
	}

	defer session.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := session.Send(ctx, []byte{1}, false); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
}

func TestRTPSessionTracksReceptionAndPacesPackets(t *testing.T) {
	receiver, err := NewRTPSession(RTPConfig{LocalAddr: "127.0.0.1:0", PayloadType: 0, SSRC: 20})
	if err != nil {
		t.Fatal(err)
	}

	defer receiver.Close()

	sender, err := NewRTPSession(RTPConfig{
		LocalAddr:        "127.0.0.1:0",
		RemoteAddr:       receiver.LocalAddr().String(),
		PayloadType:      0,
		ClockRate:        8000,
		SamplesPerPacket: 160,
		SSRC:             10,
		Pacing:           true,
	})
	if err != nil {
		t.Fatal(err)
	}

	defer sender.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	started := time.Now()
	if err := sender.Send(ctx, []byte{1}, false); err != nil {
		t.Fatal(err)
	}

	if err := sender.Send(ctx, []byte{2}, false); err != nil {
		t.Fatal(err)
	}

	if elapsed := time.Since(started); elapsed < 15*time.Millisecond {
		t.Fatalf("two paced packets completed in %v", elapsed)
	}

	for index := 0; index < 2; index++ {
		select {
		case <-receiver.Packets():
		case <-ctx.Done():
			t.Fatal("timed out waiting for paced RTP")
		}
	}

	metrics := receiver.Metrics()
	if metrics.PacketsReceived != 2 || metrics.SourceSSRC != 10 {
		t.Fatalf("reception metrics = %#v", metrics)
	}

	if _, err := receiver.RTCPReceiverReport(); err != nil {
		t.Fatalf("RTCPReceiverReport() error = %v", err)
	}
}
