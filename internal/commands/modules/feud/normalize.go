package feud

import (
	"strings"
	"unicode"
)

// normalize reduces text to a matching key: lowercase letters and digits,
// single spaces, no leading article, and simple plurals made singular, so
// "The Cats!" and "cat" match.
func normalize(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
		case r == '\'' || r == '’':
			// "don't" -> "dont"
		default:
			b.WriteRune(' ')
		}
	}
	words := strings.Fields(b.String())
	if len(words) > 1 {
		switch words[0] {
		case "a", "an", "the", "my", "your":
			words = words[1:]
		}
	}
	for n, w := range words {
		words[n] = singular(w)
	}
	return strings.Join(words, " ")
}

func singular(w string) string {
	r := []rune(w)
	if len(r) <= 3 || r[len(r)-1] != 's' {
		return w
	}
	switch r[len(r)-2] {
	case 's', 'u', 'i':
		return w // glass, bus, octopus, analysis
	}
	return string(r[:len(r)-1])
}

// cleanText trims and collapses whitespace for display.
func cleanText(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// hasWord reports whether text contains a letter or digit.
func hasWord(s string) bool {
	return strings.IndexFunc(s, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }) >= 0
}
