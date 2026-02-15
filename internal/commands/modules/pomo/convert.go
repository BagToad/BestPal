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

// convertToOpusFrames converts raw audio data (mp3, wav) to
// 2-byte LE length-prefixed opus frames suitable for Discord voice.
// Streams the conversion in chunks to handle large files without
// loading the entire PCM buffer into memory.
func convertToOpusFrames(input []byte, filename string) ([]byte, error) {
	ext := strings.ToLower(filename)

	// pcmReader delivers interleaved stereo int16 LE PCM at 48kHz
	var pcmReader io.Reader
	var err error

	switch {
	case strings.HasSuffix(ext, ".mp3"):
		pcmReader, err = newMp3PcmReader(input)
	case strings.HasSuffix(ext, ".wav"):
		pcmReader, err = newWavPcmReader(input)
	default:
		return nil, fmt.Errorf("unsupported format: %s (supported: mp3, wav)", ext)
	}
	if err != nil {
		return nil, fmt.Errorf("decode audio: %w", err)
	}

	return streamEncodeOpus(pcmReader)
}

// streamEncodeOpus reads interleaved stereo int16 LE PCM from r one frame
// at a time and encodes each frame to opus, returning length-prefixed output.
func streamEncodeOpus(r io.Reader) ([]byte, error) {
	enc, err := opusenc.NewEncoder(opusSampleRate, opusChannels, opusenc.ApplicationAudio)
	if err != nil {
		return nil, fmt.Errorf("opus encoder: %w", err)
	}
	defer enc.Close()

	if err := enc.SetBitrate(opusBitrate); err != nil {
		return nil, fmt.Errorf("set bitrate: %w", err)
	}

	samplesPerFrame := opusFrameSize * opusChannels
	// Read one opus frame worth of PCM bytes at a time (960 samples * 2 channels * 2 bytes)
	pcmBuf := make([]byte, samplesPerFrame*2)
	pcmSamples := make([]int16, samplesPerFrame)
	packet := make([]byte, 4000)
	var out bytes.Buffer

	for {
		_, err := io.ReadFull(r, pcmBuf)
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read pcm: %w", err)
		}

		for i := range pcmSamples {
			pcmSamples[i] = int16(binary.LittleEndian.Uint16(pcmBuf[i*2 : i*2+2]))
		}

		n, err := enc.Encode(pcmSamples, opusFrameSize, packet)
		if err != nil {
			return nil, fmt.Errorf("opus encode: %w", err)
		}

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

// newMp3PcmReader returns an io.Reader that produces interleaved stereo
// int16 LE PCM at 48kHz from MP3 data. Resamples if needed.
func newMp3PcmReader(data []byte) (io.Reader, error) {
	dec, err := mp3.NewDecoder(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("mp3 init: %w", err)
	}

	// go-mp3 outputs signed 16-bit LE stereo PCM — already an io.Reader
	var r io.Reader = dec
	if dec.SampleRate() != opusSampleRate {
		r = newResampleReader(r, opusChannels, dec.SampleRate(), opusSampleRate)
	}
	return r, nil
}

// newWavPcmReader returns an io.Reader that produces interleaved stereo
// int16 LE PCM at 48kHz from WAV data. Handles mono→stereo and resampling.
func newWavPcmReader(data []byte) (io.Reader, error) {
	dec := wav.NewDecoder(bytes.NewReader(data))
	if !dec.IsValidFile() {
		return nil, fmt.Errorf("invalid wav file")
	}

	srcChannels := int(dec.NumChans)
	srcRate := int(dec.SampleRate)
	bitDepth := int(dec.BitDepth)

	r := &wavChunkReader{
		dec:         dec,
		srcChannels: srcChannels,
		bitDepth:    bitDepth,
	}

	var pcmReader io.Reader = r
	if srcChannels == 1 {
		pcmReader = newMonoToStereoReader(pcmReader)
	}
	if srcRate != opusSampleRate {
		pcmReader = newResampleReader(pcmReader, opusChannels, srcRate, opusSampleRate)
	}
	return pcmReader, nil
}

// wavChunkReader reads WAV data in chunks and outputs int16 LE bytes.
type wavChunkReader struct {
	dec         *wav.Decoder
	srcChannels int
	bitDepth    int
	buf         []byte // leftover bytes from previous read
}

func (w *wavChunkReader) Read(p []byte) (int, error) {
	// Drain leftover first
	if len(w.buf) > 0 {
		n := copy(p, w.buf)
		w.buf = w.buf[n:]
		return n, nil
	}

	// Read a chunk of PCM from the WAV decoder
	chunkSamples := 4096
	intBuf := &goaudio.IntBuffer{
		Format:         w.dec.Format(),
		Data:           make([]int, chunkSamples),
		SourceBitDepth: w.bitDepth,
	}
	n, err := w.dec.PCMBuffer(intBuf)
	if n == 0 {
		if err == nil {
			err = io.EOF
		}
		return 0, err
	}

	// Convert int samples to int16 LE bytes
	shift := w.bitDepth - 16
	if shift < 0 {
		shift = 0
	}
	raw := make([]byte, n*2)
	for i := 0; i < n; i++ {
		s := int16(intBuf.Data[i] >> shift)
		binary.LittleEndian.PutUint16(raw[i*2:], uint16(s))
	}

	copied := copy(p, raw)
	if copied < len(raw) {
		w.buf = raw[copied:]
	}
	return copied, nil
}

// monoToStereoReader duplicates mono int16 LE samples to stereo.
type monoToStereoReader struct {
	src io.Reader
	buf []byte
}

func newMonoToStereoReader(src io.Reader) *monoToStereoReader {
	return &monoToStereoReader{src: src}
}

func (m *monoToStereoReader) Read(p []byte) (int, error) {
	if len(m.buf) > 0 {
		n := copy(p, m.buf)
		m.buf = m.buf[n:]
		return n, nil
	}

	// Read mono samples (2 bytes each), produce stereo (4 bytes each)
	monoBytes := make([]byte, len(p)/2)
	n, err := m.src.Read(monoBytes)
	if n == 0 {
		return 0, err
	}

	// Ensure we have complete samples
	n = (n / 2) * 2
	stereo := make([]byte, n*2)
	for i := 0; i < n; i += 2 {
		copy(stereo[i*2:], monoBytes[i:i+2])
		copy(stereo[i*2+2:], monoBytes[i:i+2])
	}

	copied := copy(p, stereo)
	if copied < len(stereo) {
		m.buf = stereo[copied:]
	}
	return copied, nil
}

// resampleReader performs streaming linear interpolation resampling on
// interleaved int16 LE PCM data.
type resampleReader struct {
	src      io.Reader
	channels int
	ratio    float64 // srcRate / dstRate
	srcPos   float64 // current fractional position in source frames
	prev     []int16 // previous source frame (per channel)
	curr     []int16 // current source frame (per channel)
	hasPrev  bool
	buf      []byte // leftover output bytes
	eof      bool
}

func newResampleReader(src io.Reader, channels, srcRate, dstRate int) *resampleReader {
	return &resampleReader{
		src:      src,
		channels: channels,
		ratio:    float64(srcRate) / float64(dstRate),
		prev:     make([]int16, channels),
		curr:     make([]int16, channels),
	}
}

func (r *resampleReader) readSourceFrame() error {
	buf := make([]byte, r.channels*2)
	_, err := io.ReadFull(r.src, buf)
	if err != nil {
		return err
	}
	copy(r.prev, r.curr)
	for ch := 0; ch < r.channels; ch++ {
		r.curr[ch] = int16(binary.LittleEndian.Uint16(buf[ch*2 : ch*2+2]))
	}
	r.hasPrev = true
	return nil
}

func (r *resampleReader) Read(p []byte) (int, error) {
	if len(r.buf) > 0 {
		n := copy(p, r.buf)
		r.buf = r.buf[n:]
		return n, nil
	}
	if r.eof {
		return 0, io.EOF
	}

	// Seed the first frame
	if !r.hasPrev {
		if err := r.readSourceFrame(); err != nil {
			return 0, err
		}
		copy(r.prev, r.curr)
	}

	// Generate output frames until we fill p or run out of source
	outFrameBytes := r.channels * 2
	maxFrames := len(p) / outFrameBytes
	if maxFrames == 0 {
		maxFrames = 1
	}
	out := make([]byte, 0, maxFrames*outFrameBytes)

	for len(out)+outFrameBytes <= cap(out) {
		srcIdx := int(r.srcPos)

		// Advance source frames to catch up
		for !r.eof && srcIdx > 0 {
			if err := r.readSourceFrame(); err != nil {
				r.eof = true
				break
			}
			srcIdx--
			r.srcPos -= 1.0
		}
		if r.eof {
			break
		}

		frac := r.srcPos - float64(int(r.srcPos))
		for ch := 0; ch < r.channels; ch++ {
			v := float64(r.prev[ch])*(1-frac) + float64(r.curr[ch])*frac
			var b [2]byte
			binary.LittleEndian.PutUint16(b[:], uint16(int16(v)))
			out = append(out, b[:]...)
		}
		r.srcPos += r.ratio
	}

	if len(out) == 0 {
		return 0, io.EOF
	}

	copied := copy(p, out)
	if copied < len(out) {
		r.buf = out[copied:]
	}
	return copied, nil
}
