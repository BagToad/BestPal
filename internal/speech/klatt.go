package speech

import "math"

// Waveform types for the glottal source.
const (
	WaveSaw = iota
	WaveTriangle
	WaveSin
	WaveSquare
	WaveRosenberg
)

// Rosenberg glottal-pulse shape parameters. rosenRise is the fraction of the
// open phase spent opening (rising to peak flow); the remainder is the steeper
// closing phase whose abrupt end excites the vocal tract. rosenAmp scales the
// pulse to roughly the same output level as the legacy square source.
const (
	rosenRise = 0.66
	rosenAmp  = 11000.0
)

// klattFrame holds parameters for one synthesis frame.
type klattFrame struct {
	f0                       int // fundamental frequency * 10
	voicingAmp               int
	formant1Freq             int
	formant1BW               int
	formant2Freq             int
	formant2BW               int
	formant3Freq             int
	formant3BW               int
	formant4Freq             int
	formant4BW               int
	formant5Freq             int
	formant5BW               int
	formant6Freq             int
	formant6BW               int
	nasalZeroFreq            int
	nasalZeroBW              int
	nasalPoleFreq            int
	nasalPoleBW              int
	aspirationAmp            int
	samplesInOpenPeriod      int
	voicingBreathiness       int
	voicingSpectralTilt      int
	fricationAmp             int
	skewness                 int
	formant1Amp              int
	formant1ParallelBW       int
	formant2Amp              int
	formant2ParallelBW       int
	formant3Amp              int
	formant3ParallelBW       int
	formant4Amp              int
	formant4ParallelBW       int
	formant5Amp              int
	formant5ParallelBW       int
	formant6Amp              int
	formant6ParallelBW       int
	parallelNasalAmp         int
	bypassFricationAmp       int
	parallelVoicingAmp       int
	overallGain              int
}

func newKlattFrame() klattFrame {
	return klattFrame{
		f0: 1330, voicingAmp: 60,
		formant1Freq: 500, formant1BW: 60,
		formant2Freq: 1500, formant2BW: 90,
		formant3Freq: 2800, formant3BW: 150,
		formant4Freq: 3250, formant4BW: 200,
		formant5Freq: 3700, formant5BW: 200,
		formant6Freq: 4990, formant6BW: 500,
		nasalZeroFreq: 270, nasalZeroBW: 100,
		nasalPoleFreq: 270, nasalPoleBW: 100,
		samplesInOpenPeriod: 30, voicingSpectralTilt: 10,
		formant1ParallelBW: 80, formant2ParallelBW: 200,
		formant3ParallelBW: 350, formant4ParallelBW: 500,
		formant5ParallelBW: 600, formant6ParallelBW: 800,
		overallGain: 62,
	}
}

// slope holds transition boundary value and time.
type slope struct {
	value float32
	time  int
}

// klatt is the formant synthesizer.
type klatt struct {
	// Configuration
	baseF0          int
	baseSpeed       float32
	baseDeclination float32
	baseWaveform    int

	// Runtime state
	sampleRate int
	nspFr      int // samples per frame
	f0Flutter  int

	f0           int
	voicingAmp   int
	skewness     int
	timeCount    int
	nPer         int
	t0           int
	nOpen        int
	nMod         int
	ampVoice     float32
	ampBypas     float32
	ampAspir     float32
	ampFrica     float32
	ampBreth     float32
	skew         int
	vLast        float32
	nLast        float32
	glotLast     float32
	decay        float32
	oneMd        float32
	seed         uint32

	// Resonators
	parallelF1, parallelF2, parallelF3 resonator
	parallelF4, parallelF5, parallelF6 resonator
	parallelNasal                      resonator
	nasalPole, nasalZero               resonator
	glotLP, downsampLP, outputLP       resonator

	// Synthesis state
	frame        klattFrame
	elements     []byte
	elemIndex    int
	lastElement  *Element
	tStress      int
	ntStress     int
	stressS      slope
	stressE      slope
	top          float32
}

// ampTable converts dB to linear amplitude (0-87 dB range).
var ampTable = [88]float32{
	0.0, 0.0, 0.0, 0.0, 0.0,
	0.0, 0.0, 0.0, 0.0, 0.0,
	0.0, 0.0, 0.0, 6.0, 7.0,
	8.0, 9.0, 10.0, 11.0, 13.0,
	14.0, 16.0, 18.0, 20.0, 22.0,
	25.0, 28.0, 32.0, 35.0, 40.0,
	45.0, 51.0, 57.0, 64.0, 71.0,
	80.0, 90.0, 101.0, 114.0, 128.0,
	142.0, 159.0, 179.0, 202.0, 227.0,
	256.0, 284.0, 318.0, 359.0, 405.0,
	455.0, 512.0, 568.0, 638.0, 719.0,
	811.0, 911.0, 1024.0, 1137.0, 1276.0,
	1438.0, 1622.0, 1823.0, 2048.0, 2273.0,
	2552.0, 2875.0, 3244.0, 3645.0, 4096.0,
	4547.0, 5104.0, 5751.0, 6488.0, 7291.0,
	8192.0, 9093.0, 10207.0, 11502.0, 12976.0,
	14582.0, 16384.0, 18350.0, 20644.0, 23429.0,
	26214.0, 29491.0, 32767.0,
}

func dbToLin(dB int) float32 {
	if dB < 0 {
		dB = 0
	} else if dB >= 88 {
		dB = 87
	}
	return ampTable[dB] * 0.001
}

func clip(v float32) int16 {
	if v < -32767 {
		return -32767
	}
	if v > 32767 {
		return 32767
	}
	return int16(v)
}

const ampAdj = 14

// klattSampleRate is the synthesizer's internal output rate. 22050 Hz gives an
// 11 kHz Nyquist so fricatives (s, sh, f, th) keep their high-frequency energy
// instead of being dulled by an 11025 Hz / 5.5 kHz ceiling.
const klattSampleRate = 22050

// baseFrameMs is the synthesizer frame length in milliseconds and doubles as the
// master tempo control (nspFr = sampleRate*baseFrameMs/1000). Larger values
// stretch every phoneme and pause without changing pitch or the intonation
// contour. 10.0 is the classic Klatt frame; 11.0 slows delivery ~10% for clearer,
// more deliberate articulation.
const baseFrameMs = 11.0

func newKlatt() *klatt {
	return &klatt{
		baseF0:          1330,
		baseSpeed:       baseFrameMs,
		baseDeclination: 0.5,
		baseWaveform:    WaveRosenberg,
		seed:            5,
	}
}

func (k *klatt) init() {
	k.sampleRate = klattSampleRate
	k.f0Flutter = 0
	k.f0 = k.baseF0
	k.frame = newKlattFrame()
	k.frame.f0 = k.baseF0

	flpHz := (950 * k.sampleRate) / 10000
	blpHz := (630 * k.sampleRate) / 10000
	k.nspFr = int(float32(k.sampleRate)*k.baseSpeed) / 1000

	k.downsampLP.initResonator(flpHz, blpHz, k.sampleRate)
	k.nPer = 0
	k.t0 = 0
	k.vLast = 0
	k.nLast = 0
	k.glotLast = 0
}

func (k *klatt) naturalSource(nPer int) float32 {
	if nPer >= k.nOpen {
		return 0
	}
	switch k.baseWaveform {
	case WaveTriangle:
		return float32((nPer%200)-100) * 81.92
	case WaveSin:
		return float32(math.Sin(float64(nPer)*0.0314)) * 8192
	case WaveSquare:
		if (nPer%200)-100 > 0 {
			return 8192
		}
		return -8192
	case WaveRosenberg:
		// Rosenberg glottal flow pulse: a smooth cosine rise to peak flow, then
		// a steeper cosine fall to a hard closure at nOpen. Unlike the square
		// source, its harmonics roll off gently, which removes the buzz; the
		// sharp slope at closure still gives a crisp excitation.
		open := float32(k.nOpen)
		tp := open * rosenRise
		n := float32(nPer)
		if n < tp {
			return rosenAmp * 0.5 * (1 - float32(math.Cos(float64(math.Pi*n/tp))))
		}
		tn := open - tp
		return rosenAmp * float32(math.Cos(float64(math.Pi*(n-tp)/(2*tn))))
	default: // WaveSaw
		return float32(abs((nPer%200)-100)-50) * 163.84
	}
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func (k *klatt) flutter() {
	origF0 := k.frame.f0 / 10
	fla := float64(k.f0Flutter) / 50
	flb := float64(origF0) / 100
	t := float64(k.timeCount)
	flc := math.Sin(2 * math.Pi * 12.7 * t)
	fld := math.Sin(2 * math.Pi * 7.1 * t)
	fle := math.Sin(2 * math.Pi * 4.7 * t)
	delta := fla * flb * (flc + fld + fle) * 10
	k.f0 += int(delta)
}

func (k *klatt) pitchSynchParReset(ns int) {
	if k.f0 > 0 {
		k.t0 = (40 * k.sampleRate) / k.f0
		k.ampVoice = dbToLin(k.voicingAmp)
		k.nMod = k.t0
		if k.voicingAmp > 0 {
			k.nMod >>= 1
		}
		k.ampBreth = dbToLin(k.frame.voicingBreathiness) * 0.1
		k.nOpen = 4 * k.frame.samplesInOpenPeriod
		if k.nOpen >= k.t0-1 {
			k.nOpen = k.t0 - 2
		}
		if k.nOpen < 40 {
			k.nOpen = 40
		}
		temp := k.sampleRate / k.nOpen
		k.glotLP.initResonator(0, temp, k.sampleRate)
		temp1 := float32(k.nOpen) * 0.00833
		k.glotLP.setGain(temp1 * temp1)
		tempSkew := k.t0 - k.nOpen
		if k.skewness > tempSkew {
			k.skewness = tempSkew
		}
		if k.skew >= 0 {
			k.skew = k.skewness
		} else {
			k.skew = -k.skewness
		}
		k.t0 += k.skew
		k.skew = -k.skew
	} else {
		k.t0 = 4
		k.ampVoice = 0
		k.nMod = k.t0
		k.ampBreth = 0
	}

	if k.t0 != 4 || ns == 0 {
		k.decay = 0.033 * float32(k.frame.voicingSpectralTilt)
		if k.decay > 0 {
			k.oneMd = 1.0 - k.decay
		} else {
			k.oneMd = 1.0
		}
	}
}

func (k *klatt) frameInit() {
	k.f0 = k.frame.f0
	k.voicingAmp = k.frame.voicingAmp - 7
	if k.voicingAmp < 0 {
		k.voicingAmp = 0
	}
	k.ampAspir = dbToLin(k.frame.aspirationAmp) * 0.05
	k.ampFrica = dbToLin(k.frame.fricationAmp) * 0.25
	k.skewness = k.frame.skewness

	ampParF1 := dbToLin(k.frame.formant1Amp) * 0.4
	ampParF2 := dbToLin(k.frame.formant2Amp) * 0.15
	ampParF3 := dbToLin(k.frame.formant3Amp) * 0.06
	ampParF4 := dbToLin(k.frame.formant4Amp) * 0.04
	ampParF5 := dbToLin(k.frame.formant5Amp) * 0.022
	ampParF6 := dbToLin(k.frame.formant6Amp) * 0.03
	ampParFN := dbToLin(k.frame.parallelNasalAmp) * 0.6
	k.ampBypas = dbToLin(k.frame.bypassFricationAmp) * 0.05

	k.nasalPole.initResonator(k.frame.nasalPoleFreq, k.frame.nasalPoleBW, k.sampleRate)
	k.nasalZero.initAntiresonator(k.frame.nasalZeroFreq, k.frame.nasalZeroBW, k.sampleRate)

	k.parallelF1.initResonator(k.frame.formant1Freq, k.frame.formant1ParallelBW, k.sampleRate)
	k.parallelF1.setGain(ampParF1)
	k.parallelNasal.initResonator(k.frame.nasalPoleFreq, k.frame.nasalPoleBW, k.sampleRate)
	k.parallelNasal.setGain(ampParFN)
	k.parallelF2.initResonator(k.frame.formant2Freq, k.frame.formant2ParallelBW, k.sampleRate)
	k.parallelF2.setGain(ampParF2)
	k.parallelF3.initResonator(k.frame.formant3Freq, k.frame.formant3ParallelBW, k.sampleRate)
	k.parallelF3.setGain(ampParF3)
	k.parallelF4.initResonator(k.frame.formant4Freq, k.frame.formant4ParallelBW, k.sampleRate)
	k.parallelF4.setGain(ampParF4)
	k.parallelF5.initResonator(k.frame.formant5Freq, k.frame.formant5ParallelBW, k.sampleRate)
	k.parallelF5.setGain(ampParF5)
	k.parallelF6.initResonator(k.frame.formant6Freq, k.frame.formant6ParallelBW, k.sampleRate)
	k.parallelF6.setGain(ampParF6)

	overallGain := k.frame.overallGain - 3
	if overallGain <= 0 {
		overallGain = 57
	}
	k.outputLP.initResonator(0, k.sampleRate, k.sampleRate)
	k.outputLP.setGain(dbToLin(overallGain))
}

func (k *klatt) parwave(out []int16) {
	k.frameInit()
	if k.f0Flutter != 0 {
		k.timeCount++
		k.flutter()
	}

	for ns := 0; ns < k.nspFr; ns++ {
		// Random noise source
		k.seed = k.seed*1664525 + 1
		nrand := int32((k.seed << (32 - 32)) >> (32 - 14))
		noise := float32(nrand) + 0.75*k.nLast
		k.nLast = noise
		if k.nPer > k.nMod {
			noise *= 0.5
		}

		frics := k.ampFrica * noise
		sourc := frics

		// Voicing at 4x sample rate
		var voice float32
		for n4 := 0; n4 < 4; n4++ {
			voice = k.naturalSource(k.nPer)
			if k.nPer >= k.t0 {
				k.nPer = 0
				k.pitchSynchParReset(ns)
			}
			voice = k.downsampLP.resonate(voice)
			k.nPer++
		}

		voice = voice*k.oneMd + k.vLast*k.decay
		k.vLast = voice
		if k.nPer < k.nOpen {
			voice += k.ampBreth * float32(nrand)
		}
		glotout := k.ampVoice * voice
		aspiration := k.ampAspir * noise
		glotout += aspiration
		parGlotout := glotout

		parGlotout = k.nasalZero.antiresonate(parGlotout)
		parGlotout = k.nasalPole.resonate(parGlotout)
		outSamp := k.parallelF1.resonate(parGlotout)
		sourc += parGlotout - k.glotLast
		k.glotLast = parGlotout

		outSamp = k.parallelF6.resonate(sourc) - outSamp
		outSamp = k.parallelF5.resonate(sourc) - outSamp
		outSamp = k.parallelF4.resonate(sourc) - outSamp
		outSamp = k.parallelF3.resonate(sourc) - outSamp
		outSamp = k.parallelF2.resonate(sourc) - outSamp
		outSamp = k.ampBypas*sourc - outSamp
		outSamp = k.outputLP.resonate(outSamp)

		out[ns] = clip(outSamp)
	}
}

func (k *klatt) initSynth(elems []byte) {
	k.elements = elems
	k.elemIndex = 0
	k.lastElement = &elements[0]
	k.seed = 5
	k.tStress = 0
	k.ntStress = 0
	k.frame.f0 = k.baseF0
	k.top = 1.1 * float32(k.frame.f0)
	k.frame.nasalPoleFreq = int(k.lastElement.Interp[ElmFN].Steady)
	k.frame.formant1ParallelBW = 60
	k.frame.formant1BW = 60
	k.frame.formant2ParallelBW = 90
	k.frame.formant2BW = 90
	k.frame.formant3ParallelBW = 150
	k.frame.formant3BW = 150
	k.stressS.time = 40
	k.stressE.time = 40
	k.stressE.value = 0
}

func lerp(a, b float32, t, d int) float32 {
	if t <= 0 {
		return a
	}
	if t >= d {
		return b
	}
	f := float32(t) / float32(d)
	return a + (b-a)*f
}

func interpolate(start, end *slope, mid float32, t, dur int) float32 {
	steady := dur - (start.time + end.time)
	if steady >= 0 {
		if t < start.time {
			return lerp(start.value, mid, t, start.time)
		}
		t -= start.time
		if t <= steady {
			return mid
		}
		return lerp(mid, end.value, t-steady, end.time)
	}
	f := 1.0 - float32(t)/float32(dur)
	sp := lerp(start.value, mid, t, start.time)
	ep := lerp(end.value, mid, dur-t, end.time)
	return f*sp + (1.0-f)*ep
}

func setTrans(t []slope, a, b *Element, ext bool) {
	for i := 0; i < ElmCount; i++ {
		if ext {
			t[i].time = int(a.Interp[i].ExtDelay)
		} else {
			t[i].time = int(a.Interp[i].IntDelay)
		}
		if t[i].time != 0 {
			t[i].value = a.Interp[i].Fixed + (float32(a.Interp[i].Proportion)*b.Interp[i].Steady)*0.01
		} else {
			t[i].value = b.Interp[i].Steady
		}
	}
}

// synth synthesizes one element and returns the number of samples produced.
// Returns -1 when synthesis is complete.
func (k *klatt) synth(out []int16) int {
	if k.elemIndex >= len(k.elements) {
		return -1
	}

	elemIdx := int(k.elements[k.elemIndex])
	k.elemIndex++
	dur := int(k.elements[k.elemIndex])
	k.elemIndex++
	k.elemIndex++ // skip stress byte

	current := &elements[elemIdx]

	if current.RK == 31 { // END element
		k.frame.f0 = k.baseF0
		k.top = 1.1 * float32(k.frame.f0)
	}

	if dur <= 0 {
		k.lastElement = current
		return 0
	}

	var ne *Element
	if k.elemIndex < len(k.elements) {
		ne = &elements[k.elements[k.elemIndex]]
	} else {
		ne = &elements[0]
	}

	start := make([]slope, ElmCount)
	end := make([]slope, ElmCount)

	if current.RK > k.lastElement.RK {
		setTrans(start, current, k.lastElement, false)
	} else {
		setTrans(start, k.lastElement, current, true)
	}
	if ne.RK > current.RK {
		setTrans(end, ne, current, true)
	} else {
		setTrans(end, current, ne, false)
	}

	samples := 0
	for t := 0; t < dur; t++ {
		base := k.top * 0.8
		tp := make([]float32, ElmCount)

		if k.tStress == k.ntStress {
			j := k.elemIndex
			k.stressS = k.stressE
			k.tStress = 0
			k.ntStress = dur

			for j <= len(k.elements) {
				var e *Element
				var du, s int
				if j < len(k.elements) {
					e = &elements[k.elements[j]]
					j++
				} else {
					e = &elements[0]
				}
				if j < len(k.elements) {
					du = int(k.elements[j])
					j++
				}
				if j < len(k.elements) {
					s = int(k.elements[j])
					j++
				} else {
					s = 3
				}

				if s != 0 || (e.Feat&FeatureVWL) != 0 {
					if s != 0 {
						k.stressE.value = float32(s) / 3
					} else {
						k.stressE.value = 0.1
					}
					d := 0
					for {
						d += du
						if j >= len(k.elements) {
							break
						}
						e = &elements[k.elements[j]]
						j++
						if j >= len(k.elements) {
							break
						}
						du = int(k.elements[j])
						j++
						if (e.Feat&FeatureVWL) == 0 {
							break
						}
						if j >= len(k.elements) {
							break
						}
						if int(k.elements[j]) != s {
							j++
							break
						}
						j++
					}
					k.ntStress += d / 2
					break
				}
				k.ntStress += du
			}
		}

		for j := 0; j < ElmCount; j++ {
			tp[j] = interpolate(&start[j], &end[j], current.Interp[j].Steady, t, dur)
		}

		k.frame.f0 = int(base + (k.top-base)*interpolate(&k.stressS, &k.stressE, 0, k.tStress, k.ntStress))
		k.frame.voicingAmp = int(tp[ElmAV])
		k.frame.parallelVoicingAmp = int(tp[ElmAV])
		k.frame.fricationAmp = int(tp[ElmAF])
		k.frame.nasalZeroFreq = int(tp[ElmFN])
		k.frame.aspirationAmp = int(tp[ElmASP])
		k.frame.voicingBreathiness = int(tp[ElmAVC])
		k.frame.formant1BW = int(tp[ElmB1])
		k.frame.formant1ParallelBW = int(tp[ElmB1])
		k.frame.formant2BW = int(tp[ElmB2])
		k.frame.formant2ParallelBW = int(tp[ElmB2])
		k.frame.formant3BW = int(tp[ElmB3])
		k.frame.formant3ParallelBW = int(tp[ElmB3])
		k.frame.formant1Freq = int(tp[ElmF1])
		k.frame.formant2Freq = int(tp[ElmF2])
		k.frame.formant3Freq = int(tp[ElmF3])
		k.frame.bypassFricationAmp = ampAdj + int(tp[ElmAB])
		k.frame.formant5Amp = ampAdj + int(tp[ElmA5])
		k.frame.formant6Amp = ampAdj + int(tp[ElmA6])
		k.frame.formant1Amp = ampAdj + int(tp[ElmA1])
		k.frame.formant2Amp = ampAdj + int(tp[ElmA2])
		k.frame.formant3Amp = ampAdj + int(tp[ElmA3])
		k.frame.formant4Amp = ampAdj + int(tp[ElmA4])

		k.parwave(out[samples : samples+k.nspFr])
		samples += k.nspFr
		k.top -= k.baseDeclination
		k.tStress++
	}

	k.lastElement = current
	return samples
}
