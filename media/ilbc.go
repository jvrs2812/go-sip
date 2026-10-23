package media

import (
	"errors"
	"sync"

	ilbc "github.com/Bigous/iLBC-Go/src"
)

var ErrInvalidILBCMode = errors.New("iLBC mode must be 20 or 30 milliseconds")

type ILBC struct {
	mu      sync.Mutex
	mode    ilbc.Mode
	encoder *ilbc.Encoder
	decoder *ilbc.Decoder
}

func NewILBC(modeMilliseconds int, enhancement bool) (*ILBC, error) {
	mode := ilbc.Mode(modeMilliseconds)
	encoder, err := ilbc.NewEncoder(mode)
	if err != nil {
		return nil, ErrInvalidILBCMode
	}

	decoder, err := ilbc.NewDecoder(mode, enhancement)
	if err != nil {
		return nil, ErrInvalidILBCMode
	}

	return &ILBC{mode: mode, encoder: encoder, decoder: decoder}, nil
}

func (*ILBC) RTPClockRate() uint32 {
	return 8000
}

func (*ILBC) SampleRate() uint32 {
	return 8000
}

func (c *ILBC) Mode() int {
	return int(c.mode)
}

func (c *ILBC) FrameSamples() int {
	return c.encoder.FrameSamples()
}

func (c *ILBC) FrameBytes() int {
	return c.encoder.FrameBytes()
}

func (c *ILBC) EncodePCM(samples []int16) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	payload := make([]byte, c.encoder.FrameBytes())
	if err := c.encoder.Encode(payload, samples); err != nil {
		return nil, err
	}

	return payload, nil
}

func (c *ILBC) DecodePCM(payload []byte) ([]int16, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	samples := make([]int16, c.decoder.FrameSamples())
	if err := c.decoder.Decode(samples, payload); err != nil {
		return nil, err
	}

	return samples, nil
}

func (c *ILBC) Conceal() ([]int16, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	samples := make([]int16, c.decoder.FrameSamples())
	if err := c.decoder.Conceal(samples); err != nil {
		return nil, err
	}

	return samples, nil
}
