package model

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"
)

// tokenMatcher for the exact shape [NewToken] mints: a "t_" prefix followed by 32 lowercase hexadecimal characters.
// Anchored at both ends, so a token with anything around it is not a token.
var tokenMatcher = regexp.MustCompile(`^t_[0-9a-f]{32}$`)

// Token is an unguessable credential for single-use flows such as magic-link login.
// It is a secret and nothing else: all tokens have the same shape, so what a token was minted for cannot be read off it.
// See [ErrorTokenExpired], [ErrorTokenNotFound] and [ErrorTokenUsed] for the domain errors that concern tokens.
type Token string

// NewToken from 16 bytes read with [rand.Read], hex-encoded behind a "t_" prefix.
// Those 128 bits of randomness are what make the token unguessable.
func NewToken() Token {
	var secret [16]byte
	// rand.Read never returns an error, and instead aborts the program if randomness is unavailable.
	_, _ = rand.Read(secret[:])
	return Token("t_" + hex.EncodeToString(secret[:]))
}

// IsValid reports whether the token has the shape [NewToken] mints.
// It says nothing about whether the token was ever minted, only that it could have been.
func (t Token) IsValid() bool {
	return tokenMatcher.MatchString(string(t))
}

// String satisfies [fmt.Stringer].
func (t Token) String() string {
	return string(t)
}

var _ fmt.Stringer = Token("")
