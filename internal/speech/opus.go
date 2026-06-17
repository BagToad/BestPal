package speech

import "github.com/thesyncim/gopus"

const (
	// discordSampleRate is the sample rate Discord voice expects.
	discordSampleRate = 48000
	// discordChannels is mono; the synthesizer produces a single channel.
	discordChannels = 1
	// opusFrameSamples is a 20 ms frame at 48 kHz (48000 / 50).
	opusFrameSamples = discordSampleRate / 50
	// maxOpusPacketBytes bounds a single encoded packet.
	maxOpusPacketBytes = 4000
)

// encodeOpusFrames encodes 48 kHz mono PCM into a sequence of 20 ms Opus
// packets ready for the voice transport. The final partial frame is
// zero-padded to a full 20 ms.
func encodeOpusFrames(pcm []int16) ([][]byte, error) {
	enc, err := gopus.NewEncoder(gopus.EncoderConfig{
		SampleRate:  discordSampleRate,
		Channels:    discordChannels,
		Application: gopus.ApplicationAudio,
	})
	if err != nil {
		return nil, err
	}

	var frames [][]byte
	scratch := make([]byte, maxOpusPacketBytes)
	frame := make([]int16, opusFrameSamples)

	for off := 0; off < len(pcm); off += opusFrameSamples {
		end := off + opusFrameSamples
		if end <= len(pcm) {
			copy(frame, pcm[off:end])
		} else {
			n := copy(frame, pcm[off:])
			for i := n; i < len(frame); i++ {
				frame[i] = 0
			}
		}

		n, err := enc.EncodeInt16(frame, scratch)
		if err != nil {
			return nil, err
		}
		packet := make([]byte, n)
		copy(packet, scratch[:n])
		frames = append(frames, packet)
	}

	return frames, nil
}
