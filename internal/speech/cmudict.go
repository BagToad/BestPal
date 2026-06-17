package speech

import (
	_ "embed"
	"strings"
	"sync"
)

// cmudictRaw is the CMU Pronouncing Dictionary, the standard ~134k word English
// pronunciation lexicon. It is the comprehensive base for pronunciation: the NRL
// letter-to-sound rules are only a fallback for words it does not list (names,
// typos, slang). See cmudict.LICENSE (BSD 2-clause, Carnegie Mellon University).
//
//go:embed cmudict.dict
var cmudictRaw string

var (
	cmudictOnce sync.Once
	cmudictMap  map[string]string
)

// arpabetToPhoneme maps the CMUdict ARPABET symbol set onto the rsynth-style
// phoneme alphabet the Klatt front-end consumes (see phonemes_gen.go). Stress
// digits are handled separately in convertARPABET. AH is special-cased there:
// unstressed AH0 is the schwa '@', stressed AH is the wedge 'V'.
var arpabetToPhoneme = map[string]string{
	// Vowels.
	"AA": "A", "AE": "&", "AH": "V", "AO": "O", "AW": "aU", "AY": "aI",
	"EH": "e", "ER": "3", "EY": "eI", "IH": "I", "IY": "i", "OW": "@U",
	"OY": "oI", "UH": "U", "UW": "u",
	// Consonants.
	"B": "b", "CH": "tS", "D": "d", "DH": "D", "F": "f", "G": "g",
	"HH": "h", "JH": "dZ", "K": "k", "L": "l", "M": "m", "N": "n",
	"NG": "N", "P": "p", "R": "r", "S": "s", "SH": "S", "T": "t",
	"TH": "T", "V": "v", "W": "w", "Y": "j", "Z": "z", "ZH": "Z",
}

// convertARPABET turns a CMUdict phoneme sequence into the front-end's alphabet,
// inserting a stress marker before each stressed vowel: primary (digit 1) -> "'",
// secondary (digit 2) -> ",". These are exactly the markers phoneToElements reads.
func convertARPABET(tokens []string) (string, bool) {
	var b strings.Builder
	for _, tok := range tokens {
		if tok == "" {
			continue
		}
		stress := byte(0)
		if c := tok[len(tok)-1]; c >= '0' && c <= '9' {
			stress = c
			tok = tok[:len(tok)-1]
		}
		sym, ok := arpabetToPhoneme[tok]
		if !ok {
			return "", false
		}
		switch stress {
		case '1':
			b.WriteByte(primaryStress)
		case '2':
			b.WriteByte(',')
		}
		if tok == "AH" && stress == '0' {
			b.WriteByte('@') // unstressed wedge reduces to schwa
			continue
		}
		b.WriteString(sym)
	}
	return b.String(), true
}

// loadCMUDict parses the embedded dictionary once, keeping the first (default)
// pronunciation for each word and discarding the "word(2)" variants.
func loadCMUDict() {
	cmudictMap = make(map[string]string, 140000)
	for _, line := range strings.Split(cmudictRaw, "\n") {
		if line == "" || strings.HasPrefix(line, ";;;") {
			continue
		}
		if h := strings.IndexByte(line, '#'); h >= 0 {
			line = line[:h]
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		word := fields[0]
		if strings.IndexByte(word, '(') >= 0 {
			continue // a variant pronunciation; keep only the default
		}
		if _, exists := cmudictMap[word]; exists {
			continue
		}
		if ph, ok := convertARPABET(fields[1:]); ok {
			cmudictMap[word] = ph
		}
	}
}

// cmudictLookup returns the front-end phoneme string for word (lowercased), or
// false if the word is not in the dictionary.
func cmudictLookup(word string) (string, bool) {
	cmudictOnce.Do(loadCMUDict)
	ph, ok := cmudictMap[word]
	return ph, ok
}

// reduceFunctionWord strips stress markers from a dictionary pronunciation when
// the word is a function word, so it stays unstressed and the sentence keeps its
// English rhythm (the same reduction assignStress applies on the rules path).
func reduceFunctionWord(word, phonemes string) string {
	if !functionWords[word] {
		return phonemes
	}
	return strings.NewReplacer(string(primaryStress), "", ",", "").Replace(phonemes)
}
