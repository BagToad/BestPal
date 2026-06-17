package voice

// Audio constants. Discord voice always runs at 48kHz; a 20ms frame is the
// canonical size (960 samples per channel) and the RTP timestamp advances by
// the per-channel sample count of each frame.
const (
	sampleRate = 48000

	// maxOpusFrameSize bounds a single Opus payload we will send.
	maxOpusFrameSize = 1400

	// defaultFrameSamples is the per-channel sample count of a 20ms frame, used
	// as a fallback when a packet's table-of-contents byte can't be parsed.
	defaultFrameSamples = 960
)

// silenceFrame is a single 20ms Opus frame of silence. Sending a few of these
// when speech ends flushes Discord's jitter buffer and prevents the tail of the
// audio from being cut or repeated.
var silenceFrame = []byte{0xF8, 0xFF, 0xFE}

// frameSamplesByConfig maps an Opus TOC "config" (the high 5 bits of the first
// byte) to the number of samples per channel in one frame at 48kHz. Derived
// from RFC 6716 section 3.1.
var frameSamplesByConfig = [32]int{
	// SILK NB / MB / WB: 10, 20, 40, 60 ms
	480, 960, 1920, 2880,
	480, 960, 1920, 2880,
	480, 960, 1920, 2880,
	// Hybrid SWB / FB: 10, 20 ms
	480, 960,
	480, 960,
	// CELT NB / WB / SWB / FB: 2.5, 5, 10, 20 ms
	120, 240, 480, 960,
	120, 240, 480, 960,
	120, 240, 480, 960,
	120, 240, 480, 960,
}

// OpusPacketSamples returns the number of samples per channel (at 48kHz)
// represented by a single Opus packet, parsed from its TOC byte and frame-count
// code per RFC 6716. This is what the RTP timestamp must advance by and how long
// the frame should play for. It falls back to a 20ms frame for malformed input
// so playback pacing degrades gracefully rather than stalling.
func OpusPacketSamples(packet []byte) int {
	if len(packet) == 0 {
		return defaultFrameSamples
	}

	toc := packet[0]
	perFrame := frameSamplesByConfig[toc>>3]

	var frames int
	switch toc & 0x03 {
	case 0:
		frames = 1
	case 1, 2:
		frames = 2
	case 3:
		// Code 3: the number of frames is in the low 6 bits of the second byte.
		if len(packet) < 2 {
			return defaultFrameSamples
		}
		frames = int(packet[1] & 0x3F)
		if frames == 0 {
			return defaultFrameSamples
		}
	}

	total := perFrame * frames
	// An Opus packet may not represent more than 120ms of audio.
	if total <= 0 || total > 5760 {
		return defaultFrameSamples
	}
	return total
}
