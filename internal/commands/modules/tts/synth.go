package tts

import "gamerpal/internal/speech"

// Synthesizer turns text into a sequence of 20 ms Opus frames at 48 kHz mono,
// the exact format the voice connection consumes.
type Synthesizer interface {
	Synthesize(text string) ([][]byte, error)
}

// offlineSynthesizer drives the pure-Go, fully offline speech engine in
// internal/speech. It needs no network access, API key, or cgo.
type offlineSynthesizer struct{}

func newOfflineSynthesizer() offlineSynthesizer { return offlineSynthesizer{} }

// Synthesize converts text to Opus frames via the offline Klatt synthesizer.
func (offlineSynthesizer) Synthesize(text string) ([][]byte, error) {
	return speech.Synthesize(text)
}
