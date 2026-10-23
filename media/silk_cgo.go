//go:build silk && cgo

package media

import (
	"bytes"
	"encoding/binary"
	"errors"
	"sync"

	silkcodec "github.com/youthlin/silk"
)

const silkStreamHeader = "#!SILK_V3"

type SILK struct {
	mu           sync.Mutex
	sampleRate   int
	bitRate      int
	frameSamples int
}

func SILKAvailable() bool {
	return true
}

func NewSILK(sampleRate, bitRate int) (*SILK, error) {
	if !validSILKSampleRate(sampleRate) {
		return nil, ErrInvalidSILKConfig
	}

	if bitRate <= 0 {
		bitRate = 25000
	}

	return &SILK{
		sampleRate:   sampleRate,
		bitRate:      bitRate,
		frameSamples: sampleRate / 50,
	}, nil
}

func (*SILK) PayloadType() uint8 {
	return DefaultSILKPayloadType
}

func (c *SILK) RTPClockRate() uint32 {
	return uint32(c.sampleRate)
}

func (c *SILK) SampleRate() uint32 {
	return uint32(c.sampleRate)
}

func (c *SILK) FrameSamples() int {
	return c.frameSamples
}

func (c *SILK) EncodePCM(samples []int16) ([]byte, error) {
	if len(samples) != c.frameSamples {
		return nil, ErrInvalidSILKFrame
	}

	pcm := make([]byte, len(samples)*2)
	for index, sample := range samples {
		binary.LittleEndian.PutUint16(pcm[index*2:], uint16(sample))
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	stream, err := silkcodec.Encode(
		bytes.NewReader(pcm),
		silkcodec.SampleRate(c.sampleRate),
		silkcodec.MaxInternal(c.sampleRate),
		silkcodec.PacketSizeMs(20),
		silkcodec.BitRate(c.bitRate),
		silkcodec.Stx(false),
	)
	if err != nil {
		return nil, err
	}

	return extractSILKFrame(stream)
}

func (c *SILK) DecodePCM(payload []byte) ([]int16, error) {
	if len(payload) == 0 || len(payload) > 1250 {
		return nil, ErrInvalidSILKFrame
	}

	stream := make([]byte, 0, len(silkStreamHeader)+len(payload)+4)
	stream = append(stream, silkStreamHeader...)
	stream = binary.LittleEndian.AppendUint16(stream, uint16(len(payload)))
	stream = append(stream, payload...)
	stream = append(stream, 0xff, 0xff)

	c.mu.Lock()
	decoded, err := silkcodec.Decode(bytes.NewReader(stream), silkcodec.WithSampleRate(c.sampleRate))
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}

	if len(decoded)%2 != 0 || len(decoded) == 0 {
		return nil, ErrInvalidSILKFrame
	}

	samples := make([]int16, len(decoded)/2)
	for index := range samples {
		samples[index] = int16(binary.LittleEndian.Uint16(decoded[index*2:]))
	}

	return samples, nil
}

func extractSILKFrame(stream []byte) ([]byte, error) {
	if len(stream) < len(silkStreamHeader)+4 || string(stream[:len(silkStreamHeader)]) != silkStreamHeader {
		return nil, ErrInvalidSILKFrame
	}

	offset := len(silkStreamHeader)
	length := int(int16(binary.LittleEndian.Uint16(stream[offset : offset+2])))
	offset += 2
	if length <= 0 || offset+length > len(stream) {
		return nil, ErrInvalidSILKFrame
	}

	if offset+length+2 > len(stream) || !bytes.Equal(stream[offset+length:offset+length+2], []byte{0xff, 0xff}) {
		return nil, errors.New("SILK encoder returned more than one RTP frame")
	}

	return append([]byte(nil), stream[offset:offset+length]...), nil
}
