package s3test_test

import (
	"testing"

	"maragu.dev/glue/s3test"
)

func TestNewBucket(t *testing.T) {
	t.Run("can create a new bucket", func(t *testing.T) {
		s3test.NewBucket(t)
	})
}
