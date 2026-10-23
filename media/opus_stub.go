//go:build !opus || !cgo

package media

import "time"

type Opus struct{}

func OpusAvailable() bool {
	return false
}

func NewOpus(int, int, time.Duration) (*Opus, error) {
	return nil, ErrCodecUnavailable
}

func (*Opus) PayloadType() uint8 {
	return DefaultOpusPayloadType
}

func (*Opus) RTPClockRate() uint32 {
	return 48000
}

func (*Opus) SampleRate() uint32 {
	return 0
}

func (*Opus) Channels() int {
	return 0
}

func (*Opus) FrameSamples() int {
	return 0
}

func (*Opus) EncodePCM([]int16) ([]byte, error) {
	return nil, ErrCodecUnavailable
}

func (*Opus) DecodePCM([]byte) ([]int16, error) {
	return nil, ErrCodecUnavailable
}

func (*Opus) Conceal() ([]int16, error) {
	return nil, ErrCodecUnavailable
}
