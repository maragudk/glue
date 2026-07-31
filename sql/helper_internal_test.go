package sql

import (
	"strings"
	"testing"

	"maragu.dev/is"
)

func TestNormalizeQuery(t *testing.T) {
	tests := []struct {
		name     string
		query    string
		expected string
	}{
		{
			name:     "should collapse excessive whitespace",
			query:    "select *\n\t\tfrom  glitter",
			expected: "select * from glitter",
		},
		{
			name:     "should keep a single question mark placeholder",
			query:    "select * from festivals where id = ?",
			expected: "select * from festivals where id = ?",
		},
		{
			name:     "should collapse a run of question mark placeholders",
			query:    "select * from festivals where id in (?, ?, ?)",
			expected: "select * from festivals where id in (?)",
		},
		{
			name:     "should collapse a run of question mark placeholders without spaces",
			query:    "select * from festivals where id in (?,?,?)",
			expected: "select * from festivals where id in (?)",
		},
		{
			name:     "should collapse a run of dollar placeholders into the first one",
			query:    "select * from festivals where year = $1 and id in ($2, $3, $4)",
			expected: "select * from festivals where year = $1 and id in ($2)",
		},
		{
			name:     "should collapse multiple separate placeholder runs",
			query:    "insert into lineups (day, artist) values (?, ?), (?, ?)",
			expected: "insert into lineups (day, artist) values (?), (?)",
		},
		{
			name:     "should collapse a large placeholder run instead of truncating it",
			query:    "select * from festivals where id in (" + strings.Repeat("?, ", 999) + "?)",
			expected: "select * from festivals where id in (?)",
		},
		{
			name:     "should truncate long queries",
			query:    "select '" + strings.Repeat("a", 1000) + "'",
			expected: ("select '" + strings.Repeat("a", 1000))[:1000] + "…",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			is.Equal(t, test.expected, normalizeQuery(test.query))
		})
	}
}
