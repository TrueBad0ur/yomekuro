// Package shared holds logic genuinely duplicated between the yomekuro
// server module and the converter module, which otherwise share no code —
// see CLAUDE.md's "cross-module duplication" notes. Kept intentionally small:
// only functions that were literal copies (and had already drifted at least
// once) live here, not everything that looks superficially similar.
package shared

// IsJapanese reports whether r is hiragana, katakana, kanji, or CJK/fullwidth
// punctuation — used to distinguish a real Japanese text layer (PDF or EPUB)
// from a non-Japanese-aware OCR pass's dense Latin-character noise.
func IsJapanese(r rune) bool {
	switch {
	case r >= 0x3040 && r <= 0x30FF: // Hiragana + Katakana
		return true
	case r >= 0x4E00 && r <= 0x9FFF: // CJK Unified Ideographs
		return true
	case r >= 0x3400 && r <= 0x4DBF: // CJK Extension A
		return true
	case r >= 0x3000 && r <= 0x303F: // CJK punctuation
		return true
	case r >= 0xFF00 && r <= 0xFFEF: // Halfwidth/fullwidth forms
		return true
	default:
		return false
	}
}
