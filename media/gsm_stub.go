//go:build !gsm || !cgo

package media

type GSM struct{}

func GSMAvailable() bool {
	return false
}

func NewGSM() (*GSM, error) {
	return nil, ErrCodecUnavailable
}

func (*GSM) PayloadType() uint8 {
	return 3
}

func (*GSM) RTPClockRate() uint32 {
	return 8000
}

func (*GSM) SampleRate() uint32 {
	return 8000
}

func (*GSM) FrameSamples() int {
	return GSMFrameSamples
}

func (*GSM) FrameBytes() int {
	return GSMFrameBytes
}

func (*GSM) EncodePCM([]int16) ([]byte, error) {
	return nil, ErrCodecUnavailable
}

func (*GSM) DecodePCM([]byte) ([]int16, error) {
	return nil, ErrCodecUnavailable
}

func (*GSM) Close() error {
	return nil
}
