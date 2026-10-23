package media

import (
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/pion/rtcp"
)

type ReceptionMetrics struct {
	SourceSSRC              uint32
	PacketsReceived         uint64
	PacketsExpected         uint64
	PacketsLost             uint64
	FractionLost            uint8
	ExtendedHighestSequence uint32
	InterarrivalJitter      uint32
}

type ReceptionTracker struct {
	mu        sync.Mutex
	clockRate uint32
	started   bool
	ssrc      uint32
	base      uint16
	maximum   uint16
	cycles    uint32
	received  uint64

	expectedPrior uint64
	receivedPrior uint64
	transit       int64
	jitter        float64
}

func NewReceptionTracker(clockRate uint32) *ReceptionTracker {
	if clockRate == 0 {
		clockRate = 8000
	}

	return &ReceptionTracker{clockRate: clockRate}
}

func (t *ReceptionTracker) Observe(packet RTPPacket, arrival time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if !t.started {
		t.started = true
		t.ssrc = packet.SSRC
		t.base = packet.SequenceNumber
		t.maximum = packet.SequenceNumber
		t.transit = arrivalTimestamp(arrival, t.clockRate) - int64(packet.Timestamp)
		t.received = 1

		return
	}

	if packet.SSRC != t.ssrc {
		return
	}

	if packet.SequenceNumber < t.maximum && t.maximum-packet.SequenceNumber > 1<<15 {
		t.cycles += 1 << 16
		t.maximum = packet.SequenceNumber
	} else if sequenceDistance(t.maximum, packet.SequenceNumber) < 1<<15 && packet.SequenceNumber != t.maximum {
		t.maximum = packet.SequenceNumber
	}

	t.received++

	transit := arrivalTimestamp(arrival, t.clockRate) - int64(packet.Timestamp)
	difference := transit - t.transit
	if difference < 0 {
		difference = -difference
	}

	t.transit = transit
	t.jitter += (float64(difference) - t.jitter) / 16
}

func (t *ReceptionTracker) Snapshot() ReceptionMetrics {
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.snapshotLocked(true)
}

func (t *ReceptionTracker) ReceiverReport(localSSRC uint32) ([]byte, error) {
	t.mu.Lock()
	metrics := t.snapshotLocked(true)
	t.mu.Unlock()

	report := &rtcp.ReceiverReport{
		SSRC: localSSRC,
		Reports: []rtcp.ReceptionReport{{
			SSRC:               metrics.SourceSSRC,
			FractionLost:       metrics.FractionLost,
			TotalLost:          uint32(min(metrics.PacketsLost, (1<<24)-1)),
			LastSequenceNumber: metrics.ExtendedHighestSequence,
			Jitter:             metrics.InterarrivalJitter,
		}},
	}

	encoded, err := report.Marshal()
	if err != nil {
		return nil, fmt.Errorf("marshal RTCP receiver report: %w", err)
	}

	return encoded, nil
}

func (t *ReceptionTracker) snapshotLocked(updateInterval bool) ReceptionMetrics {
	if !t.started {
		return ReceptionMetrics{}
	}

	extendedMaximum := t.cycles + uint32(t.maximum)
	expected := uint64(extendedMaximum-uint32(t.base)) + 1
	lost := uint64(0)
	if expected > t.received {
		lost = expected - t.received
	}

	expectedInterval := expected - t.expectedPrior
	receivedInterval := t.received - t.receivedPrior
	lostInterval := uint64(0)
	if expectedInterval > receivedInterval {
		lostInterval = expectedInterval - receivedInterval
	}

	fractionLost := uint8(0)
	if expectedInterval > 0 && lostInterval > 0 {
		fraction := (lostInterval << 8) / expectedInterval
		fractionLost = uint8(min(fraction, 255))
	}

	if updateInterval {
		t.expectedPrior = expected
		t.receivedPrior = t.received
	}

	return ReceptionMetrics{
		SourceSSRC:              t.ssrc,
		PacketsReceived:         t.received,
		PacketsExpected:         expected,
		PacketsLost:             lost,
		FractionLost:            fractionLost,
		ExtendedHighestSequence: extendedMaximum,
		InterarrivalJitter:      uint32(math.Round(t.jitter)),
	}
}

func arrivalTimestamp(arrival time.Time, clockRate uint32) int64 {
	return arrival.UnixNano() * int64(clockRate) / int64(time.Second)
}
