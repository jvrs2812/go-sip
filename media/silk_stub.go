//go:build !silk || !cgo

package media

type SILK struct{}

func SILKAvailable() bool {
	return false
}

func NewSILK(int, int) (*SILK, error) {
	return nil, ErrCodecUnavailable
}

func (*SILK) PayloadType() uint8 {
	return DefaultSILKPayloadType
}

func (*SILK) RTPClockRate() uint32 {
	return 0
}

func (*SILK) SampleRate() uint32 {
	return 0
}

func (*SILK) FrameSamples() int {
	return 0
}

func (*SILK) EncodePCM([]int16) ([]byte, error) {
	return nil, ErrCodecUnavailable
}

func (*SILK) DecodePCM([]byte) ([]int16, error) {
	return nil, ErrCodecUnavailable
}
