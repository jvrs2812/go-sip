//go:build gsm && cgo

package media

/*
#cgo LDFLAGS: -lgsm
#include <gsm/gsm.h>
*/
import "C"

import (
	"errors"
	"runtime"
	"sync"
	"unsafe"
)

type GSM struct {
	mu        sync.Mutex
	encoder   C.gsm
	decoder   C.gsm
	closeOnce sync.Once
}

func GSMAvailable() bool {
	return true
}

func NewGSM() (*GSM, error) {
	codec := &GSM{
		encoder: C.gsm_create(),
		decoder: C.gsm_create(),
	}

	if codec.encoder == nil || codec.decoder == nil {
		_ = codec.Close()

		return nil, errors.New("initialize GSM-FR codec")
	}

	runtime.SetFinalizer(codec, func(value *GSM) {
		_ = value.Close()
	})

	return codec, nil
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

func (c *GSM) EncodePCM(samples []int16) ([]byte, error) {
	if len(samples) != GSMFrameSamples {
		return nil, ErrInvalidGSMFrame
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.encoder == nil {
		return nil, ErrCodecUnavailable
	}

	payload := make([]byte, GSMFrameBytes)
	C.gsm_encode(
		c.encoder,
		(*C.gsm_signal)(unsafe.Pointer(&samples[0])),
		(*C.gsm_byte)(unsafe.Pointer(&payload[0])),
	)

	return payload, nil
}

func (c *GSM) DecodePCM(payload []byte) ([]int16, error) {
	if len(payload) != GSMFrameBytes {
		return nil, ErrInvalidGSMFrame
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.decoder == nil {
		return nil, ErrCodecUnavailable
	}

	samples := make([]int16, GSMFrameSamples)
	result := C.gsm_decode(
		c.decoder,
		(*C.gsm_byte)(unsafe.Pointer(&payload[0])),
		(*C.gsm_signal)(unsafe.Pointer(&samples[0])),
	)
	if result != 0 {
		return nil, errors.New("decode GSM-FR frame")
	}

	return samples, nil
}

func (c *GSM) Close() error {
	c.closeOnce.Do(func() {
		c.mu.Lock()
		defer c.mu.Unlock()

		if c.encoder != nil {
			C.gsm_destroy(c.encoder)
			c.encoder = nil
		}

		if c.decoder != nil {
			C.gsm_destroy(c.decoder)
			c.decoder = nil
		}

		runtime.SetFinalizer(c, nil)
	})

	return nil
}
