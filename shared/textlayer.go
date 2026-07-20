package shared

// Thresholds for deciding whether a PDF or an uploaded EPUB already has a
// real, usable Japanese text layer (skip OCR) versus scanned page images or
// text-layer noise from a non-Japanese-aware OCR pass (needs OCR).
// converter/pdf.go and internal/api/converter.go both apply these to their
// own extracted-text streams; keeping the numbers in one place is the whole
// point — matching values that live in two places by convention is exactly
// what silently drifts.
const (
	// MinCharsPerPage is the average non-whitespace character count per page
	// above which a document counts as having a text layer at all.
	MinCharsPerPage = 20

	// MinJapaneseFraction guards against a present-but-garbage text layer:
	// scans run through a non-Japanese-aware OCR pass carry dense Latin
	// noise that passes a raw character-count check but isn't the book's
	// actual content. Real Japanese text lands around 0.9+; garbage OCR
	// lands at 0.0.
	MinJapaneseFraction = 0.3
)
