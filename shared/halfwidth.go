package shared

import "strings"

// ToHalfwidth maps fullwidth digits (U+FF10-U+FF19) and fullwidth parens
// (U+FF08/U+FF09) to their ASCII equivalents, leaving everything else
// untouched. Real-world Japanese release filenames use fullwidth characters
// for volume numbers (e.g. "葬送のフリーレン（０５）") — Go's \d and regexp's
// Unicode character classes only match ASCII digits by default, so any code
// extracting a volume number must normalize first or it silently gets 0 for
// every volume. This exact bug was hit and fixed independently in two
// unrelated places before this function existed (see git history /
// CLAUDE.md) — one shared implementation now, not N.
func ToHalfwidth(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= '０' && r <= '９':
			return r - '０' + '0'
		case r == '（':
			return '('
		case r == '）':
			return ')'
		}
		return r
	}, s)
}
