package shared

import (
	"regexp"
	"strconv"
)

var (
	reLastInteger = regexp.MustCompile(`\d+`)
	reLastNumber  = regexp.MustCompile(`\d+(?:\.\d+)?`)
)

// LastInteger returns the last run of ASCII digits in s, after fullwidth
// normalization (ToHalfwidth) — used to sort/match volume names by their
// trailing number ("Frieren v14" -> 14). A decimal point does not glue
// digits together: "v1.5" yields 5, matching this project's existing
// volume-sort convention (see the callers this replaces).
func LastInteger(s string) (int, bool) {
	matches := reLastInteger.FindAllString(ToHalfwidth(s), -1)
	if len(matches) == 0 {
		return 0, false
	}
	n, err := strconv.Atoi(matches[len(matches)-1])
	if err != nil {
		return 0, false
	}
	return n, true
}

// LastNumber returns the last number in s, allowing one optional embedded
// decimal point ("v1.5" -> 1.5), after fullwidth normalization — used when
// parsing a book's series index directly from its filename.
func LastNumber(s string) (float64, bool) {
	matches := reLastNumber.FindAllString(ToHalfwidth(s), -1)
	if len(matches) == 0 {
		return 0, false
	}
	f, err := strconv.ParseFloat(matches[len(matches)-1], 64)
	if err != nil {
		return 0, false
	}
	return f, true
}
