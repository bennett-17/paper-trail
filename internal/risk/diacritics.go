package risk

import "strings"

// diacriticFold maps common Latin-alphabet accented characters
// (lowercase only -- foldDiacritics always runs after strings.ToLower)
// to their unaccented ASCII equivalent, so e.g. "jose" and "josé" (or
// "muller"/"müller") compare equal for both the exact (normalizeText)
// and fuzzy (tokenizeName) matchers. Not full Unicode normalization --
// that needs golang.org/x/text/unicode/norm, a dependency this
// stdlib-only project doesn't take -- this covers the common Western
// European diacritics likely to appear in UK/US/AU corporate and
// charity records, the same "reasonable common set, not exhaustive"
// spirit as nameTitleWords in fuzzyname.go.
var diacriticFold = map[rune]string{
	'à': "a", 'á': "a", 'â': "a", 'ã': "a", 'ä': "a", 'å': "a", 'ā': "a", 'ă': "a", 'ą': "a",
	'è': "e", 'é': "e", 'ê': "e", 'ë': "e", 'ē': "e", 'ĕ': "e", 'ė': "e", 'ę': "e", 'ě': "e",
	'ì': "i", 'í': "i", 'î': "i", 'ï': "i", 'ī': "i", 'ĭ': "i", 'į': "i", 'ı': "i",
	'ò': "o", 'ó': "o", 'ô': "o", 'õ': "o", 'ö': "o", 'ø': "o", 'ō': "o", 'ŏ': "o", 'ő': "o",
	'ù': "u", 'ú': "u", 'û': "u", 'ü': "u", 'ū': "u", 'ŭ': "u", 'ů': "u", 'ű': "u", 'ų': "u",
	'ý': "y", 'ÿ': "y",
	'ñ': "n", 'ń': "n", 'ņ': "n", 'ň': "n",
	'ç': "c", 'ć': "c", 'ĉ': "c", 'ċ': "c", 'č': "c",
	'ś': "s", 'ŝ': "s", 'ş': "s", 'š': "s",
	'ź': "z", 'ż': "z", 'ž': "z",
	'ł': "l",
	'đ': "d", 'ď': "d",
	'ğ': "g", 'ĝ': "g", 'ġ': "g", 'ģ': "g",
	'ř': "r",
	'ť': "t", 'ţ': "t",
	'ß': "ss",
	'æ': "ae",
	'œ': "oe",
}

// foldDiacritics replaces each accented character in s with its
// unaccented equivalent (see diacriticFold), leaving any character not
// in the table untouched. s is expected to already be lowercased.
func foldDiacritics(s string) string {
	var b strings.Builder
	changed := false
	for _, r := range s {
		if repl, ok := diacriticFold[r]; ok {
			b.WriteString(repl)
			changed = true
			continue
		}
		b.WriteRune(r)
	}
	if !changed {
		return s
	}
	return b.String()
}
