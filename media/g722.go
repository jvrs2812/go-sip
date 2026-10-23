package media

import (
	"errors"
	"sync"

	"github.com/gotranspile/g722"
)

var ErrInvalidG722Frame = errors.New("invalid G.722 frame")

type G722 struct {
	mu      sync.Mutex
	encoder *g722.Encoder
	decoder *g722.Decoder
}

func NewG722() *G722 {
	return &G722{
		encoder: g722.NewEncoder(g722.Rate64000, 0),
		decoder: g722.NewDecoder(g722.Rate64000, 0),
	}
}

func (*G722) PayloadType() uint8 {
	return 9
}

func (*G722) RTPClockRate() uint32 {
	return 8000
}

func (*G722) SampleRate() uint32 {
	return 16000
}

func (*G722) FrameSamples() int {
	return 320
}

func (c *G722) EncodePCM(samples []int16) ([]byte, error) {
	if len(samples) == 0 || len(samples)%2 != 0 {
		return nil, ErrInvalidG722Frame
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	encoded := make([]byte, (len(samples)+1)/2)
	written := c.encoder.Encode(encoded, samples)

	return encoded[:written], nil
}

func (c *G722) DecodePCM(payload []byte) ([]int16, error) {
	if len(payload) == 0 {
		return nil, ErrInvalidG722Frame
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	decoded := make([]int16, len(payload)*2)
	written := c.decoder.Decode(decoded, payload)

	return decoded[:written], nil
}
