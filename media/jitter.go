package media

import "sync"

type JitterBufferStats struct {
	Buffered   int
	Delivered  uint64
	Lost       uint64
	Duplicates uint64
	Late       uint64
}

type JitterBuffer struct {
	mu       sync.Mutex
	capacity int
	started  bool
	expected uint16
	packets  map[uint16]RTPPacket
	stats    JitterBufferStats
}

func NewJitterBuffer(capacity int) *JitterBuffer {
	if capacity < 2 {
		capacity = 2
	}

	return &JitterBuffer{
		capacity: capacity,
		packets:  make(map[uint16]RTPPacket, capacity),
	}
}

func (b *JitterBuffer) Push(packet RTPPacket) []RTPPacket {
	b.mu.Lock()
	defer b.mu.Unlock()

	if !b.started {
		b.started = true
		b.expected = packet.SequenceNumber
	}

	distance := sequenceDistance(b.expected, packet.SequenceNumber)
	if distance >= 1<<15 {
		b.stats.Late++

		return nil
	}

	if _, exists := b.packets[packet.SequenceNumber]; exists {
		b.stats.Duplicates++

		return nil
	}

	b.packets[packet.SequenceNumber] = packet

	ready := b.drainReady()
	if len(b.packets) < b.capacity {
		b.stats.Buffered = len(b.packets)

		return ready
	}

	next, distance, found := b.nearestPacket()
	if !found {
		b.stats.Buffered = 0

		return ready
	}

	b.stats.Lost += uint64(distance)
	b.expected = next
	ready = append(ready, b.drainReady()...)
	b.stats.Buffered = len(b.packets)

	return ready
}

func (b *JitterBuffer) Flush() []RTPPacket {
	b.mu.Lock()
	defer b.mu.Unlock()

	ready := make([]RTPPacket, 0, len(b.packets))

	for len(b.packets) > 0 {
		next, distance, found := b.nearestPacket()
		if !found {
			break
		}

		b.stats.Lost += uint64(distance)
		b.expected = next
		ready = append(ready, b.drainReady()...)
	}

	b.stats.Buffered = 0

	return ready
}

func (b *JitterBuffer) Stats() JitterBufferStats {
	b.mu.Lock()
	defer b.mu.Unlock()

	stats := b.stats
	stats.Buffered = len(b.packets)

	return stats
}

func (b *JitterBuffer) drainReady() []RTPPacket {
	ready := make([]RTPPacket, 0)

	for {
		packet, exists := b.packets[b.expected]
		if !exists {
			break
		}

		delete(b.packets, b.expected)
		ready = append(ready, packet)
		b.expected++
		b.stats.Delivered++
	}

	return ready
}

func (b *JitterBuffer) nearestPacket() (uint16, uint16, bool) {
	var selected uint16
	var selectedDistance uint16
	found := false

	for sequenceNumber := range b.packets {
		distance := sequenceDistance(b.expected, sequenceNumber)
		if distance >= 1<<15 {
			continue
		}

		if !found || distance < selectedDistance {
			selected = sequenceNumber
			selectedDistance = distance
			found = true
		}
	}

	return selected, selectedDistance, found
}

func sequenceDistance(from, to uint16) uint16 {
	return to - from
}
