package model_test

import (
	"testing"

	"maragu.dev/glue/model"
	"maragu.dev/is"
)

func TestEmailAddress_IsValid(t *testing.T) {
	t.Run("reports valid email addresses", func(t *testing.T) {
		tests := []struct {
			address string
			valid   bool
		}{
			{"me@example.com", true},
			{"@example.com", false},
			{"me@", false},
			{"@", false},
			{"", false},
			{"me@example", false},
		}

		for _, test := range tests {
			t.Run(test.address, func(t *testing.T) {
				a := model.EmailAddress(test.address)
				is.Equal(t, test.valid, a.IsValid())
			})
		}
	})
}

func TestEmailAddress_Local(t *testing.T) {
	t.Run("returns the local part", func(t *testing.T) {
		e := model.EmailAddress("me@example.com")
		is.Equal(t, "me", e.Local())
	})
}
