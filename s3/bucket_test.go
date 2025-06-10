package s3_test

import (
	"io"
	"strings"
	"testing"
	"time"

	"maragu.dev/is"

	"maragu.dev/glue/s3test"
)

func TestBucket(t *testing.T) {
	t.Run("puts, gets, lists, and deletes an object", func(t *testing.T) {
		b := s3test.NewBucket(t)

		err := b.Put(t.Context(), "test", "text/plain", strings.NewReader("hello"))
		is.NotError(t, err)

		body, err := b.Get(t.Context(), "test")
		is.NotError(t, err)
		bodyBytes, err := io.ReadAll(body)
		is.NotError(t, err)
		is.Equal(t, "hello", string(bodyBytes))

		keys, err := b.List(t.Context(), "", 100)
		is.NotError(t, err)
		is.EqualSlice(t, []string{"test"}, keys)

		err = b.Delete(t.Context(), "test")
		is.NotError(t, err)

		body, err = b.Get(t.Context(), "test")
		is.NotError(t, err)
		is.True(t, body == nil)

		err = b.Delete(t.Context(), "test")
		is.NotError(t, err)
	})
}

func TestBucket_List(t *testing.T) {
	t.Run("lists all objects", func(t *testing.T) {
		b := s3test.NewBucket(t)

		err := b.Put(t.Context(), "test1", "text/plain", strings.NewReader(""))
		is.NotError(t, err)

		err = b.Put(t.Context(), "test2", "text/plain", strings.NewReader(""))
		is.NotError(t, err)

		keys, err := b.List(t.Context(), "", 100)
		is.NotError(t, err)
		is.EqualSlice(t, []string{"test1", "test2"}, keys)
	})

	t.Run("lists objects with prefix", func(t *testing.T) {
		b := s3test.NewBucket(t)

		err := b.Put(t.Context(), "test1", "text/plain", strings.NewReader(""))
		is.NotError(t, err)

		err = b.Put(t.Context(), "test2", "text/plain", strings.NewReader(""))
		is.NotError(t, err)

		keys, err := b.List(t.Context(), "test", 100)
		is.NotError(t, err)
		is.EqualSlice(t, []string{"test1", "test2"}, keys)

		keys, err = b.List(t.Context(), "test1", 100)
		is.NotError(t, err)
		is.EqualSlice(t, []string{"test1"}, keys)
	})
}

func TestBucket_GetPresignedURL(t *testing.T) {
	t.Run("returns a presigned URL", func(t *testing.T) {
		b := s3test.NewBucket(t)

		url, err := b.GetPresignedURL(t.Context(), "test", time.Hour)
		is.NotError(t, err)

		t.Log(url)
		is.True(t, strings.Contains(url, "/test?X-Amz-Algorithm"))
		is.True(t, strings.Contains(url, "Expires=3600"))
	})
}
