package media

import (
	"errors"
	"time"
)

var (
	ErrCodecUnavailable  = errors.New("codec backend is unavailable in this build")
	ErrInvalidOpusConfig = errors.New("invalid Opus configuration")
)

const DefaultOpusPayloadType uint8 = 111

func validOpusConfig(sampleRate, channels int, frameDuration time.Duration) bool {
	supportedRate := sampleRate == 8000 || sampleRate == 12000 || sampleRate == 16000 || sampleRate == 24000 || sampleRate == 48000
	supportedDuration := frameDuration == 5*time.Millisecond/2 || frameDuration == 5*time.Millisecond || frameDuration == 10*time.Millisecond ||
		frameDuration == 20*time.Millisecond || frameDuration == 40*time.Millisecond || frameDuration == 60*time.Millisecond

	return supportedRate && (channels == 1 || channels == 2) && supportedDuration
}
