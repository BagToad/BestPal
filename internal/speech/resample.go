package speech

import "math"

// resample converts PCM from inRate to outRate using a windowed-sinc (Lanczos)
// kernel. The Klatt synthesizer runs at 22050 Hz; Discord voice needs 48000 Hz.
// Sinc interpolation rejects the imaging artifacts that plain linear
// interpolation leaves behind, so consonants stay clean after upsampling.
func resample(in []int16, inRate, outRate int) []int16 {
	if len(in) == 0 || inRate <= 0 || outRate <= 0 {
		return nil
	}
	if inRate == outRate {
		out := make([]int16, len(in))
		copy(out, in)
		return out
	}

	outLen := int(int64(len(in)) * int64(outRate) / int64(inRate))
	if outLen <= 0 {
		return nil
	}
	out := make([]int16, outLen)

	// Input samples per output sample. For upsampling (ratio < 1) the kernel
	// cutoff is the input Nyquist, so a fixed half-width in input samples works.
	ratio := float64(inRate) / float64(outRate)
	const halfWidth = 8 // Lanczos a: number of input samples each side

	for i := range out {
		center := float64(i) * ratio
		base := int(math.Floor(center))

		var acc, wsum float64
		for j := base - halfWidth + 1; j <= base+halfWidth; j++ {
			if j < 0 || j >= len(in) {
				continue
			}
			w := lanczos(center-float64(j), halfWidth)
			acc += float64(in[j]) * w
			wsum += w
		}
		if wsum != 0 {
			acc /= wsum
		}
		out[i] = clip(float32(acc))
	}
	return out
}

// lanczos evaluates the Lanczos kernel of order a at x.
func lanczos(x float64, a int) float64 {
	if x == 0 {
		return 1
	}
	af := float64(a)
	if x <= -af || x >= af {
		return 0
	}
	return sinc(x) * sinc(x/af)
}

func sinc(x float64) float64 {
	px := math.Pi * x
	return math.Sin(px) / px
}
