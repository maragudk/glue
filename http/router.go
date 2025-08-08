package http

import (
	"net/http"

	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/chi/v5"
	"go.opentelemetry.io/otel/trace"
	. "maragu.dev/gomponents"
	. "maragu.dev/gomponents/http"

	"maragu.dev/glue/html"
)

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
	r.Mux.Get(path, Adapt(func(w http.ResponseWriter, r *http.Request) (Node, error) {
		return cb(GetProps(w, r))
	}))
}

func (r *Router) Post(path string, cb func(props html.PageProps) (Node, error)) {
	r.Mux.Post(path, Adapt(func(w http.ResponseWriter, r *http.Request) (Node, error) {
		return cb(GetProps(w, r))
	}))
}

func (r *Router) Put(path string, cb func(props html.PageProps) (Node, error)) {
	r.Mux.Put(path, Adapt(func(w http.ResponseWriter, r *http.Request) (Node, error) {
		return cb(GetProps(w, r))
	}))
}

func (r *Router) Delete(path string, cb func(props html.PageProps) (Node, error)) {
	r.Mux.Delete(path, Adapt(func(w http.ResponseWriter, r *http.Request) (Node, error) {
		return cb(GetProps(w, r))
	}))
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
