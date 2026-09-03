package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	g "maragu.dev/gomponents"
	ghtml "maragu.dev/gomponents/html"
	"maragu.dev/httph"
	"maragu.dev/is"

	"maragu.dev/glue/html"
	"maragu.dev/glue/model"
)

func TestSetupRoutes(t *testing.T) {
	t.Run("renders the not-found page with security headers and a nonce for an unmatched path", func(t *testing.T) {
		s, page := newTestRoutes(t, NewServerOptions{CSP: scriptNonce})

		res := get(t, s, "/nope")

		is.Equal(t, http.StatusNotFound, res.Code)
		is.Equal(t, 1, page.calls)
		is.Equal(t, "Not found", page.props.Title)
		is.True(t, strings.Contains(res.Body.String(), "<h1>Not found</h1>"))

		is.True(t, page.props.Nonce != "", "the page should have gotten a nonce")
		is.True(t, strings.Contains(res.Header().Get("Content-Security-Policy"), "'nonce-"+page.props.Nonce+"'"))
		is.Equal(t, "deny", res.Header().Get("X-Frame-Options"))
		is.Equal(t, "no-store", res.Header().Get("Cache-Control"))
	})

	t.Run("renders the not-found page with security headers and no nonce where the CSP asks for none", func(t *testing.T) {
		s, page := newTestRoutes(t, NewServerOptions{})

		res := get(t, s, "/nope")

		is.Equal(t, http.StatusNotFound, res.Code)
		is.Equal(t, "", page.props.Nonce)
		is.True(t, res.Header().Get("Content-Security-Policy") != "")
		is.Equal(t, "deny", res.Header().Get("X-Frame-Options"))
		is.Equal(t, "", res.Header().Get("Cache-Control"))
	})

	t.Run("renders the not-found page with the session's user and permissions", func(t *testing.T) {
		s, page := newTestRoutes(t, NewServerOptions{
			CSP:               scriptNonce,
			PermissionsGetter: &mockPermissionsGetter{permissions: []model.Permission{"read"}},
			UserActiveChecker: &mockActiveUserChecker{},
		})

		res := get(t, s, "/nope", sessionCookie(t, s, "u_123"))

		is.Equal(t, http.StatusNotFound, res.Code)
		is.Equal(t, 1, page.calls)
		is.NotNil(t, page.props.UserID)
		is.Equal(t, model.UserID("u_123"), *page.props.UserID)
		is.EqualSlice(t, []model.Permission{"read"}, page.props.Permissions)
		is.True(t, page.props.Nonce != "", "the page should have gotten a nonce")
	})

	t.Run("renders the not-found page for an unmatched path under a route an app mounts", func(t *testing.T) {
		s, page := newTestRoutes(t, NewServerOptions{
			CSP: scriptNonce,
			HTTPRouterInjector: func(r *Router) {
				r.Route("/sub", func(r *Router) {
					r.Get("/thing", func(props html.PageProps) (g.Node, error) {
						return ghtml.P(g.Text("thing")), nil
					})
				})
			},
		})

		res := get(t, s, "/sub/nope")

		is.Equal(t, http.StatusNotFound, res.Code)
		is.Equal(t, 1, page.calls, "the not-found page should be rendered exactly once")
		is.Equal(t, "Not found", page.props.Title)

		is.True(t, page.props.Nonce != "", "the page should have gotten a nonce")
		is.True(t, strings.Contains(res.Header().Get("Content-Security-Policy"), "'nonce-"+page.props.Nonce+"'"))
		is.Equal(t, "deny", res.Header().Get("X-Frame-Options"))
		is.Equal(t, "no-store", res.Header().Get("Cache-Control"))
	})

	t.Run("renders the not-found page for an unmatched path under a sub-router an app mounts", func(t *testing.T) {
		sub := NewRouter(NewRouterOpts{})
		sub.Get("/thing", func(props html.PageProps) (g.Node, error) {
			return ghtml.P(g.Text("thing")), nil
		})

		s, page := newTestRoutes(t, NewServerOptions{
			CSP: scriptNonce,
			HTTPRouterInjector: func(r *Router) {
				r.Mux.Mount("/sub", sub.Mux)
			},
		})

		res := get(t, s, "/sub/nope")

		is.Equal(t, http.StatusNotFound, res.Code)
		is.Equal(t, 1, page.calls, "the not-found page should be rendered exactly once")
		is.True(t, page.props.Nonce != "", "the page should have gotten a nonce")
		is.True(t, strings.Contains(res.Header().Get("Content-Security-Policy"), "'nonce-"+page.props.Nonce+"'"))
		is.Equal(t, "deny", res.Header().Get("X-Frame-Options"))
		is.Equal(t, "no-store", res.Header().Get("Cache-Control"))
	})

	t.Run("renders the not-found page for an asset-looking path the static routes do not match", func(t *testing.T) {
		s, page := newTestRoutes(t, NewServerOptions{CSP: scriptNonce})

		res := get(t, s, "/static/nope")

		is.Equal(t, http.StatusNotFound, res.Code)
		is.Equal(t, 1, page.calls)
		is.True(t, page.props.Nonce != "", "the page should have gotten a nonce")
		is.Equal(t, "deny", res.Header().Get("X-Frame-Options"))
	})

	// A path the static routes match is served by [http.FileServer], which answers a missing file with
	// its own plain-text 404 without ever reaching chi's not-found handler, so it gets neither the
	// not-found page nor the HTML group's headers.
	for _, path := range []string{"/nope.js", "/images/nope.png", "/scripts/nope.js", "/styles/nope.css"} {
		t.Run("leaves "+path+" to the file server, which answers a missing file with a plain 404", func(t *testing.T) {
			s, page := newTestRoutes(t, NewServerOptions{CSP: scriptNonce})

			res := get(t, s, path)

			is.Equal(t, http.StatusNotFound, res.Code)
			is.Equal(t, 0, page.calls, "the not-found page should not be rendered")
			is.Equal(t, "404 page not found\n", res.Body.String())
			is.Equal(t, "", res.Header().Get("Content-Security-Policy"))
		})
	}

	t.Run("marks a nonced response which writes a session cookie as no-store", func(t *testing.T) {
		s, _ := newTestRoutes(t, NewServerOptions{
			CSP: scriptNonce,
			HTTPRouterInjector: func(r *Router) {
				r.Get("/thing", func(props html.PageProps) (g.Node, error) {
					r.SM.Put(props.Ctx, "greeting", "hi")
					return ghtml.P(g.Text("thing")), nil
				})
			},
		})

		res := get(t, s, "/thing")

		is.Equal(t, http.StatusOK, res.Code)
		is.True(t, res.Header().Get("Set-Cookie") != "", "the session cookie should have been written")
		is.True(t, slices.Contains(res.Header().Values("Cache-Control"), "no-store"),
			"got Cache-Control "+strings.Join(res.Header().Values("Cache-Control"), " | "))
	})

	t.Run("flushes a nonced response through the whole middleware chain", func(t *testing.T) {
		s, _ := newTestRoutes(t, NewServerOptions{
			CSP: scriptNonce,
			HTTPRouterInjector: func(r *Router) {
				r.Get("/thing", func(props html.PageProps) (g.Node, error) {
					_, err := props.W.Write([]byte("thing"))
					is.NotError(t, err)
					is.NotError(t, http.NewResponseController(props.W).Flush())
					return nil, nil
				})
			},
		})

		res := get(t, s, "/thing")

		is.Equal(t, http.StatusOK, res.Code)
		is.True(t, res.Flushed, "the flush should have reached the underlying writer")
	})

	t.Run("renders a matched page with a nonce", func(t *testing.T) {
		var nonce string
		s, _ := newTestRoutes(t, NewServerOptions{
			CSP: scriptNonce,
			HTTPRouterInjector: func(r *Router) {
				r.Get("/thing", func(props html.PageProps) (g.Node, error) {
					nonce = props.Nonce
					return ghtml.P(g.Text("thing")), nil
				})
			},
		})

		res := get(t, s, "/thing")

		is.Equal(t, http.StatusOK, res.Code)
		is.True(t, nonce != "", "the page should have gotten a nonce")
		is.True(t, strings.Contains(res.Header().Get("Content-Security-Policy"), "'nonce-"+nonce+"'"))
		is.Equal(t, "no-store", res.Header().Get("Cache-Control"))
	})
}

func scriptNonce(opts *httph.ContentSecurityPolicyOptions) {
	opts.ScriptNonce = true
}

type mockPermissionsGetter struct {
	permissions []model.Permission
}

func (m *mockPermissionsGetter) GetPermissions(ctx context.Context, id model.UserID) ([]model.Permission, error) {
	return m.permissions, nil
}

type mockActiveUserChecker struct{}

func (m *mockActiveUserChecker) IsUserActive(ctx context.Context, id model.UserID) (bool, error) {
	return true, nil
}

// recordedPage keeps what the page function under test was last called with, and how often.
type recordedPage struct {
	calls int
	props html.PageProps
}

// newTestRoutes builds a [Server] with the given options and its routes set up, together with the
// recorder for the page function it renders through.
func newTestRoutes(t *testing.T, opts NewServerOptions) (*Server, *recordedPage) {
	t.Helper()

	page := &recordedPage{}

	opts.BaseURL = "https://example.com"
	opts.HTMLPage = func(props html.PageProps, children ...g.Node) g.Node {
		page.calls++
		page.props = props
		return ghtml.Body(g.Group(children))
	}

	s := NewServer(opts)
	s.setupRoutes()

	return s, page
}

// sessionCookie for a stored session holding the given user ID, as a logged-in request carries it.
func sessionCookie(t *testing.T, s *Server, userID model.UserID) *http.Cookie {
	t.Helper()

	ctx, err := s.r.SM.Load(t.Context(), "")
	is.NotError(t, err)

	s.r.SM.Put(ctx, SessionUserIDKey, string(userID))

	token, _, err := s.r.SM.Commit(ctx)
	is.NotError(t, err)

	return &http.Cookie{Name: s.r.SM.Cookie.Name, Value: token}
}

func get(t *testing.T, s *Server, target string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, target, nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}

	res := httptest.NewRecorder()
	s.server.Handler.ServeHTTP(res, req)
	return res
}
