package speech

// nativeSampleRate is the Klatt synthesizer's output rate.
const nativeSampleRate = klattSampleRate

// Synthesize converts English text into a sequence of 20 ms Opus packets at
// 48 kHz mono, ready to stream over Discord voice. It returns nil frames for
// empty or silent input.
func Synthesize(text string) ([][]byte, error) {
	pcm := synthesizePCM(text)
	if len(pcm) == 0 {
		return nil, nil
	}
	resampled := resample(pcm, nativeSampleRate, discordSampleRate)
	return encodeOpusFrames(resampled)
}

// SynthesizePCM exposes the raw 11025 Hz mono PCM for tests and tooling.
func SynthesizePCM(text string) []int16 {
	return synthesizePCM(text)
}

func synthesizePCM(text string) []int16 {
	var ph phonemeBuf
	xlateString(text, &ph)

	elems := phoneToElements(ph.b)
	if len(elems) == 0 {
		return nil
	}

	k := newKlatt()
	k.init()
	k.initSynth(elems)

	// Each element runs for at most a byte's worth of frames; size the work
	// buffer for the largest possible duration.
	buf := make([]int16, k.nspFr*256)
	var pcm []int16
	for {
		n := k.synth(buf)
		if n < 0 {
			break
		}
		if n > 0 {
			pcm = append(pcm, buf[:n]...)
		}
	}
	return pcm
}
