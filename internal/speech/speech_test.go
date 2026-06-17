package speech

import (
	"encoding/binary"
	"math"
	"os"
	"strings"
	"testing"

	"github.com/thesyncim/gopus"
)

func TestG2P(t *testing.T) {
	cases := []struct {
		text string
		want string
	}{
		{"hello", "h@l'@U  "},
		{"world", "w'3ld  "},
		{"the quick brown fox", "D@  kw'Ik  br'aUn  f'Aks  "},
		{"one two three", "w'Vn  t'u  Tr'i  "},
		{"42", "f'Orti  t'u  "},
		{"3.5", "Tr'i  p'oInt  f'aIv  "},
	}
	for _, c := range cases {
		var p phonemeBuf
		xlateString(c.text, &p)
		if got := string(p.b); got != c.want {
			t.Errorf("xlateString(%q) = %q, want %q", c.text, got, c.want)
		}
	}
}

func TestManualDict(t *testing.T) {
	// The manual override map is the top layer: project/brand names and slang
	// CMUdict does not list. These must resolve from dict.go, not the rules.
	cases := []struct {
		text string
		want string
	}{
		{"bagtoad", "b'&gt,@Ud"},
		{"bestpal", "b'estp,&l"},
		{"gamerpal", "g'eIm3p,&l"},
		{"emoji", "Im'@UdZi"},
		{"esports", "'isp,Orts"},
		{"poggers", "p'0g3z"},
		{"noob", "n'ub"},
	}
	for _, c := range cases {
		var p phonemeBuf
		xlateString(c.text, &p)
		if got := strings.TrimRight(string(p.b), " "); got != c.want {
			t.Errorf("xlateString(%q) = %q, want %q", c.text, got, c.want)
		}
	}
}

func TestCMUDict(t *testing.T) {
	// Words the letter-to-sound rules mispronounce; CMUdict, the comprehensive
	// base lexicon, must resolve them correctly (with secondary stress where the
	// dictionary marks it).
	cases := []struct {
		text string
		want string
	}{
		{"everyone", "'evriw,Vn"},
		{"offline", "'Ofl,aIn"},
		{"rhythm", "r'ID@m"},
		{"completely", "k@mpl'itli"},
		{"external", "Ikst'3n@l"},
		{"before", "bIf'Or"},
		{"character", "k'erIkt3"},
		{"computer", "k@mpj'ut3"},
	}
	for _, c := range cases {
		var p phonemeBuf
		xlateString(c.text, &p)
		if got := strings.TrimRight(string(p.b), " "); got != c.want {
			t.Errorf("xlateString(%q) = %q, want %q", c.text, got, c.want)
		}
	}
}

func TestConvertARPABET(t *testing.T) {
	cases := []struct {
		tokens []string
		want   string
	}{
		{[]string{"HH", "AH0", "L", "OW1"}, "h@l'@U"},                         // hello, AH0 -> schwa
		{[]string{"K", "AH0", "M", "P", "Y", "UW1", "T", "ER0"}, "k@mpj'ut3"}, // computer
		{[]string{"AO1", "F", "L", "AY2", "N"}, "'Ofl,aIn"},                   // offline, secondary stress
		{[]string{"W", "AH1", "N"}, "w'Vn"},                                   // one, stressed AH -> wedge
	}
	for _, c := range cases {
		got, ok := convertARPABET(c.tokens)
		if !ok {
			t.Errorf("convertARPABET(%v) returned ok=false", c.tokens)
			continue
		}
		if got != c.want {
			t.Errorf("convertARPABET(%v) = %q, want %q", c.tokens, got, c.want)
		}
	}
}

func TestFunctionWordReduction(t *testing.T) {
	// Function words must lose their stress marks so the sentence keeps its
	// rhythm, even though CMUdict lists them stressed.
	cases := []struct {
		text string
		want string
	}{
		{"to", "tu"},
		{"with", "wID"},
		{"are", "Ar"},
	}
	for _, c := range cases {
		var p phonemeBuf
		xlateString(c.text, &p)
		if got := strings.TrimRight(string(p.b), " "); got != c.want {
			t.Errorf("xlateString(%q) = %q, want %q", c.text, got, c.want)
		}
	}
}

func TestSynthesizeProducesAudio(t *testing.T) {
	pcm := SynthesizePCM("hello world")
	if len(pcm) == 0 {
		t.Fatal("expected non-empty PCM")
	}
	// Roughly a second of speech at 22050 Hz; just sanity-check the range.
	if len(pcm) < 4000 || len(pcm) > 22050*5 {
		t.Fatalf("unexpected PCM length %d", len(pcm))
	}

	var peak int16
	for _, s := range pcm {
		if s > peak {
			peak = s
		}
	}
	if peak < 1000 {
		t.Fatalf("audio too quiet, peak=%d", peak)
	}
}

func TestSynthesizeOpusRoundTrip(t *testing.T) {
	frames, err := Synthesize("testing one two three")
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) == 0 {
		t.Fatal("expected Opus frames")
	}

	dec, err := gopus.NewDecoder(gopus.DecoderConfig{
		SampleRate: discordSampleRate,
		Channels:   discordChannels,
	})
	if err != nil {
		t.Fatal(err)
	}
	pcm := make([]int16, opusFrameSamples)
	for i, f := range frames {
		n, err := dec.DecodeInt16(f, pcm)
		if err != nil {
			t.Fatalf("frame %d: decode failed: %v", i, err)
		}
		if n != opusFrameSamples {
			t.Fatalf("frame %d: decoded %d samples, want %d", i, n, opusFrameSamples)
		}
	}
}

// TestVoicedSpectrum confirms a sustained vowel is voiced: energy at the
// fundamental and its harmonics dwarfs out-of-band energy.
func TestVoicedSpectrum(t *testing.T) {
	pcm := SynthesizePCM("are")
	if len(pcm) < 2000 {
		t.Fatalf("vowel too short: %d samples", len(pcm))
	}
	// Analyse the steady middle third to avoid onset/offset transients.
	lo, hi := len(pcm)/3, 2*len(pcm)/3
	seg := pcm[lo:hi]

	const f0 = 133.0
	harmonic := goertzel(seg, f0, nativeSampleRate) +
		goertzel(seg, 2*f0, nativeSampleRate) +
		goertzel(seg, 3*f0, nativeSampleRate)
	outOfBand := goertzel(seg, 5200, nativeSampleRate)

	if harmonic < outOfBand*8 {
		t.Fatalf("signal not clearly voiced: harmonic=%.3g outOfBand=%.3g", harmonic, outOfBand)
	}
}

// goertzel returns the power of a single frequency bin.
func goertzel(x []int16, freq, sampleRate float64) float64 {
	w := 2 * math.Pi * freq / sampleRate
	cw := math.Cos(w)
	coeff := 2 * cw
	var s0, s1, s2 float64
	for _, v := range x {
		s0 = float64(v) + coeff*s1 - s2
		s2 = s1
		s1 = s0
	}
	return s1*s1 + s2*s2 - coeff*s1*s2
}

// TestWriteWAVArtifact dumps a WAV file when SPEECH_WAV_OUT is set so the
// synthesizer can be auditioned. Skipped during normal test runs.
func TestWriteWAVArtifact(t *testing.T) {
	out := os.Getenv("SPEECH_WAV_OUT")
	if out == "" {
		t.Skip("set SPEECH_WAV_OUT to write a WAV artifact")
	}
	text := os.Getenv("SPEECH_WAV_TEXT")
	if text == "" {
		text = "Hello, this is your bot speaking, fully offline."
	}
	pcm := resample(SynthesizePCM(text), nativeSampleRate, discordSampleRate)
	if err := writeWAV(out, pcm, discordSampleRate); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %d samples to %s", len(pcm), out)
}

func writeWAV(path string, pcm []int16, sampleRate int) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	dataBytes := len(pcm) * 2
	var hdr [44]byte
	copy(hdr[0:4], "RIFF")
	binary.LittleEndian.PutUint32(hdr[4:8], uint32(36+dataBytes))
	copy(hdr[8:12], "WAVE")
	copy(hdr[12:16], "fmt ")
	binary.LittleEndian.PutUint32(hdr[16:20], 16)
	binary.LittleEndian.PutUint16(hdr[20:22], 1)
	binary.LittleEndian.PutUint16(hdr[22:24], 1)
	binary.LittleEndian.PutUint32(hdr[24:28], uint32(sampleRate))
	binary.LittleEndian.PutUint32(hdr[28:32], uint32(sampleRate*2))
	binary.LittleEndian.PutUint16(hdr[32:34], 2)
	binary.LittleEndian.PutUint16(hdr[34:36], 16)
	copy(hdr[36:40], "data")
	binary.LittleEndian.PutUint32(hdr[40:44], uint32(dataBytes))
	if _, err := f.Write(hdr[:]); err != nil {
		return err
	}

	buf := make([]byte, dataBytes)
	for i, s := range pcm {
		binary.LittleEndian.PutUint16(buf[i*2:], uint16(s))
	}
	_, err = f.Write(buf)
	return err
}
