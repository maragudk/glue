package sql_test

import (
	"testing"

	"maragu.dev/is"

	"maragu.dev/glue/sql"
	internaltesting "maragu.dev/glue/sql/internal/testing"
)

func TestHelper_Connect(t *testing.T) {
	internaltesting.Run(t, "has a jobs queue", func(t *testing.T, h *sql.Helper) {
		is.NotNil(t, h.JobsQ)
	})
}
