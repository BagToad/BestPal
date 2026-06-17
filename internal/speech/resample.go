package speech

// resampleLinear converts PCM from inRate to outRate using linear
// interpolation. The Klatt synthesizer runs at 11025 Hz; Discord voice needs
// 48000 Hz, so we upsample before Opus encoding.
func resampleLinear(in []int16, inRate, outRate int) []int16 {
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
	ratio := float64(inRate) / float64(outRate)

	for i := range out {
		srcPos := float64(i) * ratio
		idx := int(srcPos)
		frac := srcPos - float64(idx)

		s0 := float64(in[idx])
		s1 := s0
		if idx+1 < len(in) {
			s1 = float64(in[idx+1])
		}
		out[i] = int16(s0 + (s1-s0)*frac)
	}
	return out
}
