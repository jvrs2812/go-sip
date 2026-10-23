//go:build opus && cgo

package media

import (
	"errors"
	"sync"
	"time"

	opus "gopkg.in/hraban/opus.v2"
)

type Opus struct {
	mu           sync.Mutex
	encoder      *opus.Encoder
	decoder      *opus.Decoder
	sampleRate   int
	channels     int
	frameSamples int
}

func OpusAvailable() bool {
	return true
}

func NewOpus(sampleRate, channels int, frameDuration time.Duration) (*Opus, error) {
	if !validOpusConfig(sampleRate, channels, frameDuration) {
		return nil, ErrInvalidOpusConfig
	}

	encoder, err := opus.NewEncoder(sampleRate, channels, opus.AppVoIP)
	if err != nil {
		return nil, err
	}

	decoder, err := opus.NewDecoder(sampleRate, channels)
	if err != nil {
		return nil, err
	}

	return &Opus{
		encoder:      encoder,
		decoder:      decoder,
		sampleRate:   sampleRate,
		channels:     channels,
		frameSamples: int(time.Duration(sampleRate) * frameDuration / time.Second),
	}, nil
}

func (*Opus) PayloadType() uint8 {
	return DefaultOpusPayloadType
}

func (*Opus) RTPClockRate() uint32 {
	return 48000
}

func (c *Opus) SampleRate() uint32 {
	return uint32(c.sampleRate)
}

func (c *Opus) Channels() int {
	return c.channels
}

func (c *Opus) FrameSamples() int {
	return c.frameSamples
}

func (c *Opus) EncodePCM(samples []int16) ([]byte, error) {
	if len(samples) != c.frameSamples*c.channels {
		return nil, ErrInvalidOpusConfig
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	payload := make([]byte, 4000)
	written, err := c.encoder.Encode(samples, payload)
	if err != nil {
		return nil, err
	}

	return payload[:written], nil
}

func (c *Opus) DecodePCM(payload []byte) ([]int16, error) {
	if len(payload) == 0 {
		return nil, errors.New("empty Opus payload")
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	maximumSamples := c.sampleRate * 120 / 1000
	decoded := make([]int16, maximumSamples*c.channels)
	written, err := c.decoder.Decode(payload, decoded)
	if err != nil {
		return nil, err
	}

	return decoded[:written*c.channels], nil
}

func (c *Opus) Conceal() ([]int16, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	decoded := make([]int16, c.frameSamples*c.channels)
	if err := c.decoder.DecodePLC(decoded); err != nil {
		return nil, err
	}

	return decoded, nil
}
