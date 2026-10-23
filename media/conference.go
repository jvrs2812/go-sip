package media

import (
	"errors"
	"fmt"
	"math"
	"sync"
)

var (
	ErrConferenceFull       = errors.New("conference has reached its participant limit")
	ErrUnknownParticipant   = errors.New("unknown conference participant")
	ErrInvalidConferencePCM = errors.New("invalid conference PCM frame")
)

type ParticipantConfig struct {
	Gain  float64
	Muted bool
}

type PCMFrame struct {
	Samples    []int16
	SampleRate int
}

type conferenceParticipant struct {
	gain  float64
	muted bool
}

type ConferenceBridge struct {
	mu              sync.RWMutex
	sampleRate      int
	frameSamples    int
	maxParticipants int
	participants    map[string]conferenceParticipant
}

func NewConferenceBridge(sampleRate, frameSamples, maxParticipants int) (*ConferenceBridge, error) {
	if sampleRate <= 0 || frameSamples <= 0 || maxParticipants < 2 {
		return nil, errors.New("conference configuration is invalid")
	}

	return &ConferenceBridge{
		sampleRate:      sampleRate,
		frameSamples:    frameSamples,
		maxParticipants: maxParticipants,
		participants:    make(map[string]conferenceParticipant, maxParticipants),
	}, nil
}

func (b *ConferenceBridge) AddParticipant(id string, config ParticipantConfig) error {
	if id == "" {
		return errors.New("conference participant ID is empty")
	}

	gain, err := normalizeGain(config.Gain)
	if err != nil {
		return err
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if _, exists := b.participants[id]; !exists && len(b.participants) >= b.maxParticipants {
		return ErrConferenceFull
	}

	b.participants[id] = conferenceParticipant{gain: gain, muted: config.Muted}

	return nil
}

func (b *ConferenceBridge) RemoveParticipant(id string) {
	b.mu.Lock()
	delete(b.participants, id)
	b.mu.Unlock()
}

func (b *ConferenceBridge) SetMuted(id string, muted bool) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	participant, exists := b.participants[id]
	if !exists {
		return ErrUnknownParticipant
	}

	participant.muted = muted
	b.participants[id] = participant

	return nil
}

func (b *ConferenceBridge) SetGain(id string, gain float64) error {
	normalized, err := normalizeGain(gain)
	if err != nil {
		return err
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	participant, exists := b.participants[id]
	if !exists {
		return ErrUnknownParticipant
	}

	participant.gain = normalized
	b.participants[id] = participant

	return nil
}

func (b *ConferenceBridge) Mix(inputs map[string][]int16) (map[string][]int16, error) {
	b.mu.RLock()
	participants := make(map[string]conferenceParticipant, len(b.participants))

	for id, participant := range b.participants {
		participants[id] = participant
	}

	b.mu.RUnlock()

	for id, samples := range inputs {
		if _, exists := participants[id]; !exists {
			return nil, fmt.Errorf("%w: %s", ErrUnknownParticipant, id)
		}

		if len(samples) != b.frameSamples {
			return nil, fmt.Errorf("%w: participant %s has %d samples, want %d", ErrInvalidConferencePCM, id, len(samples), b.frameSamples)
		}
	}

	mix := make([]float64, b.frameSamples)
	contributions := make(map[string][]float64, len(participants))

	for id, participant := range participants {
		contribution := make([]float64, b.frameSamples)
		samples := inputs[id]

		if !participant.muted && len(samples) == b.frameSamples {
			for index, sample := range samples {
				value := float64(sample) * participant.gain
				contribution[index] = value
				mix[index] += value
			}
		}

		contributions[id] = contribution
	}

	outputs := make(map[string][]int16, len(participants))

	for id := range participants {
		output := make([]int16, b.frameSamples)
		ownContribution := contributions[id]

		for index := range output {
			output[index] = clipPCM(mix[index] - ownContribution[index])
		}

		outputs[id] = output
	}

	return outputs, nil
}

func (b *ConferenceBridge) MixFrames(inputs map[string]PCMFrame) (map[string][]int16, error) {
	resampled := make(map[string][]int16, len(inputs))

	for id, frame := range inputs {
		if frame.SampleRate <= 0 {
			return nil, fmt.Errorf("%w: participant %s has invalid sample rate", ErrInvalidConferencePCM, id)
		}

		samples, err := ResampleLinear(frame.Samples, frame.SampleRate, b.sampleRate, b.frameSamples)
		if err != nil {
			return nil, fmt.Errorf("%w: participant %s: %v", ErrInvalidConferencePCM, id, err)
		}

		resampled[id] = samples
	}

	return b.Mix(resampled)
}

func ResampleLinear(samples []int16, inputRate, outputRate, outputSamples int) ([]int16, error) {
	if len(samples) == 0 || inputRate <= 0 || outputRate <= 0 || outputSamples <= 0 {
		return nil, ErrInvalidConferencePCM
	}

	output := make([]int16, outputSamples)
	if len(samples) == 1 {
		for index := range output {
			output[index] = samples[0]
		}

		return output, nil
	}

	step := float64(inputRate) / float64(outputRate)

	for index := range output {
		position := float64(index) * step
		left := int(position)

		if left >= len(samples)-1 {
			output[index] = samples[len(samples)-1]

			continue
		}

		fraction := position - float64(left)
		value := float64(samples[left])*(1-fraction) + float64(samples[left+1])*fraction
		output[index] = clipPCM(value)
	}

	return output, nil
}

func (b *ConferenceBridge) SampleRate() int {
	return b.sampleRate
}

func (b *ConferenceBridge) FrameSamples() int {
	return b.frameSamples
}

func normalizeGain(gain float64) (float64, error) {
	if math.IsNaN(gain) || math.IsInf(gain, 0) || gain < 0 || gain > 4 {
		return 0, errors.New("conference gain must be between 0 and 4")
	}

	if gain == 0 {
		return 1, nil
	}

	return gain, nil
}

func clipPCM(value float64) int16 {
	if value > math.MaxInt16 {
		return math.MaxInt16
	}

	if value < math.MinInt16 {
		return math.MinInt16
	}

	return int16(math.Round(value))
}
