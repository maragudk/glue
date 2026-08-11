package model_test

import (
	"testing"

	"maragu.dev/is"

	"maragu.dev/glue/model"
)

func TestNewToken(t *testing.T) {
	t.Run("mints a token which is valid", func(t *testing.T) {
		token := model.NewToken()
		is.True(t, token.IsValid(), token.String())
	})
}

func TestToken_IsValid(t *testing.T) {
	t.Run("accepts a token of the minted shape", func(t *testing.T) {
		token := model.Token("t_0123456789abcdef0123456789abcdef")
		is.True(t, token.IsValid())
	})

	t.Run("rejects malformed tokens", func(t *testing.T) {
		tests := []struct {
			name  string
			token string
		}{
			{name: "empty", token: ""},
			{name: "no prefix", token: "0123456789abcdef0123456789abcdef"},
			{name: "wrong prefix", token: "u_0123456789abcdef0123456789abcdef"},
			{name: "doubled underscore", token: "t__123456789abcdef0123456789abcdef"},
			{name: "too short", token: "t_0123456789abcdef0123456789abcde"},
			{name: "too long", token: "t_0123456789abcdef0123456789abcdef0"},
			{name: "non-hex character", token: "t_0123456789abcdef0123456789abcdeg"},
			{name: "uppercase hex", token: "t_0123456789ABCDEF0123456789abcdef"},
			{name: "character before", token: "xt_0123456789abcdef0123456789abcdef"},
			{name: "character after", token: "t_0123456789abcdef0123456789abcdefx"},
			{name: "trailing newline", token: "t_0123456789abcdef0123456789abcdef\n"},
			{name: "sentence", token: "select * from tokens"},
		}

		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				is.Equal(t, false, model.Token(test.token).IsValid())
			})
		}
	})
}

func TestToken_String(t *testing.T) {
	t.Run("returns the token as a string", func(t *testing.T) {
		token := model.Token("t_0123456789abcdef0123456789abcdef")
		is.Equal(t, "t_0123456789abcdef0123456789abcdef", token.String())
	})
}
