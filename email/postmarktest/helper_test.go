package postmarktest_test

import (
	"testing"

	"maragu.dev/glue/email/postmarktest"
)

func TestNewSender(t *testing.T) {
	t.Run("can create a new sender", func(t *testing.T) {
		postmarktest.NewSender(t)
	})
}
