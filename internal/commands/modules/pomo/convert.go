package pomo

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"strings"

	goaudio "github.com/go-audio/audio"
	"github.com/go-audio/wav"
	mp3 "github.com/hajimehoshi/go-mp3"
	opusenc "github.com/kazzmir/opus-go/opus"
)

const (
	opusSampleRate = 48000
	opusChannels   = 2
	opusFrameSize  = 960 // 20ms at 48kHz
	opusBitrate    = 64000
)

// convertToOpusFrames converts raw audio data (mp3, wav, ogg) to
// 2-byte LE length-prefixed opus frames suitable for Discord voice.
// Pure Go implementation — no ffmpeg required.
func convertToOpusFrames(input []byte, filename string) ([]byte, error) {
	ext := strings.ToLower(filename)

	var pcm []int16
	var err error

	switch {
	case strings.HasSuffix(ext, ".mp3"):
		pcm, err = decodeMp3(input)
	case strings.HasSuffix(ext, ".wav"):
		pcm, err = decodeWav(input)
	default:
		return nil, fmt.Errorf("unsupported format: %s (supported: mp3, wav)", ext)
	}
	if err != nil {
		return nil, fmt.Errorf("decode audio: %w", err)
	}

	return encodePcmToOpusFrames(pcm)
}

// decodeMp3 decodes MP3 to interleaved stereo int16 PCM at 48kHz.
func decodeMp3(data []byte) ([]int16, error) {
	dec, err := mp3.NewDecoder(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("mp3 init: %w", err)
	}

	// go-mp3 outputs signed 16-bit LE stereo at the file's sample rate
	raw, err := io.ReadAll(dec)
	if err != nil {
		return nil, fmt.Errorf("mp3 decode: %w", err)
	}

	// Convert bytes to int16 samples
	samples := make([]int16, len(raw)/2)
	for i := range samples {
		samples[i] = int16(binary.LittleEndian.Uint16(raw[i*2 : i*2+2]))
	}

	srcRate := dec.SampleRate()
	if srcRate != opusSampleRate {
		samples = resample(samples, opusChannels, srcRate, opusSampleRate)
	}

	return samples, nil
}

// decodeWav decodes WAV to interleaved stereo int16 PCM at 48kHz.
func decodeWav(data []byte) ([]int16, error) {
	dec := wav.NewDecoder(bytes.NewReader(data))
	buf, err := dec.FullPCMBuffer()
	if err != nil {
		return nil, fmt.Errorf("wav decode: %w", err)
	}

	srcChannels := int(dec.NumChans)
	srcRate := int(dec.SampleRate)
	bitDepth := int(dec.BitDepth)

	// Normalize to int16 range
	samples := intBufToInt16(buf, bitDepth)

	// Convert mono to stereo if needed
	if srcChannels == 1 {
		samples = monoToStereo(samples)
	}

	if srcRate != opusSampleRate {
		samples = resample(samples, opusChannels, srcRate, opusSampleRate)
	}

	return samples, nil
}

// encodePcmToOpusFrames encodes interleaved stereo int16 PCM into
// 2-byte LE length-prefixed opus frames.
func encodePcmToOpusFrames(pcm []int16) ([]byte, error) {
	enc, err := opusenc.NewEncoder(opusSampleRate, opusChannels, opusenc.ApplicationAudio)
	if err != nil {
		return nil, fmt.Errorf("opus encoder: %w", err)
	}
	defer enc.Close()

	if err := enc.SetBitrate(opusBitrate); err != nil {
		return nil, fmt.Errorf("set bitrate: %w", err)
	}

	samplesPerFrame := opusFrameSize * opusChannels
	packet := make([]byte, 4000) // max opus packet size
	var out bytes.Buffer

	for offset := 0; offset+samplesPerFrame <= len(pcm); offset += samplesPerFrame {
		frame := pcm[offset : offset+samplesPerFrame]
		n, err := enc.Encode(frame, opusFrameSize, packet)
		if err != nil {
			return nil, fmt.Errorf("opus encode: %w", err)
		}

		// Write 2-byte LE length prefix + frame
		var lenBuf [2]byte
		binary.LittleEndian.PutUint16(lenBuf[:], uint16(n))
		out.Write(lenBuf[:])
		out.Write(packet[:n])
	}

	if out.Len() == 0 {
		return nil, fmt.Errorf("no audio frames produced")
	}
	return out.Bytes(), nil
}

// intBufToInt16 converts go-audio IntBuffer to []int16, normalizing bit depth.
func intBufToInt16(buf *goaudio.IntBuffer, bitDepth int) []int16 {
	samples := make([]int16, len(buf.Data))
	shift := bitDepth - 16
	if shift < 0 {
		shift = 0
	}
	for i, v := range buf.Data {
		samples[i] = int16(v >> shift)
	}
	return samples
}

// monoToStereo duplicates a mono signal into stereo.
func monoToStereo(mono []int16) []int16 {
	stereo := make([]int16, len(mono)*2)
	for i, s := range mono {
		stereo[i*2] = s
		stereo[i*2+1] = s
	}
	return stereo
}

// resample performs simple linear interpolation resampling.
// Input and output are interleaved with the given channel count.
func resample(samples []int16, channels, srcRate, dstRate int) []int16 {
	srcFrames := len(samples) / channels
	dstFrames := int(int64(srcFrames) * int64(dstRate) / int64(srcRate))
	out := make([]int16, dstFrames*channels)

	for i := 0; i < dstFrames; i++ {
		// Map destination frame to source position
		srcPos := float64(i) * float64(srcRate) / float64(dstRate)
		srcIdx := int(srcPos)
		frac := srcPos - float64(srcIdx)

		for ch := 0; ch < channels; ch++ {
			idx0 := srcIdx*channels + ch
			idx1 := idx0 + channels
			if idx1 >= len(samples) {
				idx1 = idx0
			}
			v := float64(samples[idx0])*(1-frac) + float64(samples[idx1])*frac
			out[i*channels+ch] = int16(v)
		}
	}
	return out
}

