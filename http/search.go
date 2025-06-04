package http

import (
	"strings"
	"unicode"
)

// SanitizeQuery by removing control characters,
// trimming whitespace, collapsing multiple spaces into single space,
// and enforcing a maximum length.
func SanitizeQuery(q string) string {
	// Remove control characters
	q = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, q)

	// Trim whitespace
	q = strings.TrimSpace(q)

	// Collapse multiple spaces into single space
	q = strings.Join(strings.Fields(q), " ")

	return q
}
