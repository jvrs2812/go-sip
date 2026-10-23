package media

import "github.com/zaf/g711"

type G711Codec interface {
	PayloadType() uint8
	ClockRate() uint32
	Encode([]byte) []byte
	Decode([]byte) []byte
}

type PCMU struct{}

func (PCMU) PayloadType() uint8 {
	return 0
}

func (PCMU) ClockRate() uint32 {
	return 8000
}

func (PCMU) Encode(pcm []byte) []byte {
	return g711.EncodeUlaw(pcm)
}

func (PCMU) Decode(payload []byte) []byte {
	return g711.DecodeUlaw(payload)
}

type PCMA struct{}

func (PCMA) PayloadType() uint8 {
	return 8
}

func (PCMA) ClockRate() uint32 {
	return 8000
}

func (PCMA) Encode(pcm []byte) []byte {
	return g711.EncodeAlaw(pcm)
}

func (PCMA) Decode(payload []byte) []byte {
	return g711.DecodeAlaw(payload)
}
