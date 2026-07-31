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
			name:     "should collapse a run of dollar placeholders without spaces",
			query:    "select * from festivals where id in ($1,$2,$3)",
			expected: "select * from festivals where id in ($1)",
		},
		{
			name:     "should collapse a large placeholder run instead of truncating it",
			query:    "select * from festivals where id in (" + strings.Repeat("?, ", 999) + "?)",
			expected: "select * from festivals where id in (?)",
		},
		{
			name:     "should not collapse placeholders inside a string literal",
			query:    "select * from signs where text = '?, ?, ?'",
			expected: "select * from signs where text = '?, ?, ?'",
		},
		{
			name:     "should not collapse dollar amounts inside a string literal",
			query:    "select * from merch where deal = 'buy $1, $2 off'",
			expected: "select * from merch where deal = 'buy $1, $2 off'",
		},
		{
			name:     "should not collapse inside a string literal with an escaped quote",
			query:    "select * from merch where slogan = 'glitter''s great: ?, ?'",
			expected: "select * from merch where slogan = 'glitter''s great: ?, ?'",
		},
		{
			name:     "should collapse a run following a string literal",
			query:    "select * from festivals where name = 'Roskilde' and id in (?, ?, ?)",
			expected: "select * from festivals where name = 'Roskilde' and id in (?)",
		},
		{
			name:     "should not collapse a list of string literals",
			query:    "select * from festivals where name in ('Roskilde', 'Glastonbury', 'Fusion')",
			expected: "select * from festivals where name in ('Roskilde', 'Glastonbury', 'Fusion')",
		},
		{
			name:     "should not treat jsonb question mark operators as placeholders",
			query:    "select * from festivals where meta ? $1 and tags ?| array[$2, $3]",
			expected: "select * from festivals where meta ? $1 and tags ?| array[$2]",
		},
		{
			name:     "should not collapse placeholders separated by identifiers",
			query:    "select ?, year, ? from festivals",
			expected: "select ?, year, ? from festivals",
		},
		{
			name:     "should not collapse placeholders with casts between them",
			query:    "select * from festivals where id in ($1::int, $2::int)",
			expected: "select * from festivals where id in ($1::int, $2::int)",
		},
		{
			name:     "should stop collapsing a run at a non-placeholder argument",
			query:    "select coalesce(?, ?, 'none') from festivals",
			expected: "select coalesce(?, 'none') from festivals",
		},
		{
			name:     "should truncate long queries",
			query:    "select '" + strings.Repeat("a", 1000) + "'",
			expected: ("select '" + strings.Repeat("a", 1000))[:1000] + "…",
		},
		{
			name:     "should truncate long queries at a rune boundary",
			query:    strings.Repeat("a", 998) + "🎉",
			expected: strings.Repeat("a", 998) + "…",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			is.Equal(t, test.expected, normalizeQuery(test.query))
		})
	}
}
