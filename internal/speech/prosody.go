package speech

// This file adds prosody to the otherwise flat NRL output. The Klatt core
// already turns stress marks in the phoneme stream into an F0 contour (see
// klatt.synth), but the NRL letter-to-sound rules never emit any, so every
// syllable comes out on the same pitch. Here we assign one primary-stress mark
// per content word and leave common function words reduced, which gives the
// speech English-like rhythm and intonation.

// primaryStress is the phoneme-stream marker klatt reads as full stress.
const primaryStress = '\''

// vowelPhonemes are the phoneme characters that begin a vowel nucleus. 'a'
// only ever appears as the onset of the diphthongs "aI"/"aU".
const vowelPhonemes = "iIe&aAoO0UuV@3"

func isVowelPhoneme(c byte) bool {
	for i := 0; i < len(vowelPhonemes); i++ {
		if vowelPhonemes[i] == c {
			return true
		}
	}
	return false
}

// functionWords are unstressed in normal English prose. Leaving them without a
// primary-stress mark makes them duck in pitch, which is exactly the natural
// pattern and the main source of speech rhythm.
var functionWords = map[string]bool{
	"a": true, "an": true, "the": true,
	"of": true, "to": true, "in": true, "on": true, "at": true, "by": true,
	"for": true, "from": true, "into": true, "onto": true, "with": true,
	"as": true, "than": true, "that": true, "this": true, "these": true,
	"and": true, "or": true, "but": true, "nor": true, "so": true, "if": true,
	"is": true, "am": true, "are": true, "was": true, "were": true, "be": true,
	"been": true, "do": true, "does": true, "did": true, "has": true,
	"have": true, "had": true, "will": true, "would": true, "shall": true,
	"should": true, "can": true, "could": true, "may": true, "might": true,
	"must": true, "it": true, "its": true, "he": true, "she": true,
	"they": true, "them": true, "his": true, "her": true, "their": true,
	"we": true, "us": true, "our": true, "you": true, "your": true,
	"i": true, "my": true, "me": true, "him": true, "who": true, "which": true,
}

// stressExceptions pins the primary-stress nucleus (0-based) for common words
// the default heuristic gets wrong.
var stressExceptions = map[string]int{
	"hello": 1, "okay": 1, "guitar": 1, "hotel": 1, "police": 1,
	"july": 1, "alone": 1, "asleep": 1, "unique": 1, "event": 1,
	"machine": 1, "between": 1, "without": 1, "perhaps": 1, "until": 1,
	"however": 1, "tonight": 1, "today": 1, "tomorrow": 1, "address": 1,
}

// findNuclei returns the start index of each vowel nucleus in a word's phoneme
// stream. Adjacent vowel characters (diphthongs like "@U") count as one.
func findNuclei(phon []byte) []int {
	var nuclei []int
	for i := 0; i < len(phon); i++ {
		if isVowelPhoneme(phon[i]) && (i == 0 || !isVowelPhoneme(phon[i-1])) {
			nuclei = append(nuclei, i)
		}
	}
	return nuclei
}

// assignStress inserts a single primary-stress marker into a content word's
// phoneme stream. Function words are returned unchanged (reduced). word is the
// lowercased source spelling, used for the exception and function-word tables.
func assignStress(phon []byte, word string) []byte {
	if functionWords[word] {
		return phon
	}
	nuclei := findNuclei(phon)
	if len(nuclei) == 0 {
		return phon
	}

	target := 0
	if idx, ok := stressExceptions[word]; ok && idx < len(nuclei) {
		target = idx
	} else if len(nuclei) > 1 && phon[nuclei[0]] == '@' {
		// A leading schwa is essentially never stressed; move to the first
		// non-schwa nucleus (e.g. "about", "computer", "banana").
		target = 1
		for target < len(nuclei)-1 && phon[nuclei[target]] == '@' {
			target++
		}
	}

	pos := nuclei[target]
	out := make([]byte, 0, len(phon)+1)
	out = append(out, phon[:pos]...)
	out = append(out, primaryStress)
	out = append(out, phon[pos:]...)
	return out
}

// lowerWord returns an ASCII-lowercased copy of word for table lookups.
func lowerWord(word []byte) string {
	b := make([]byte, len(word))
	for i, c := range word {
		if c >= 'A' && c <= 'Z' {
			c = c - 'A' + 'a'
		}
		b[i] = c
	}
	return string(b)
}
