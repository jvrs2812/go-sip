package media

import "testing"

func TestJitterBufferReordersAndAccountsForLoss(t *testing.T) {
	buffer := NewJitterBuffer(2)

	ready := buffer.Push(RTPPacket{SequenceNumber: 10})
	assertSequences(t, ready, 10)

	if ready := buffer.Push(RTPPacket{SequenceNumber: 12}); len(ready) != 0 {
		t.Fatalf("unexpected packet before gap is resolved: %#v", ready)
	}

	ready = buffer.Push(RTPPacket{SequenceNumber: 11})
	assertSequences(t, ready, 11, 12)

	if ready := buffer.Push(RTPPacket{SequenceNumber: 15}); len(ready) != 0 {
		t.Fatalf("unexpected packet before buffer reaches capacity: %#v", ready)
	}

	ready = buffer.Push(RTPPacket{SequenceNumber: 16})
	assertSequences(t, ready, 15, 16)

	stats := buffer.Stats()
	if stats.Lost != 2 || stats.Delivered != 5 {
		t.Fatalf("unexpected jitter stats: %#v", stats)
	}
}

func TestJitterBufferHandlesSequenceWrap(t *testing.T) {
	buffer := NewJitterBuffer(3)

	assertSequences(t, buffer.Push(RTPPacket{SequenceNumber: 65535}), 65535)
	assertSequences(t, buffer.Push(RTPPacket{SequenceNumber: 0}), 0)
}

func assertSequences(t *testing.T, packets []RTPPacket, expected ...uint16) {
	t.Helper()

	if len(packets) != len(expected) {
		t.Fatalf("got %d packets, want %d", len(packets), len(expected))
	}

	for index := range expected {
		if packets[index].SequenceNumber != expected[index] {
			t.Fatalf("packet %d sequence = %d, want %d", index, packets[index].SequenceNumber, expected[index])
		}
	}
}
