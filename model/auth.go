package model

import (
	"fmt"
	"strings"
	"unicode"
)

// Role for a user. A user may have 0 to many of these.
type Role string

// String satisfies [fmt.Stringer].
func (r Role) String() string {
	return string(r)
}

// Pretty formats the role for display.
func (r Role) Pretty() string {
	role := strings.TrimSpace(string(r))
	if role == "" {
		return ""
	}

	role = strings.ReplaceAll(role, "_", " ")
	role = strings.ReplaceAll(role, "-", " ")

	words := strings.Fields(role)
	for i, word := range words {
		lower := strings.ToLower(word)
		runes := []rune(lower)
		runes[0] = unicode.ToTitle(runes[0])
		words[i] = string(runes)
	}

	return strings.Join(words, " ")
}

var _ fmt.Stringer = Role("")

// Permission for a user used during authorization.
type Permission string

// String satisfies [fmt.Stringer].
func (p Permission) String() string {
	return string(p)
}

var _ fmt.Stringer = Permission("")
