package speech

// This file ports the NRL (Naval Research Laboratory) letter-to-sound
// front-end from rsynth/SoLoud's tts.cpp. It turns English text into a stream
// of phoneme characters, then converts those phonemes into Klatt element
// triplets (element index, duration, stress) consumed by the synthesizer.
//
// The rule tables live in rules_gen.go (generated). The matching algorithm,
// number expansion, and tokenizer are reimplemented here in Go.

// asciiTable maps a byte to the words used when spelling it out. Mirrors the
// default (non ALPHA_IN_DICT) ASCII table from tts.cpp.
var asciiTable = [128]string{
	"null", "", "", "",
	"", "", "", "",
	"", "", "", "",
	"", "", "", "",
	"", "", "", "",
	"", "", "", "",
	"", "", "", "",
	"", "", "", "",
	"space", "exclamation mark", "double quote", "hash",
	"dollar", "percent", "ampersand", "quote",
	"open parenthesis", "close parenthesis", "asterisk", "plus",
	"comma", "minus", "full stop", "slash",
	"zero", "one", "two", "three",
	"four", "five", "six", "seven",
	"eight", "nine", "colon", "semi colon",
	"less than", "equals", "greater than", "question mark",
	"at", "ay", "bee", "see",
	"dee", "e", "eff", "gee",
	"aych", "i", "jay", "kay",
	"ell", "em", "en", "ohe",
	"pee", "kju", "are", "es",
	"tee", "you", "vee", "double you",
	"eks", "why", "zed", "open bracket",
	"back slash", "close bracket", "circumflex", "underscore",
	"back quote", "ay", "bee", "see",
	"dee", "e", "eff", "gee",
	"aych", "i", "jay", "kay",
	"ell", "em", "en", "ohe",
	"pee", "kju", "are", "es",
	"tee", "you", "vee", "double you",
	"eks", "why", "zed", "open brace",
	"vertical bar", "close brace", "tilde", "delete",
}

var cardinals = [20]string{
	"zero", "one", "two", "three", "four",
	"five", "six", "seven", "eight", "nine",
	"ten", "eleven", "twelve", "thirteen", "fourteen",
	"fifteen", "sixteen", "seventeen", "eighteen", "nineteen",
}

var twenties = [8]string{
	"twenty", "thirty", "forty", "fifty",
	"sixty", "seventy", "eighty", "ninety",
}

// phonemeBuf accumulates phoneme characters produced by the front-end.
type phonemeBuf struct {
	b []byte
}

func (p *phonemeBuf) put(c byte)    { p.b = append(p.b, c) }
func (p *phonemeBuf) cat(s string)  { p.b = append(p.b, s...) }
func (p *phonemeBuf) size() int     { return len(p.b) }

// ASCII classification helpers matching C ctype semantics in the C locale.

func isAlphaB(c byte) bool { return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') }
func isUpperB(c byte) bool { return c >= 'A' && c <= 'Z' }
func isLowerB(c byte) bool { return c >= 'a' && c <= 'z' }
func isDigitB(c byte) bool { return c >= '0' && c <= '9' }

func isSpaceB(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\v' || c == '\f' || c == '\r'
}

func isPunctB(c byte) bool {
	return c >= 0x21 && c <= 0x7e && !isAlphaB(c) && !isDigitB(c)
}

func toUpperB(c byte) byte {
	if c >= 'a' && c <= 'z' {
		return c - 'a' + 'A'
	}
	return c
}

func isVowelB(c byte) bool {
	return c == 'A' || c == 'E' || c == 'I' || c == 'O' || c == 'U'
}

func isConsonantB(c byte) bool {
	return isUpperB(c) && !isVowelB(c)
}

// at returns the byte at index i, or 0 (a word boundary sentinel) when out of
// range. Using 0 reproduces the C behavior where context walks off the ends of
// the space-padded, NUL-terminated word buffer.
func at(w []byte, i int) byte {
	if i < 0 || i >= len(w) {
		return 0
	}
	return w[i]
}

// leftmatch matches pattern against the text ending at index ctx, scanning
// right to left. Mirrors leftmatch() in tts.cpp.
func leftmatch(w []byte, pattern string, ctx int) bool {
	if pattern == "" {
		return true
	}
	text := ctx
	for pi := len(pattern) - 1; pi >= 0; pi-- {
		pc := pattern[pi]
		tc := at(w, text)

		if isAlphaB(pc) || pc == '\'' || pc == ' ' {
			if pc != tc {
				return false
			}
			text--
			continue
		}

		switch pc {
		case '#': // one or more vowels
			if !isVowelB(tc) {
				return false
			}
			text--
			for isVowelB(at(w, text)) {
				text--
			}
		case ':': // zero or more consonants
			for isConsonantB(at(w, text)) {
				text--
			}
		case '^': // one consonant
			if !isConsonantB(tc) {
				return false
			}
			text--
		case '.': // a voiced consonant
			if tc != 'B' && tc != 'D' && tc != 'V' && tc != 'G' && tc != 'J' &&
				tc != 'L' && tc != 'M' && tc != 'N' && tc != 'R' && tc != 'W' && tc != 'Z' {
				return false
			}
			text--
		case '+': // a front vowel: E, I or Y
			if tc != 'E' && tc != 'I' && tc != 'Y' {
				return false
			}
			text--
		default:
			return false
		}
	}
	return true
}

// rightmatch matches pattern against the text starting at index ctx, scanning
// left to right. Mirrors rightmatch() in tts.cpp.
func rightmatch(w []byte, pattern string, ctx int) bool {
	if pattern == "" {
		return true
	}
	text := ctx
	for pi := 0; pi < len(pattern); pi++ {
		pc := pattern[pi]
		tc := at(w, text)

		if isAlphaB(pc) || pc == '\'' || pc == ' ' {
			if pc != tc {
				return false
			}
			text++
			continue
		}

		switch pc {
		case '#': // one or more vowels
			if !isVowelB(tc) {
				return false
			}
			text++
			for isVowelB(at(w, text)) {
				text++
			}
		case ':': // zero or more consonants
			for isConsonantB(at(w, text)) {
				text++
			}
		case '^': // one consonant
			if !isConsonantB(tc) {
				return false
			}
			text++
		case '.': // a voiced consonant
			if tc != 'B' && tc != 'D' && tc != 'V' && tc != 'G' && tc != 'J' &&
				tc != 'L' && tc != 'M' && tc != 'N' && tc != 'R' && tc != 'W' && tc != 'Z' {
				return false
			}
			text++
		case '+': // a front vowel: E, I or Y
			if tc != 'E' && tc != 'I' && tc != 'Y' {
				return false
			}
			text++
		case '%': // a suffix: ER, E, ES, ED, ING, ELY
			if at(w, text) == 'E' {
				text++
				if at(w, text) == 'L' {
					text++
					if at(w, text) == 'Y' {
						text++
					} else {
						text-- // don't gobble L
					}
				} else if c := at(w, text); c == 'R' || c == 'S' || c == 'D' {
					text++
				}
			} else if at(w, text) == 'I' {
				text++
				if at(w, text) == 'N' {
					text++
					if at(w, text) == 'G' {
						text++
						break
					}
				}
				return false
			} else {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// findRule searches rules for a match at word[index], emits its phonemes, and
// returns the index past the consumed text. If nothing matches it skips one
// character. Mirrors find_rule() in tts.cpp.
func findRule(out *phonemeBuf, word []byte, index int, rules []rule) int {
	for i := range rules {
		r := &rules[i]
		rem := index
		matched := true
		for k := 0; k < len(r.match); k++ {
			if r.match[k] != at(word, rem) {
				matched = false
				break
			}
			rem++
		}
		if !matched {
			continue
		}
		if !leftmatch(word, r.left, index-1) {
			continue
		}
		if !rightmatch(word, r.right, rem) {
			continue
		}
		out.cat(r.output)
		return rem
	}
	return index + 1 // no rule found: skip the character
}

// guessWord applies the rule tables across a space-padded, uppercased word.
func guessWord(out *phonemeBuf, word []byte) {
	index := 1 // skip the leading space
	for at(word, index) != 0 {
		var class int
		if c := word[index]; isUpperB(c) {
			class = int(c-'A') + 1
		}
		index = findRule(out, word, index, ruleTable[class])
	}
}

// nrl runs the NRL rules over the n bytes of s, appending phonemes to out.
func nrl(out *phonemeBuf, s []byte) {
	word := make([]byte, 0, len(s)+3)
	word = append(word, ' ')
	for _, c := range s {
		word = append(word, toUpperB(c))
	}
	word = append(word, ' ', 0)
	guessWord(out, word)
}

// spellOut speaks each byte of s using the ASCII spelling table.
func spellOut(out *phonemeBuf, s []byte) {
	for _, c := range s {
		xlateString(asciiTable[c&0x7f], out)
	}
}

// suspectWord decides whether s should be spelled out letter by letter rather
// than pronounced via the rules. Mirrors suspect_word() in tts.cpp.
func suspectWord(s []byte) bool {
	var seenLower, seenUpper, seenVowel bool
	var last byte
	for i, c := range s {
		if i != 0 && last != '-' && isUpperB(c) {
			seenUpper = true
		}
		if isLowerB(c) {
			seenLower = true
			c = toUpperB(c)
		}
		if c == 'A' || c == 'E' || c == 'I' || c == 'O' || c == 'U' || c == 'Y' {
			seenVowel = true
		}
		last = c
	}
	return !seenVowel || (seenUpper && seenLower) || !seenLower
}

// xlateWord pronounces a single word. Bracketed text ("[..]") is treated as
// literal phonemes. Mirrors xlate_word() in tts.cpp.
func xlateWord(out *phonemeBuf, word []byte) {
	if len(word) == 0 {
		out.put(' ')
		return
	}
	if word[0] != '[' {
		if suspectWord(word) {
			spellOut(out, word)
			return
		}
		nrl(out, word)
	} else {
		body := word[1:]
		if len(body) > 0 && body[len(body)-1] == ']' {
			body = body[:len(body)-1]
		}
		for _, c := range body {
			out.put(c)
		}
	}
	out.put(' ')
}

// xlateCardinal expands a cardinal number to words and translates them.
// Mirrors xlate_cardinal() in tts.cpp.
func xlateCardinal(value int, out *phonemeBuf) {
	if value < 0 {
		xlateString("minus", out)
		value = -value
		if value < 0 { // overflow
			xlateString("a lot", out)
			return
		}
	}

	if value >= 1000000000 {
		xlateCardinal(value/1000000000, out)
		xlateString("billion", out)
		value %= 1000000000
		if value == 0 {
			return
		}
		if value < 100 {
			xlateString("and", out)
		}
	}

	if value >= 1000000 {
		xlateCardinal(value/1000000, out)
		xlateString("million", out)
		value %= 1000000
		if value == 0 {
			return
		}
		if value < 100 {
			xlateString("and", out)
		}
	}

	if (value >= 1000 && value <= 1099) || value >= 2000 {
		xlateCardinal(value/1000, out)
		xlateString("thousand", out)
		value %= 1000
		if value == 0 {
			return
		}
		if value < 100 {
			xlateString("and", out)
		}
	}

	if value >= 100 {
		xlateString(cardinals[value/100], out)
		xlateString("hundred", out)
		value %= 100
		if value == 0 {
			return
		}
	}

	if value >= 20 {
		xlateString(twenties[(value-20)/10], out)
		value %= 10
		if value == 0 {
			return
		}
	}

	xlateString(cardinals[value], out)
}

// xlateString is the top-level text-to-phoneme tokenizer. Mirrors
// xlate_string() in tts.cpp.
func xlateString(s string, out *phonemeBuf) {
	text := []byte(s)
	n := len(text)
	get := func(i int) byte {
		if i < 0 || i >= n {
			return 0
		}
		return text[i]
	}

	i := 0
	for isSpaceB(get(i)) {
		i++
	}

	for get(i) != 0 {
		ch := get(i)
		wordStart := i

		if isAlphaB(ch) {
			for {
				ch = get(i)
				if isAlphaB(ch) || ((ch == '\'' || ch == '-' || ch == '.') && isAlphaB(get(i+1))) {
					i++
					continue
				}
				break
			}

			if ch == 0 || isSpaceB(ch) || isPunctB(ch) ||
				(isDigitB(ch) && !suspectWord(text[wordStart:i])) {
				xlateWord(out, text[wordStart:i])
			} else {
				for get(i) != 0 && !isSpaceB(get(i)) && !isPunctB(get(i)) {
					i++
				}
				spellOut(out, text[wordStart:i])
			}
			continue
		}

		if isDigitB(ch) || (ch == '-' && isDigitB(get(i+1))) {
			sign := 1
			if ch == '-' {
				sign = -1
				i++
			}
			value := 0
			for isDigitB(get(i)) {
				value = value*10 + int(get(i)-'0')
				i++
			}

			if get(i) == '.' && isDigitB(get(i+1)) {
				i++
				fracStart := i
				xlateCardinal(value*sign, out)
				xlateString("point", out)
				for isDigitB(get(i)) {
					i++
				}
				spellOut(out, text[fracStart:i])
			} else {
				xlateCardinal(value*sign, out)
			}
		} else if isPunctB(ch) {
			switch ch {
			case '!', '?', '.':
				i++
				out.put('.')
			case '"', ':', '-', ';', ',', '(', ')':
				i++
				out.put(' ')
			case '[':
				if e := indexByte(text, i, ']'); e >= 0 {
					i++
					for i < e {
						out.put(get(i))
						i++
					}
					i = e + 1
				} else {
					spellOut(out, text[wordStart:wordStart+1])
					i++
				}
			default:
				spellOut(out, text[wordStart:wordStart+1])
				i++
			}
		} else {
			for get(i) != 0 && !isSpaceB(get(i)) {
				i++
			}
			spellOut(out, text[wordStart:i])
		}

		for isSpaceB(get(i)) {
			i++
		}
	}
}

func indexByte(b []byte, from int, c byte) int {
	for i := from; i < len(b); i++ {
		if b[i] == c {
			return i
		}
	}
	return -1
}

// phoneToElements converts a phoneme buffer into the element triplet stream
// (element index, duration, stress) consumed by klatt.synth. Mirrors
// klatt::phone_to_elm() in klatt.cpp.
func phoneToElements(ph []byte) []byte {
	var out []byte
	stress := byte(0)
	i := 0
	n := len(ph)
	get := func(j int) byte {
		if j < 0 || j >= n {
			return 0
		}
		return ph[j]
	}

	for i < n {
		key8 := int(get(i))
		key16 := key8 + int(get(i+1))<<8
		if get(i+1) == 0 {
			key16 = -1
		}

		entry := (*phonemeEntry)(nil)
		consumed := 1
		for k := range phonemeTable {
			if phonemeTable[k].key == key16 {
				entry = &phonemeTable[k]
				consumed = 2
				break
			}
			if phonemeTable[k].key == key8 {
				entry = &phonemeTable[k]
				consumed = 1
				break
			}
		}

		if entry != nil {
			i += consumed
			for e := 0; e < entry.count; e++ {
				x := entry.elems[e]
				p := &elements[x]
				if p.Feat&FeatureVWL == 0 {
					stress = 0
				}
				dur := (int(p.DU) + int(p.UD)) / 2
				out = append(out, byte(x), byte(dur), stress)
			}
			continue
		}

		ch := get(i)
		i++
		switch ch {
		case '\'': // primary stress
			stress = 3
		case ',': // secondary stress
			stress = 2
		case '+': // tertiary stress
			stress = 1
		default:
			// hyphen and anything else: ignore
		}
	}

	return out
}
