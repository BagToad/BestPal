package speech

// pronunciationDict overrides the NRL letter-to-sound rules for words the rules
// mispronounce. English spelling is too irregular for any rule set to get every
// word right, so a small exceptions dictionary is the standard fix.
//
// Keys are lowercase spellings. Values are phoneme strings in the same alphabet
// the rules emit (see phonemes_gen.go), with a leading apostrophe (') placed
// immediately before the primary-stress vowel. Content words MUST carry exactly
// one stress mark; function words are left unmarked so they stay reduced.
//
// Phoneme cheat sheet: i=ee(beat) I=ih(bit) e=eh(bet) &=ae(cat) V=uh(but)
// A=ah(car) O=aw(thought) 0=o(lot) U=uu(book) u=oo(boot) @=schwa 3=er(bird).
// Diphthongs: eI(day) aI(eye) oI(boy) aU(cow) @U(go) I@(ear) e@(air) U@(cure).
// Consonants: T=th(thin) D=th(this) S=sh Z=zh N=ng tS=ch dZ=j j=y.
var pronunciationDict = map[string]string{
	// Words the rules get clearly wrong (verified against synthesis output).
	"everyone":   "'evriwVn",
	"everybody":  "'evribodi",
	"everything": "'evriTIN",
	"everywhere": "'evriwe@",
	"offline":    "'OflaIn",
	"online":     "'0nlaIn",
	"rhythm":     "'rID@m",
	"completely": "k@mpl'itli",
	"complete":   "k@mpl'it",
	"entirely":   "Int'aIrli",
	"entire":     "Int'aI3",
	"external":   "Ikst'3n@l",
	"internal":   "Int'3n@l",
	"intonation": "Int@n'eIS@n",
	"before":     "bIf'Or",
	"evening":    "'ivnIN",
	"system":     "'sIst@m",
	"written":    "'rIt@n",
	"today":      "t@d'eI",
	"tonight":    "t@n'aIt",
	"tomorrow":   "t@m'0r@U",
	"better":     "'bet3",

	// Common irregular words the rules routinely break.
	"one":      "w'Vn",
	"once":     "w'Vns",
	"who":      "h'u",
	"whom":     "h'um",
	"whose":    "h'uz",
	"water":    "w'Ot3",
	"woman":    "w'Um@n",
	"women":    "w'ImIn",
	"said":     "s'ed",
	"says":     "s'ez",
	"again":    "@g'en",
	"against":  "@g'enst",
	"busy":     "b'Izi",
	"business": "b'IznIs",
	"build":    "b'Ild",
	"built":    "b'Ilt",
	"people":   "p'ip@l",
	"because":  "bIk'Oz",
	"friend":   "fr'end",
	"friends":  "fr'endz",
	"great":    "gr'eIt",
	"heart":    "h'Art",
	"earth":    "'3T",
	"work":     "w'3k",
	"works":    "w'3ks",
	"enough":   "In'Vf",
	"through":  "Tr'u",
	"though":   "D'@U",
	"thought":  "T'Ot",
	"laugh":    "l'&f",
	"listen":   "l'Is@n",
	"often":    "'0f@n",
	"know":     "n'@U",
	"knew":     "n'u",
	"love":     "l'Vv",
	"come":     "k'Vm",
	"comes":    "k'Vmz",
	"done":     "d'Vn",
	"none":     "n'Vn",
	"move":     "m'uv",
	"prove":    "pr'uv",
	"give":     "g'Iv",
	"given":    "g'Iv@n",
	"many":     "m'eni",
	"any":      "'eni",
	"anything": "'eniTIN",
	"money":    "m'Vni",
	"here":     "h'I@",
	"hear":     "h'I@",
	"year":     "j'I@",
	"years":    "j'I@z",
	"sure":     "S'U@",
	"welcome":  "w'elk@m",

	// Function words: fix the vowels but leave them reduced (no stress mark).
	"are":    "Ar",
	"were":   "w3",
	"could":  "kUd",
	"would":  "wUd",
	"should": "SUd",
	"there":  "De@",
	"where":  "we@",
}
