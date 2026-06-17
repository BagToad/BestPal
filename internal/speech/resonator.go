package speech

import "math"

// resonator is a second-order digital resonator (two-pole filter).
type resonator struct {
	a, b, c float32 // filter coefficients
	p1, p2  float32 // delay state
}

// initResonator computes coefficients for a resonator at the given frequency
// and bandwidth (both in Hz) at the given sample rate.
func (r *resonator) initResonator(freq, bw, sampleRate int) {
	arg := (-math.Pi / float64(sampleRate)) * float64(bw)
	rr := float32(math.Exp(arg))
	r.c = -(rr * rr)
	arg = (-2.0 * math.Pi / float64(sampleRate)) * float64(freq)
	r.b = rr * float32(math.Cos(arg)) * 2.0
	r.a = 1.0 - r.b - r.c
}

// initAntiresonator computes coefficients for an antiresonator (nasal zero).
func (r *resonator) initAntiresonator(freq, bw, sampleRate int) {
	r.initResonator(freq, bw, sampleRate)
	// Convert to antiresonator: a' = 1/a, b' = -b/a, c' = -c/a
	r.a = 1.0 / r.a
	r.b *= -r.a
	r.c *= -r.a
}

// setGain multiplies the gain coefficient.
func (r *resonator) setGain(g float32) {
	r.a *= g
}

// resonate filters one sample through the resonator.
func (r *resonator) resonate(input float32) float32 {
	x := r.a*input + r.b*r.p1 + r.c*r.p2
	r.p2 = r.p1
	r.p1 = x
	return x
}

// antiresonate filters one sample through the antiresonator.
func (r *resonator) antiresonate(input float32) float32 {
	x := r.a*input + r.b*r.p1 + r.c*r.p2
	r.p2 = r.p1
	r.p1 = input // antiresonator saves inputs, not outputs
	return x
}

// reset clears the filter state.
func (r *resonator) reset() {
	r.p1, r.p2 = 0, 0
}
