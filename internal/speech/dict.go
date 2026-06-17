package speech

// pronunciationDict is the top-priority pronunciation layer. It overrides both
// CMUdict (cmudict.go) and the NRL letter-to-sound rules (nrl.go).
//
// CMUdict already covers ~126k common English words, so this map is deliberately
// small: it holds project/brand names and gaming/Discord slang that CMUdict does
// not contain and the rules would otherwise mangle. Add an entry here whenever a
// word needs a deliberate, synth-specific pronunciation.
//
// Values are pre-stressed phoneme strings in the same notation the synth front
// end emits (see phonemeTable in phonemes_gen.go). Stress marks: ' primary,
// , secondary. Look them up lowercased.
var pronunciationDict = map[string]string{
	// Project / brand names (not in CMUdict).
	"bagtoad":  "b'&gt,@Ud",
	"bestpal":  "b'estp,&l",
	"gamerpal": "g'eIm3p,&l",

	// Gaming / Discord slang CMUdict lacks.
	"emoji":      "Im'@UdZi",
	"emojis":     "Im'@UdZiz",
	"emote":      "Im'@Ut",
	"emotes":     "Im'@Uts",
	"noob":       "n'ub",
	"noobs":      "n'ubz",
	"esports":    "'isp,Orts",
	"livestream": "l'aIvstr,im",
	"poggers":    "p'0g3z",
}
