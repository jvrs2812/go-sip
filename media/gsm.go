package media

import "errors"

var ErrInvalidGSMFrame = errors.New("GSM-FR requires 160 PCM samples or a 33-byte payload")

const GSMFrameSamples = 160
const GSMFrameBytes = 33
