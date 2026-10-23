package media

import "testing"

func TestConferenceBridgeMixesThreeParticipantsWithMixMinus(t *testing.T) {
	bridge, err := NewConferenceBridge(8000, 3, 3)
	if err != nil {
		t.Fatal(err)
	}

	for _, id := range []string{"alice", "bob", "carol"} {
		if err := bridge.AddParticipant(id, ParticipantConfig{}); err != nil {
			t.Fatal(err)
		}
	}

	outputs, err := bridge.Mix(map[string][]int16{
		"alice": {100, 200, 300},
		"bob":   {10, 20, 30},
		"carol": {1, 2, 3},
	})
	if err != nil {
		t.Fatal(err)
	}

	assertPCM(t, outputs["alice"], []int16{11, 22, 33})
	assertPCM(t, outputs["bob"], []int16{101, 202, 303})
	assertPCM(t, outputs["carol"], []int16{110, 220, 330})
}

func TestConferenceBridgeMuteAndClipping(t *testing.T) {
	bridge, err := NewConferenceBridge(8000, 1, 3)
	if err != nil {
		t.Fatal(err)
	}

	if err := bridge.AddParticipant("alice", ParticipantConfig{}); err != nil {
		t.Fatal(err)
	}

	if err := bridge.AddParticipant("bob", ParticipantConfig{Gain: 2}); err != nil {
		t.Fatal(err)
	}

	if err := bridge.AddParticipant("carol", ParticipantConfig{Muted: true}); err != nil {
		t.Fatal(err)
	}

	outputs, err := bridge.Mix(map[string][]int16{
		"alice": {30000},
		"bob":   {30000},
		"carol": {30000},
	})
	if err != nil {
		t.Fatal(err)
	}

	assertPCM(t, outputs["alice"], []int16{32767})
	assertPCM(t, outputs["bob"], []int16{30000})
	assertPCM(t, outputs["carol"], []int16{32767})
}

func TestConferenceBridgeResamplesParticipantFrames(t *testing.T) {
	bridge, err := NewConferenceBridge(8000, 4, 2)
	if err != nil {
		t.Fatal(err)
	}

	if err := bridge.AddParticipant("alice", ParticipantConfig{}); err != nil {
		t.Fatal(err)
	}

	if err := bridge.AddParticipant("bob", ParticipantConfig{}); err != nil {
		t.Fatal(err)
	}

	outputs, err := bridge.MixFrames(map[string]PCMFrame{
		"alice": {Samples: []int16{0, 1000}, SampleRate: 4000},
		"bob":   {Samples: []int16{100, 100, 100, 100}, SampleRate: 8000},
	})
	if err != nil {
		t.Fatal(err)
	}

	assertPCM(t, outputs["alice"], []int16{100, 100, 100, 100})
	assertPCM(t, outputs["bob"], []int16{0, 500, 1000, 1000})
}

func assertPCM(t *testing.T, actual, expected []int16) {
	t.Helper()

	if len(actual) != len(expected) {
		t.Fatalf("PCM length = %d, want %d", len(actual), len(expected))
	}

	for index := range expected {
		if actual[index] != expected[index] {
			t.Fatalf("PCM sample %d = %d, want %d", index, actual[index], expected[index])
		}
	}
}
