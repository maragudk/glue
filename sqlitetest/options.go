package sqlitetest

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	gluesql "maragu.dev/glue/sql"
)

// HelperOption for [NewHelper].
type HelperOption func(*helperConfig)

// helperConfig used with [HelperOption].
type helperConfig struct {
	fixtures      []string
	migrationFunc func(context.Context, *sql.DB) error
}

// WithFixtures adds SQL fixtures to be run after migrations.
// They are applied in the order given.
// Fixture names should not include the .sql extension.
// Fixtures are loaded from sqlite/testdata/fixtures/ directory from the project root.
func WithFixtures(fixtures ...string) HelperOption {
	return func(c *helperConfig) {
		c.fixtures = append(c.fixtures, fixtures...)
	}
}

// WithMigrationFunc sets a custom migration function to run instead of the built-in one.
func WithMigrationFunc(fn func(context.Context, *sql.DB) error) HelperOption {
	return func(c *helperConfig) {
		c.migrationFunc = fn
	}
}

func loadFixtures(t *testing.T, h *gluesql.Helper, fixtures []string) {
	t.Helper()

	// Try different relative paths for fixtures
	possiblePaths := []string{
		filepath.Join("..", "sqlite", "testdata", "fixtures"),
		filepath.Join("sqlite", "testdata", "fixtures"),
	}

	var fixturesDir string
	for _, path := range possiblePaths {
		if _, err := os.Stat(path); err == nil {
			fixturesDir = path
			break
		}
	}

	if fixturesDir == "" {
		t.Fatalf("error finding fixtures directory in any of: %v", possiblePaths)
	}

	for _, fixture := range fixtures {
		fixturePath := filepath.Join(fixturesDir, fixture+".sql")

		content, err := os.ReadFile(fixturePath)
		if err != nil {
			t.Fatalf("error reading fixture %s: %v", fixturePath, err)
		}

		if err := h.Exec(t.Context(), string(content)); err != nil {
			t.Fatalf("error executing fixture %s: %v", fixturePath, err)
		}
	}
}
