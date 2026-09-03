package http

import (
	"bufio"
	"io"
	"net"
	"net/http"

	"maragu.dev/httph"
)

type Middleware = func(next http.Handler) http.Handler

// ContextKey is a custom type to be used for storing keys in a [context.Context].
type ContextKey string

// NoStoreIfNonce is [Middleware] which marks a response whose request carries a CSP nonce as
// uncacheable, with Cache-Control: no-store. A cache may refresh a stored response's headers from a 304
// and so pair an old body with a new nonce, at which point every nonced element in that body fails the
// policy. A response which already has a Cache-Control when it starts is left as it is, on the reading
// that whatever set it knows better what the response is worth caching.
//
// Placement is load-bearing at both ends. Whether the request carries a nonce is read when this runs,
// so it belongs below whatever puts the nonce in the request context; a nonce added further down goes
// into a context this never sees, and the response then goes out cacheable. It also belongs below
// anything that sets a Cache-Control of its own through the writer it was handed, since from above,
// such a header is indistinguishable from the handler's own.
func NoStoreIfNonce(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if httph.NonceFromContext(r.Context()) == "" {
			next.ServeHTTP(w, r)
			return
		}

		nw := &noStoreWriter{ResponseWriter: w}
		next.ServeHTTP(nw, r)

		// A handler which returns without writing anything leaves the header map open, and the server
		// writes an empty 200 from it after this returns, so the header still reaches the client. Once
		// the handler has written, this does nothing.
		nw.setNoStore()
	})
}

// noStoreWriter sets Cache-Control: no-store on the response as it starts, unless it already has one.
// The header map is still open for writing at that point, and nothing written to it later would reach
// the client anyway.
//
// Flush, Hijack and ReadFrom are declared regardless of what the wrapped writer can do, so a handler
// which asserts one of them keeps working, at the price of the assertion no longer answering whether the
// writer underneath supports it. Flush and Hijack go down through an [http.ResponseController], which
// follows the chain of Unwrap methods rather than stopping at the writer immediately below, since a
// writer there may well declare neither and leave both to that controller. They report
// [http.ErrNotSupported] where nothing in the chain can serve them. ReadFrom falls back to a plain copy,
// and [http.Pusher] is not carried across at all.
type noStoreWriter struct {
	http.ResponseWriter
	started bool
}

func (w *noStoreWriter) WriteHeader(code int) {
	w.setNoStore()
	w.ResponseWriter.WriteHeader(code)
}

func (w *noStoreWriter) Write(b []byte) (int, error) {
	w.setNoStore()
	return w.ResponseWriter.Write(b)
}

// FlushError is the form [http.ResponseController] prefers, so that a caller going through one gets the
// reason a flush did not happen instead of silence.
func (w *noStoreWriter) FlushError() error {
	w.setNoStore()

	return http.NewResponseController(w.ResponseWriter).Flush()
}

func (w *noStoreWriter) Flush() {
	_ = w.FlushError()
}

func (w *noStoreWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return http.NewResponseController(w.ResponseWriter).Hijack()
}

func (w *noStoreWriter) ReadFrom(r io.Reader) (int64, error) {
	w.setNoStore()

	if rf, ok := w.ResponseWriter.(io.ReaderFrom); ok {
		return rf.ReadFrom(r)
	}
	return io.Copy(w.ResponseWriter, r)
}

func (w *noStoreWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// setNoStore once, on the first thing that starts the response. Anything after that is too late to
// reach the client, and a header set in the meantime is somebody else's decision to keep.
func (w *noStoreWriter) setNoStore() {
	if w.started {
		return
	}
	w.started = true

	if w.Header().Get("Cache-Control") == "" {
		w.Header().Set("Cache-Control", "no-store")
	}
}
