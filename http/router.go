package http

import (
	"context"
	"errors"
	"net/http"

	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/chi/v5"
	"go.opentelemetry.io/otel/trace"
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
			return n, Error{Code: statusClientClosedRequest}
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

// TracingMux is a decorator that satisfies [chi.Router] but adds tracing to all HTTP methods.
type TracingMux struct {
	mux    chi.Router
	tracer trace.Tracer
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
	return &TracingMux{
		mux:    t.mux.With(middlewares...),
		tracer: t.tracer,
	}
}

func (t *TracingMux) Group(fn func(r chi.Router)) chi.Router {
	return &TracingMux{
		mux: t.mux.Group(func(r chi.Router) {
			fn(&TracingMux{
				mux:    r,
				tracer: t.tracer,
			})
		}),
		tracer: t.tracer,
	}
}

func (t *TracingMux) Route(pattern string, fn func(r chi.Router)) chi.Router {
	return &TracingMux{
		mux: t.mux.Route(pattern, func(r chi.Router) {
			fn(&TracingMux{
				mux:    r,
				tracer: t.tracer,
			})
		}),
		tracer: t.tracer,
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

func (t *TracingMux) wrapHandlerFunc(h http.HandlerFunc) http.HandlerFunc {
	if t.tracer == nil {
		return h
	}
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, span := t.tracer.Start(r.Context(), "http.Handler")
		defer span.End()
		h(w, r.WithContext(ctx))
	}
}
