package media

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"
)

var ErrMalformedRTP = errors.New("malformed RTP packet")

type RTPPacket struct {
	Version        uint8
	Padding        bool
	Extension      bool
	Marker         bool
	PayloadType    uint8
	SequenceNumber uint16
	Timestamp      uint32
	SSRC           uint32
	CSRC           []uint32
	ExtensionID    uint16
	ExtensionData  []byte
	PaddingSize    uint8
	Payload        []byte
	RemoteAddr     net.Addr
}

func ParseRTPPacket(data []byte) (RTPPacket, error) {
	if len(data) < 12 {
		return RTPPacket{}, ErrMalformedRTP
	}

	version := data[0] >> 6
	if version != 2 {
		return RTPPacket{}, fmt.Errorf("%w: unsupported version %d", ErrMalformedRTP, version)
	}

	csrcCount := int(data[0] & 0x0f)
	offset := 12 + csrcCount*4
	if len(data) < offset {
		return RTPPacket{}, fmt.Errorf("%w: truncated CSRC list", ErrMalformedRTP)
	}

	packet := RTPPacket{
		Version:        version,
		Padding:        data[0]&0x20 != 0,
		Extension:      data[0]&0x10 != 0,
		Marker:         data[1]&0x80 != 0,
		PayloadType:    data[1] & 0x7f,
		SequenceNumber: binary.BigEndian.Uint16(data[2:4]),
		Timestamp:      binary.BigEndian.Uint32(data[4:8]),
		SSRC:           binary.BigEndian.Uint32(data[8:12]),
		CSRC:           make([]uint32, csrcCount),
	}

	for index := range packet.CSRC {
		packet.CSRC[index] = binary.BigEndian.Uint32(data[12+index*4 : 16+index*4])
	}

	if packet.Extension {
		if len(data) < offset+4 {
			return RTPPacket{}, fmt.Errorf("%w: truncated extension header", ErrMalformedRTP)
		}

		packet.ExtensionID = binary.BigEndian.Uint16(data[offset : offset+2])
		extensionLength := int(binary.BigEndian.Uint16(data[offset+2:offset+4])) * 4
		offset += 4
		if len(data) < offset+extensionLength {
			return RTPPacket{}, fmt.Errorf("%w: truncated extension", ErrMalformedRTP)
		}

		packet.ExtensionData = append([]byte(nil), data[offset:offset+extensionLength]...)
		offset += extensionLength
	}

	end := len(data)
	if packet.Padding {
		paddingLength := int(data[len(data)-1])
		if paddingLength == 0 || paddingLength > end-offset {
			return RTPPacket{}, fmt.Errorf("%w: invalid padding", ErrMalformedRTP)
		}

		packet.PaddingSize = uint8(paddingLength)
		end -= paddingLength
	}

	packet.Payload = append([]byte(nil), data[offset:end]...)

	return packet, nil
}

func (p RTPPacket) MarshalBinary() ([]byte, error) {
	if len(p.CSRC) > 15 || len(p.ExtensionData)%4 != 0 {
		return nil, ErrMalformedRTP
	}

	headerLength := 12 + len(p.CSRC)*4
	if p.Extension {
		headerLength += 4 + len(p.ExtensionData)
	}

	paddingLength := 0
	if p.Padding {
		paddingLength = int(p.PaddingSize)
		if paddingLength == 0 {
			return nil, fmt.Errorf("%w: padding size is required", ErrMalformedRTP)
		}
	}

	packet := make([]byte, headerLength+len(p.Payload)+paddingLength)
	packet[0] = 2 << 6
	if p.Padding {
		packet[0] |= 0x20
	}

	if p.Extension {
		packet[0] |= 0x10
	}

	packet[0] |= byte(len(p.CSRC))
	packet[1] = p.PayloadType & 0x7f
	if p.Marker {
		packet[1] |= 0x80
	}

	binary.BigEndian.PutUint16(packet[2:4], p.SequenceNumber)
	binary.BigEndian.PutUint32(packet[4:8], p.Timestamp)
	binary.BigEndian.PutUint32(packet[8:12], p.SSRC)
	offset := 12
	for _, csrc := range p.CSRC {
		binary.BigEndian.PutUint32(packet[offset:offset+4], csrc)
		offset += 4
	}

	if p.Extension {
		binary.BigEndian.PutUint16(packet[offset:offset+2], p.ExtensionID)
		binary.BigEndian.PutUint16(packet[offset+2:offset+4], uint16(len(p.ExtensionData)/4))
		offset += 4
		copy(packet[offset:], p.ExtensionData)
		offset += len(p.ExtensionData)
	}

	copy(packet[offset:], p.Payload)
	if paddingLength > 0 {
		packet[len(packet)-1] = byte(paddingLength)
	}

	return packet, nil
}

type RTPConfig struct {
	LocalAddr        string
	RemoteAddr       string
	PayloadType      uint8
	ClockRate        uint32
	SamplesPerPacket uint32
	SSRC             uint32
	SymmetricRTP     bool
	ReadBufferBytes  int
	Pacing           bool
}

type RTPSession struct {
	conn             net.PacketConn
	mu               sync.RWMutex
	sendMu           sync.Mutex
	remote           net.Addr
	payloadType      uint8
	clockRate        uint32
	samplesPerPacket uint32
	ssrc             uint32
	sequence         uint16
	timestamp        uint32
	symmetricRTP     bool
	pacing           bool
	nextSend         time.Time
	reception        *ReceptionTracker
	packets          chan RTPPacket
	errors           chan error
	done             chan struct{}
	closeOnce        sync.Once
}

func NewRTPSession(config RTPConfig) (*RTPSession, error) {
	localAddr := config.LocalAddr
	if localAddr == "" {
		localAddr = "0.0.0.0:0"
	}

	conn, err := net.ListenPacket("udp", localAddr)
	if err != nil {
		return nil, err
	}

	return newRTPSession(conn, config)
}

func newRTPSession(conn net.PacketConn, config RTPConfig) (*RTPSession, error) {
	var remote net.Addr
	var err error
	if config.RemoteAddr != "" {
		remote, err = net.ResolveUDPAddr("udp", config.RemoteAddr)
		if err != nil {
			_ = conn.Close()
			return nil, err
		}
	}

	ssrc := config.SSRC
	if ssrc == 0 {
		ssrc, err = randomUint32()
		if err != nil {
			_ = conn.Close()
			return nil, err
		}
	}

	sequence, err := randomUint16()
	if err != nil {
		_ = conn.Close()
		return nil, err
	}

	timestamp, err := randomUint32()
	if err != nil {
		_ = conn.Close()
		return nil, err
	}

	clockRate := config.ClockRate
	if clockRate == 0 {
		clockRate = 8000
	}

	samplesPerPacket := config.SamplesPerPacket
	if samplesPerPacket == 0 {
		samplesPerPacket = clockRate / 50
	}

	readBuffer := config.ReadBufferBytes
	if readBuffer <= 0 {
		readBuffer = 64 << 10
	}

	session := &RTPSession{
		conn:             conn,
		remote:           remote,
		payloadType:      config.PayloadType,
		clockRate:        clockRate,
		samplesPerPacket: samplesPerPacket,
		ssrc:             ssrc,
		sequence:         sequence,
		timestamp:        timestamp,
		symmetricRTP:     config.SymmetricRTP,
		pacing:           config.Pacing,
		reception:        NewReceptionTracker(clockRate),
		packets:          make(chan RTPPacket, 128),
		errors:           make(chan error, 8),
		done:             make(chan struct{}),
	}

	go session.readLoop(readBuffer)
	return session, nil
}

func (s *RTPSession) LocalAddr() net.Addr {
	return s.conn.LocalAddr()
}

func (s *RTPSession) Packets() <-chan RTPPacket {
	return s.packets
}

func (s *RTPSession) Errors() <-chan error {
	return s.errors
}

func (s *RTPSession) SetRemote(address string) error {
	remote, err := net.ResolveUDPAddr("udp", address)
	if err != nil {
		return err
	}

	s.mu.Lock()
	s.remote = remote
	s.mu.Unlock()
	return nil
}

func (s *RTPSession) ConfigurePayload(payloadType uint8, clockRate, samplesPerPacket uint32) {
	if clockRate == 0 {
		clockRate = 8000
	}

	if samplesPerPacket == 0 {
		samplesPerPacket = clockRate / 50
	}

	s.mu.Lock()
	s.payloadType = payloadType
	s.clockRate = clockRate
	s.samplesPerPacket = samplesPerPacket
	s.reception = NewReceptionTracker(clockRate)
	s.mu.Unlock()
}

func (s *RTPSession) Send(ctx context.Context, payload []byte, marker bool) error {
	s.sendMu.Lock()
	defer s.sendMu.Unlock()

	if err := s.waitForPacing(ctx); err != nil {
		return err
	}

	s.mu.Lock()
	remote := s.remote
	if remote == nil {
		s.mu.Unlock()
		return errors.New("RTP remote address is not configured")
	}

	packet := RTPPacket{
		Version:        2,
		Marker:         marker,
		PayloadType:    s.payloadType,
		SequenceNumber: s.sequence,
		Timestamp:      s.timestamp,
		SSRC:           s.ssrc,
		Payload:        append([]byte(nil), payload...),
	}

	s.sequence++
	s.timestamp += s.samplesPerPacket
	s.mu.Unlock()
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

func (s *RTPSession) Metrics() ReceptionMetrics {
	s.mu.RLock()
	tracker := s.reception
	s.mu.RUnlock()

	return tracker.Snapshot()
}

func (s *RTPSession) RTCPReceiverReport() ([]byte, error) {
	s.mu.RLock()
	tracker := s.reception
	ssrc := s.ssrc
	s.mu.RUnlock()

	return tracker.ReceiverReport(ssrc)
}

func (s *RTPSession) Close() error {
	var err error
	s.closeOnce.Do(func() {
		close(s.done)
		err = s.conn.Close()
	})
	return err
}

func (s *RTPSession) readLoop(bufferSize int) {
	defer close(s.packets)
	defer close(s.errors)
	buffer := make([]byte, bufferSize)
	for {
		count, remote, err := s.conn.ReadFrom(buffer)
		if err != nil {
			select {
			case <-s.done:
				return
			default:
				s.report(err)
				return
			}
		}

		packet, err := ParseRTPPacket(buffer[:count])
		if err != nil {
			s.report(err)
			continue
		}

		packet.RemoteAddr = remote

		s.mu.RLock()
		tracker := s.reception
		s.mu.RUnlock()
		tracker.Observe(packet, time.Now())

		if s.symmetricRTP {
			s.mu.Lock()
			s.remote = remote
			s.mu.Unlock()
		}

		select {
		case s.packets <- packet:
		case <-s.done:
			return
		}
	}
}

func (s *RTPSession) waitForPacing(ctx context.Context) error {
	s.mu.RLock()
	pacing := s.pacing
	clockRate := s.clockRate
	samplesPerPacket := s.samplesPerPacket
	s.mu.RUnlock()

	if !pacing || clockRate == 0 || samplesPerPacket == 0 {
		return nil
	}

	now := time.Now()
	if s.nextSend.After(now) {
		timer := time.NewTimer(time.Until(s.nextSend))

		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()

			return ctx.Err()
		case <-s.done:
			timer.Stop()

			return net.ErrClosed
		}
	}

	packetDuration := time.Duration(samplesPerPacket) * time.Second / time.Duration(clockRate)
	s.nextSend = time.Now().Add(packetDuration)

	return nil
}

func (s *RTPSession) report(err error) {
	select {
	case s.errors <- err:
	default:
	}
}

func randomUint16() (uint16, error) {
	var value [2]byte
	if _, err := rand.Read(value[:]); err != nil {
		return 0, err
	}

	return binary.BigEndian.Uint16(value[:]), nil
}

func randomUint32() (uint32, error) {
	var value [4]byte
	if _, err := rand.Read(value[:]); err != nil {
		return 0, err
	}

	return binary.BigEndian.Uint32(value[:]), nil
}
