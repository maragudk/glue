package sql_test

import (
	"testing"

	"maragu.dev/is"

	internaltesting "maragu.dev/glue/internal/testing"
	"maragu.dev/glue/sql"
)

func TestHelper_Connect(t *testing.T) {
	internaltesting.Run(t, "has a jobs queue", func(t *testing.T, h *sql.Helper) {
		is.NotNil(t, h.JobsQ)
	})
}
