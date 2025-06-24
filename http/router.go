package http

import (
	"net/http"

	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/chi/v5"
	. "maragu.dev/gomponents"
	. "maragu.dev/gomponents/http"

	"maragu.dev/glue/html"
)

type Router struct {
	Mux chi.Router
	SM  *scs.SessionManager
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

func GetProps(w http.ResponseWriter, r *http.Request) html.PageProps {
	return html.PageProps{
		Ctx:         r.Context(),
		R:           r,
		UserID:      GetUserIDFromContext(r.Context()),
		W:           w,
		Permissions: GetPermissionsFromContext(r.Context()),
	}
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

func GetPathParam(r *http.Request, name string) string {
	return chi.URLParam(r, name)
}
