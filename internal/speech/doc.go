// Package speech provides offline text-to-speech synthesis using a formant
// synthesizer ported from rsynth/Klatt (public domain). The pipeline is:
//
//  1. NRL letter-to-sound rules convert English text to phoneme strings.
//  2. Phoneme strings map to Klatt element sequences.
//  3. The Klatt formant synthesizer produces 11025 Hz mono PCM.
//  4. A linear resampler upsamples to 48000 Hz for Discord voice.
//  5. The gopus encoder frames the PCM into Opus packets.
//
// The synth produces robotic but intelligible speech entirely offline with no
// external dependencies or API keys.
//
// Provenance: the formant synth and phoneme tables are derived from rsynth by
// Nick Ing-Simmons (public domain) via the SoLoud speech module by Jari Komppa
// (CC0/public domain). The NRL rules are public domain US Navy Research Lab
// work. See https://github.com/jarikomppa/soloud for the original sources.
package speech
