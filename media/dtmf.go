package media

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
	"time"
)

type TelephoneEvent struct {
	Event    uint8
	End      bool
	Volume   uint8
	Duration uint16
}

type telephoneEventKey struct {
	ssrc      uint32
	timestamp uint32
	event     uint8
}

type TelephoneEventReceiver struct {
	mu        sync.Mutex
	active    map[telephoneEventKey]uint16
	completed map[telephoneEventKey]struct{}
	order     []telephoneEventKey
	maximum   int
}

func NewTelephoneEventReceiver(maximumTrackedEvents int) *TelephoneEventReceiver {
	if maximumTrackedEvents <= 0 {
		maximumTrackedEvents = 128
	}

	return &TelephoneEventReceiver{
		active:    make(map[telephoneEventKey]uint16),
		completed: make(map[telephoneEventKey]struct{}),
		maximum:   maximumTrackedEvents,
	}
}

func (r *TelephoneEventReceiver) Process(packet RTPPacket) (TelephoneEvent, bool, error) {
	event, err := ParseTelephoneEvent(packet.Payload)
	if err != nil {
		return TelephoneEvent{}, false, err
	}

	key := telephoneEventKey{ssrc: packet.SSRC, timestamp: packet.Timestamp, event: event.Event}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, duplicate := r.completed[key]; duplicate {
		return event, false, nil
	}

	if previousDuration := r.active[key]; event.Duration < previousDuration {
		return event, false, nil
	}

	r.active[key] = event.Duration

	if !event.End {
		return event, false, nil
	}

	delete(r.active, key)
	r.completed[key] = struct{}{}
	r.order = append(r.order, key)

	if len(r.order) > r.maximum {
		oldest := r.order[0]
		r.order = r.order[1:]
		delete(r.completed, oldest)
	}

	return event, true, nil
}

func ParseTelephoneEvent(payload []byte) (TelephoneEvent, error) {
	if len(payload) < 4 {
		return TelephoneEvent{}, errors.New("telephone-event payload is shorter than four bytes")
	}

	return TelephoneEvent{
		Event:    payload[0],
		End:      payload[1]&0x80 != 0,
		Volume:   payload[1] & 0x3f,
		Duration: binary.BigEndian.Uint16(payload[2:4]),
	}, nil
}

func (event TelephoneEvent) MarshalBinary() []byte {
	payload := make([]byte, 4)
	payload[0] = event.Event
	payload[1] = event.Volume & 0x3f

	if event.End {
		payload[1] |= 0x80
	}

	binary.BigEndian.PutUint16(payload[2:4], event.Duration)

	return payload
}

func DTMFEventCode(digit rune) (uint8, error) {
	switch digit {
	case '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
		return uint8(digit - '0'), nil

	case '*':
		return 10, nil

	case '#':
		return 11, nil

	case 'A', 'a':
		return 12, nil

	case 'B', 'b':
		return 13, nil

	case 'C', 'c':
		return 14, nil

	case 'D', 'd':
		return 15, nil

	default:
		return 0, fmt.Errorf("unsupported DTMF digit %q", digit)
	}
}

func (s *RTPSession) SendTelephoneEvent(ctx context.Context, payloadType, eventCode uint8, duration time.Duration) error {
	s.sendMu.Lock()
	defer s.sendMu.Unlock()

	if duration <= 0 {
		duration = 160 * time.Millisecond
	}

	s.mu.Lock()
	clockRate := s.clockRate

	if clockRate == 0 {
		clockRate = 8000
	}

	timestamp := s.timestamp
	durationUnits := uint32(duration.Seconds() * float64(clockRate))

	if durationUnits < clockRate/50 {
		durationUnits = clockRate / 50
	}

	if durationUnits > 0xffff {
		durationUnits = 0xffff
	}

	step := clockRate / 50

	if step == 0 {
		step = 160
	}

	s.mu.Unlock()

	marker := true

	for current := step; current < durationUnits; current += step {
		event := TelephoneEvent{Event: eventCode, Volume: 10, Duration: uint16(current)}

		if err := s.sendWithTimestamp(ctx, event.MarshalBinary(), payloadType, timestamp, marker); err != nil {
			return err
		}

		marker = false

		if err := waitContext(ctx, 20*time.Millisecond); err != nil {
			return err
		}
	}

	finalEvent := TelephoneEvent{Event: eventCode, End: true, Volume: 10, Duration: uint16(durationUnits)}

	for repeat := 0; repeat < 3; repeat++ {
		if err := s.sendWithTimestamp(ctx, finalEvent.MarshalBinary(), payloadType, timestamp, false); err != nil {
			return err
		}
	}

	s.mu.Lock()
	s.timestamp = timestamp + durationUnits
	s.mu.Unlock()

	return nil
}

func (s *RTPSession) sendWithTimestamp(ctx context.Context, payload []byte, payloadType uint8, timestamp uint32, marker bool) error {
	s.mu.Lock()
	remote := s.remote

	if remote == nil {
		s.mu.Unlock()

		return errors.New("RTP remote address is not configured")
	}

	sequence := s.sequence
	s.sequence++
	ssrc := s.ssrc
	s.mu.Unlock()

	packet := RTPPacket{
		Version:        2,
		Marker:         marker,
		PayloadType:    payloadType,
		SequenceNumber: sequence,
		Timestamp:      timestamp,
		SSRC:           ssrc,
		Payload:        payload,
	}

	encoded, err := packet.MarshalBinary()

	if err != nil {
		return err
	}

	if deadline, ok := ctx.Deadline(); ok {
		_ = s.conn.SetWriteDeadline(deadline)
	} else {
		_ = s.conn.SetWriteDeadline(time.Time{})
	}

	written, err := s.conn.WriteTo(encoded, remote)

	if err != nil {
		return err
	}

	if written != len(encoded) {
		return fmt.Errorf("RTP short write: wrote %d of %d bytes", written, len(encoded))
	}

	return nil
}

func waitContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()

	select {
	case <-timer.C:
		return nil

	case <-ctx.Done():
		return ctx.Err()
	}
}
