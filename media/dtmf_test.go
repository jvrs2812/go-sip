package media

import (
	"testing"
)

func TestTelephoneEventRoundTrip(t *testing.T) {
	original := TelephoneEvent{Event: 11, End: true, Volume: 10, Duration: 1280}
	parsed, err := ParseTelephoneEvent(original.MarshalBinary())

	if err != nil {
		t.Fatal(err)
	}

	if parsed != original {
		t.Fatalf("parsed = %#v, want %#v", parsed, original)
	}
}

func TestTelephoneEventReceiverDeduplicatesTerminalPackets(t *testing.T) {
	receiver := NewTelephoneEventReceiver(4)
	packet := RTPPacket{
		SSRC:      42,
		Timestamp: 1000,
		Payload: TelephoneEvent{
			Event:    5,
			End:      true,
			Volume:   10,
			Duration: 800,
		}.MarshalBinary(),
	}

	_, complete, err := receiver.Process(packet)
	if err != nil {
		t.Fatal(err)
	}

	if !complete {
		t.Fatal("first terminal packet should complete the event")
	}

	for repeat := 0; repeat < 2; repeat++ {
		_, complete, err := receiver.Process(packet)
		if err != nil {
			t.Fatal(err)
		}

		if complete {
			t.Fatal("repeated terminal packet should be deduplicated")
		}
	}
}

func TestDTMFEventCode(t *testing.T) {
	tests := map[rune]uint8{'0': 0, '9': 9, '*': 10, '#': 11, 'A': 12, 'd': 15}

	for digit, expected := range tests {
		actual, err := DTMFEventCode(digit)

		if err != nil || actual != expected {
			t.Fatalf("DTMFEventCode(%q) = %d, %v", digit, actual, err)
		}
	}
}
