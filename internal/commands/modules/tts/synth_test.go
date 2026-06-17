package tts

import "testing"

func TestOfflineSynthesizerProducesFrames(t *testing.T) {
	frames, err := newOfflineSynthesizer().Synthesize("hello world")
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if len(frames) == 0 {
		t.Fatal("expected Opus frames")
	}
	for i, f := range frames {
		if len(f) == 0 {
			t.Fatalf("frame %d is empty", i)
		}
	}
}

func TestOfflineSynthesizerEmptyText(t *testing.T) {
	frames, err := newOfflineSynthesizer().Synthesize("")
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if len(frames) != 0 {
		t.Fatalf("expected no frames for empty text, got %d", len(frames))
	}
}
