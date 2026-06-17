package voice

import "testing"

func TestOpusPacketSamples(t *testing.T) {
	tests := []struct {
		name   string
		packet []byte
		want   int
	}{
		{"silk nb 10ms config0", []byte{0x00}, 480},
		{"silk nb 20ms config1", []byte{0x08}, 960},
		{"celt nb 2.5ms config16", []byte{16 << 3}, 120},
		{"celt fb 20ms config31", []byte{31 << 3}, 960},
		{"code1 two frames", []byte{0x08 | 0x01}, 1920},
		{"code2 two frames", []byte{0x08 | 0x02}, 1920},
		{"code3 three frames", []byte{0x08 | 0x03, 0x03}, 2880},
		{"silence frame", silenceFrame, 960},
		{"empty falls back", []byte{}, defaultFrameSamples},
		{"code3 missing count falls back", []byte{0x08 | 0x03}, defaultFrameSamples},
		{"code3 zero count falls back", []byte{0x08 | 0x03, 0x00}, defaultFrameSamples},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := OpusPacketSamples(tt.packet); got != tt.want {
				t.Fatalf("OpusPacketSamples(%x) = %d, want %d", tt.packet, got, tt.want)
			}
		})
	}
}

func TestOpusPacketSamplesNeverExceedsMax(t *testing.T) {
	// A 60ms SILK config (config 3 = 2880 samples) with 3 frames would be
	// 8640 samples, exceeding the 120ms (5760) Opus limit, so it must fall back.
	got := OpusPacketSamples([]byte{(3 << 3) | 0x03, 0x03})
	if got != defaultFrameSamples {
		t.Fatalf("expected fallback for over-long packet, got %d", got)
	}
}
