package http_test

import (
	"bufio"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"maragu.dev/httph"
	"maragu.dev/is"

	gluehttp "maragu.dev/glue/http"
)

func TestNoStoreIfNonce(t *testing.T) {
	t.Run("sets no-store on a nonced response where the handler set no Cache-Control", func(t *testing.T) {
		rec := serveWithNonce(t, true, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})

		is.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
	})

	t.Run("sets no-store where the handler writes a body without calling WriteHeader", func(t *testing.T) {
		rec := serveWithNonce(t, true, func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("hi"))
		})

		is.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
		is.Equal(t, "hi", rec.Body.String())
	})

	t.Run("sets no-store where the handler writes nothing at all", func(t *testing.T) {
		rec := serveWithNonce(t, true, func(w http.ResponseWriter, r *http.Request) {})

		is.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
	})

	t.Run("sets no Cache-Control where the request carries no nonce", func(t *testing.T) {
		rec := serveWithNonce(t, false, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})

		is.Equal(t, "", rec.Header().Get("Cache-Control"))
	})

	t.Run("leaves a Cache-Control the handler set before writing alone", func(t *testing.T) {
		rec := serveWithNonce(t, true, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "max-age=60")
			w.WriteHeader(http.StatusOK)
		})

		is.Equal(t, "max-age=60", rec.Header().Get("Cache-Control"))
	})

	t.Run("leaves a Cache-Control the handler set before writing a body alone", func(t *testing.T) {
		rec := serveWithNonce(t, true, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "max-age=60")
			_, _ = w.Write([]byte("hi"))
		})

		is.Equal(t, "max-age=60", rec.Header().Get("Cache-Control"))
	})

	t.Run("flushes through to the writer it was given", func(t *testing.T) {
		rec := serveWithNonce(t, true, func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("hi"))
			w.(http.Flusher).Flush()
		})

		is.True(t, rec.Flushed, "the underlying writer should have been flushed")
		is.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
	})

	// A [strings.Reader] would be copied through its own WriteTo, which never reaches ReadFrom, so the
	// source here is one that has none.
	t.Run("sets no-store where the handler copies the body from a reader", func(t *testing.T) {
		rec := serveWithNonce(t, true, func(w http.ResponseWriter, r *http.Request) {
			_, err := io.Copy(w, io.LimitReader(strings.NewReader("hi"), 2))
			is.NotError(t, err)
		})

		is.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
		is.Equal(t, "hi", rec.Body.String())
	})

	t.Run("sets no-store when the handler flushes before writing anything", func(t *testing.T) {
		rec := serveWithNonce(t, true, func(w http.ResponseWriter, r *http.Request) {
			w.(http.Flusher).Flush()
		})

		is.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
	})
}

func TestNoStoreIfNonceWriter(t *testing.T) {
	t.Run("unwraps to the writer it was given", func(t *testing.T) {
		var unwrapped http.ResponseWriter
		rec := serveWithNonce(t, true, func(w http.ResponseWriter, r *http.Request) {
			u, ok := w.(interface{ Unwrap() http.ResponseWriter })
			is.True(t, ok, "the writer should be unwrappable")
			unwrapped = u.Unwrap()
		})

		is.Equal(t, http.ResponseWriter(rec), unwrapped)
	})

	t.Run("hijacks through to the writer it was given", func(t *testing.T) {
		hijacker := &mockHijacker{ResponseWriter: httptest.NewRecorder()}

		csp := httph.ContentSecurityPolicy(func(opts *httph.ContentSecurityPolicyOptions) {
			opts.ScriptNonce = true
		})
		h := csp(gluehttp.NoStoreIfNonce(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _, err := w.(http.Hijacker).Hijack()
			is.NotError(t, err)
		})))
		h.ServeHTTP(hijacker, httptest.NewRequest(http.MethodGet, "/", nil))

		is.True(t, hijacker.hijacked)
	})

	t.Run("flushes through a writer which exposes nothing but Unwrap", func(t *testing.T) {
		rec := httptest.NewRecorder()
		serveWrapped(t, &unwrapOnlyWriter{ResponseWriter: rec}, func(w http.ResponseWriter, r *http.Request) {
			w.(http.Flusher).Flush()
		})

		is.True(t, rec.Flushed, "the flush should have reached the underlying writer")
	})

	t.Run("flushes through a writer which exposes nothing but Unwrap, via a response controller", func(t *testing.T) {
		rec := httptest.NewRecorder()
		serveWrapped(t, &unwrapOnlyWriter{ResponseWriter: rec}, func(w http.ResponseWriter, r *http.Request) {
			is.NotError(t, http.NewResponseController(w).Flush())
		})

		is.True(t, rec.Flushed, "the flush should have reached the underlying writer")
	})

	t.Run("hijacks through a writer which exposes nothing but Unwrap", func(t *testing.T) {
		hijacker := &mockHijacker{ResponseWriter: httptest.NewRecorder()}
		serveWrapped(t, &unwrapOnlyWriter{ResponseWriter: hijacker}, func(w http.ResponseWriter, r *http.Request) {
			_, _, err := w.(http.Hijacker).Hijack()
			is.NotError(t, err)
		})

		is.True(t, hijacker.hijacked, "the hijack should have reached the underlying writer")
	})

	t.Run("reads from through to the writer it was given", func(t *testing.T) {
		readerFrom := &mockReaderFrom{ResponseWriter: httptest.NewRecorder()}

		csp := httph.ContentSecurityPolicy(func(opts *httph.ContentSecurityPolicyOptions) {
			opts.ScriptNonce = true
		})
		h := csp(gluehttp.NoStoreIfNonce(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, err := io.Copy(w, io.LimitReader(strings.NewReader("hi"), 2))
			is.NotError(t, err)
		})))
		h.ServeHTTP(readerFrom, httptest.NewRequest(http.MethodGet, "/", nil))

		is.True(t, readerFrom.read, "the underlying writer should have been read from")
		is.Equal(t, "no-store", readerFrom.Header().Get("Cache-Control"))
	})

	t.Run("reports hijacking unsupported where nothing below it can hijack", func(t *testing.T) {
		serveWithNonce(t, true, func(w http.ResponseWriter, r *http.Request) {
			_, _, err := w.(http.Hijacker).Hijack()
			is.True(t, errors.Is(err, http.ErrNotSupported), "hijacking a recorder should be unsupported")
		})
	})

	t.Run("reports flushing unsupported where nothing below it can flush", func(t *testing.T) {
		serveWrapped(t, &unwrapOnlyWriter{ResponseWriter: &plainWriter{}}, func(w http.ResponseWriter, r *http.Request) {
			err := http.NewResponseController(w).Flush()
			is.True(t, errors.Is(err, http.ErrNotSupported), "flushing a writer with no flusher should be unsupported")
		})
	})
}

// plainWriter is an [http.ResponseWriter] and nothing more, so that a chain ending in it can serve
// neither a flush nor a hijack.
type plainWriter struct {
	header http.Header
}

func (p *plainWriter) Header() http.Header {
	if p.header == nil {
		p.header = http.Header{}
	}
	return p.header
}

func (p *plainWriter) Write(b []byte) (int, error) {
	return len(b), nil
}

func (p *plainWriter) WriteHeader(code int) {}

// unwrapOnlyWriter passes on nothing but Unwrap, the way a middleware's writer does when it leaves the
// optional interfaces to [http.ResponseController] instead of declaring them itself.
type unwrapOnlyWriter struct {
	http.ResponseWriter
}

func (u *unwrapOnlyWriter) Unwrap() http.ResponseWriter {
	return u.ResponseWriter
}

type mockReaderFrom struct {
	http.ResponseWriter
	read bool
}

func (m *mockReaderFrom) ReadFrom(r io.Reader) (int64, error) {
	m.read = true
	return io.Copy(m.ResponseWriter, r)
}

type mockHijacker struct {
	http.ResponseWriter
	hijacked bool
}

func (m *mockHijacker) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	m.hijacked = true
	return nil, nil, nil
}

// serveWrapped runs the given handler behind [gluehttp.NoStoreIfNonce] on a nonced request, writing to
// the given writer rather than to a recorder of its own.
func serveWrapped(t *testing.T, w http.ResponseWriter, h http.HandlerFunc) {
	t.Helper()

	csp := httph.ContentSecurityPolicy(func(opts *httph.ContentSecurityPolicyOptions) {
		opts.ScriptNonce = true
	})
	csp(gluehttp.NoStoreIfNonce(h)).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
}

// serveWithNonce runs the given handler behind [gluehttp.NoStoreIfNonce], with the CSP middleware above
// it generating a nonce or not. That middleware is the only way a nonce gets into a request context, so
// the two are exercised together here.
func serveWithNonce(t *testing.T, nonce bool, h http.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()

	csp := httph.ContentSecurityPolicy(func(opts *httph.ContentSecurityPolicyOptions) {
		opts.ScriptNonce = nonce
	})

	rec := httptest.NewRecorder()
	csp(gluehttp.NoStoreIfNonce(h)).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	return rec
}
