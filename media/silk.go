package media

import "errors"

var (
	ErrInvalidSILKConfig = errors.New("SILK sample rate must be 8000, 12000, 16000, or 24000 Hz")
	ErrInvalidSILKFrame  = errors.New("invalid SILK frame")
)

const DefaultSILKPayloadType uint8 = 112

func validSILKSampleRate(sampleRate int) bool {
	return sampleRate == 8000 || sampleRate == 12000 || sampleRate == 16000 || sampleRate == 24000
}
