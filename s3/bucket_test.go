package s3_test

import (
	"context"
	"io"
	"strings"
	"testing"

	"maragu.dev/is"

	"maragu.dev/glue/s3test"
)

func TestBucket(t *testing.T) {
	s3test.SkipIfShort(t)

	t.Run("puts, gets, and deletes an object", func(t *testing.T) {
		b := s3test.CreateBucket(t)

		err := b.Put(context.Background(), "test", "text/plain", strings.NewReader("hello"))
		is.NotError(t, err)

		body, err := b.Get(context.Background(), "test")
		is.NotError(t, err)
		bodyBytes, err := io.ReadAll(body)
		is.NotError(t, err)
		is.Equal(t, "hello", string(bodyBytes))

		err = b.Delete(context.Background(), "test")
		is.NotError(t, err)

		body, err = b.Get(context.Background(), "test")
		is.NotError(t, err)
		is.True(t, body == nil)

		err = b.Delete(context.Background(), "test")
		is.NotError(t, err)
	})
}
