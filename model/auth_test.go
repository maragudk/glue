package model_test

import (
	"testing"

	"maragu.dev/is"

	"maragu.dev/glue/model"
)

func TestRole_Pretty(t *testing.T) {
	t.Run("formats roles for display", func(t *testing.T) {
		tests := []struct {
			name     string
			role     model.Role
			expected string
		}{
			{name: "admin", role: model.Role("admin"), expected: "Admin"},
			{name: "support agent", role: model.Role("support_agent"), expected: "Support Agent"},
			{name: "project lead", role: model.Role("project-lead"), expected: "Project Lead"},
			{name: "empty", role: model.Role(""), expected: ""},
			{name: "already formatted", role: model.Role("Support"), expected: "Support"},
		}

		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				is.Equal(t, test.expected, test.role.Pretty())
			})
		}
	})
}
