package http_test

import (
	"testing"

	"maragu.dev/is"

	"maragu.dev/glue/http"
)

func TestSanitizeQuery(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "should remove control characters",
			input:    "hello\x00world\x1F",
			expected: "helloworld",
		},
		{
			name:     "should trim leading and trailing whitespace",
			input:    "  hello world  ",
			expected: "hello world",
		},
		{
			name:     "should collapse multiple spaces into single space",
			input:    "hello  world",
			expected: "hello world",
		},
		{
			name:     "should remove tabs and newlines as control characters",
			input:    "hello\t\nworld",
			expected: "helloworld",
		},
		{
			name:     "should handle empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "should handle string with only whitespace",
			input:    "   \t\n   ",
			expected: "",
		},
		{
			name:     "should preserve valid unicode characters",
			input:    "hello 世界 🌍",
			expected: "hello 世界 🌍",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actual := http.SanitizeQuery(test.input)
			is.Equal(t, test.expected, actual)
		})
	}
}
