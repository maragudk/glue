package http

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/chi/v5"
	. "maragu.dev/gomponents"
	. "maragu.dev/gomponents/http"

	"maragu.dev/glue/html"
)

// statusClientClosedRequest is the non-standard 499 status code popularized by nginx ("Client Closed
// Request"). It is not in the IANA registry, but it is a widely recognized convention for "the client
// disconnected before we responded".
const statusClientClosedRequest = 499

type Router struct {
	Mux chi.Router
	SM  *scs.SessionManager
}

type NewRouterOpts struct {
	Mux chi.Router
	SM  *scs.SessionManager
}

func NewRouter(opts NewRouterOpts) *Router {
	if opts.Mux == nil {
		opts.Mux = chi.NewMux()
	}
	return &Router{
		Mux: opts.Mux,
		SM:  opts.SM,
	}
}

func (r *Router) Get(path string, cb func(props html.PageProps) (Node, error)) {
	r.Mux.Get(path, adaptPage(cb))
}

func (r *Router) Post(path string, cb func(props html.PageProps) (Node, error)) {
	r.Mux.Post(path, adaptPage(cb))
}

func (r *Router) Put(path string, cb func(props html.PageProps) (Node, error)) {
	r.Mux.Put(path, adaptPage(cb))
}

func (r *Router) Delete(path string, cb func(props html.PageProps) (Node, error)) {
	r.Mux.Delete(path, adaptPage(cb))
}

// adaptPage turns a page callback into a [http.HandlerFunc]. If the callback returns an error rooted in
// [context.Canceled], the client disconnected before we responded, so we respond with 499 (Client Closed
// Request) instead of 500. A vanished client is not a server error, so this keeps these out of the 5xx
// error rate. A genuine error that merely coincides with a disconnect is not [context.Canceled], so it
// still surfaces as a 500.
func adaptPage(cb func(props html.PageProps) (Node, error)) http.HandlerFunc {
	return Adapt(func(w http.ResponseWriter, r *http.Request) (Node, error) {
		n, err := cb(GetProps(w, r))
		if err != nil && errors.Is(err, context.Canceled) {
			return n, Error{Code: statusClientClosedRequest, Err: err}
		}
		return n, err
	})
}

func (r *Router) Group(cb func(r *Router)) {
	r.Mux.Group(func(mux chi.Router) {
		cb(&Router{Mux: mux, SM: r.SM})
	})
}

func (r *Router) Route(pattern string, cb func(r *Router)) {
	r.Mux.Route(pattern, func(mux chi.Router) {
		cb(&Router{Mux: mux, SM: r.SM})
	})
}

func (r *Router) Use(middlewares ...Middleware) {
	r.Mux.Use(middlewares...)
}

func (r *Router) NotFound(h http.HandlerFunc) {
	r.Mux.NotFound(h)
}

func GetProps(w http.ResponseWriter, r *http.Request) html.PageProps {
	return html.PageProps{
		Ctx:         r.Context(),
		R:           r,
		UserID:      GetUserIDFromContext(r.Context()),
		W:           w,
		Permissions: GetPermissionsFromContext(r.Context()),
	}
}

func GetPathParam(r *http.Request, name string) string {
	return chi.URLParam(r, name)
}

// TracingMux is a decorator that satisfies [chi.Router] but times the handler registered for each
// HTTP method. Registration methods which take a handler wrap it; [TracingMux.Mount] and
// [TracingMux.Use] pass theirs through untouched, and [TracingMux.Group], [TracingMux.Route] and
// [TracingMux.With] hand out a decorator over the sub-router, so a handler is wrapped exactly once.
type TracingMux struct {
	mux chi.Router
}

var _ chi.Router = (*TracingMux)(nil)

func (t *TracingMux) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	t.mux.ServeHTTP(w, r)
}

func (t *TracingMux) Routes() []chi.Route {
	return t.mux.Routes()
}

func (t *TracingMux) Middlewares() chi.Middlewares {
	return t.mux.Middlewares()
}

func (t *TracingMux) Match(rctx *chi.Context, method, path string) bool {
	return t.mux.Match(rctx, method, path)
}

func (t *TracingMux) Find(rctx *chi.Context, method, path string) string {
	return t.mux.Find(rctx, method, path)
}

func (t *TracingMux) Use(middlewares ...func(http.Handler) http.Handler) {
	t.mux.Use(middlewares...)
}

func (t *TracingMux) With(middlewares ...func(http.Handler) http.Handler) chi.Router {
	return &TracingMux{mux: t.mux.With(middlewares...)}
}

func (t *TracingMux) Group(fn func(r chi.Router)) chi.Router {
	return &TracingMux{
		mux: t.mux.Group(func(r chi.Router) {
			fn(&TracingMux{mux: r})
		}),
	}
}

func (t *TracingMux) Route(pattern string, fn func(r chi.Router)) chi.Router {
	return &TracingMux{
		mux: t.mux.Route(pattern, func(r chi.Router) {
			fn(&TracingMux{mux: r})
		}),
	}
}

func (t *TracingMux) Mount(pattern string, h http.Handler) {
	t.mux.Mount(pattern, h)
}

func (t *TracingMux) Handle(pattern string, h http.Handler) {
	t.mux.Handle(pattern, t.wrapHandler(h))
}

func (t *TracingMux) HandleFunc(pattern string, h http.HandlerFunc) {
	t.mux.HandleFunc(pattern, t.wrapHandlerFunc(h))
}

func (t *TracingMux) Method(method, pattern string, h http.Handler) {
	t.mux.Method(method, pattern, t.wrapHandler(h))
}

func (t *TracingMux) MethodFunc(method, pattern string, h http.HandlerFunc) {
	t.mux.MethodFunc(method, pattern, t.wrapHandlerFunc(h))
}

func (t *TracingMux) Connect(pattern string, h http.HandlerFunc) {
	t.mux.Connect(pattern, t.wrapHandlerFunc(h))
}

func (t *TracingMux) Delete(pattern string, h http.HandlerFunc) {
	t.mux.Delete(pattern, t.wrapHandlerFunc(h))
}

func (t *TracingMux) Get(pattern string, h http.HandlerFunc) {
	t.mux.Get(pattern, t.wrapHandlerFunc(h))
}

func (t *TracingMux) Head(pattern string, h http.HandlerFunc) {
	t.mux.Head(pattern, t.wrapHandlerFunc(h))
}

func (t *TracingMux) Options(pattern string, h http.HandlerFunc) {
	t.mux.Options(pattern, t.wrapHandlerFunc(h))
}

func (t *TracingMux) Patch(pattern string, h http.HandlerFunc) {
	t.mux.Patch(pattern, t.wrapHandlerFunc(h))
}

func (t *TracingMux) Post(pattern string, h http.HandlerFunc) {
	t.mux.Post(pattern, t.wrapHandlerFunc(h))
}

func (t *TracingMux) Put(pattern string, h http.HandlerFunc) {
	t.mux.Put(pattern, t.wrapHandlerFunc(h))
}

func (t *TracingMux) Query(pattern string, h http.HandlerFunc) {
	t.mux.Query(pattern, t.wrapHandlerFunc(h))
}

func (t *TracingMux) Trace(pattern string, h http.HandlerFunc) {
	t.mux.Trace(pattern, t.wrapHandlerFunc(h))
}

func (t *TracingMux) NotFound(h http.HandlerFunc) {
	t.mux.NotFound(t.wrapHandlerFunc(h))
}

func (t *TracingMux) MethodNotAllowed(h http.HandlerFunc) {
	t.mux.MethodNotAllowed(t.wrapHandlerFunc(h))
}

func (t *TracingMux) wrapHandler(h http.Handler) http.Handler {
	return http.Handler(t.wrapHandlerFunc(h.ServeHTTP))
}

// wrapHandlerFunc to time the handler and record it as handler.duration_ms on the current span, which
// under [OpenTelemetry] is the main span. See [setSpanDuration] for the contract.
//
// The measurement runs from when the registered handler is entered until it returns,
// so whatever was composed into that handler — including any hand-wrapped middleware — is included.
//
// It records in a defer, so a panicking handler is still measured. There is no check for whether tracing
// is configured, because there is nothing to check at registration time: with no span on the request there
// is nowhere to write, and [setSpanDuration] returns without doing anything.
func (t *TracingMux) wrapHandlerFunc(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		defer func() {
			setSpanDuration(r.Context(), "handler.duration_ms", time.Since(start))
		}()

		h(w, r)
	}
}
