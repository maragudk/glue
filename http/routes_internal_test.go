package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	g "maragu.dev/gomponents"
	"maragu.dev/is"

	"maragu.dev/glue/html"
	"maragu.dev/glue/model"
)

func TestSetupRoutes(t *testing.T) {
	t.Run("renders the not-found page for an authenticated user with their props", func(t *testing.T) {
		var props html.PageProps
		s := NewServer(NewServerOptions{
			BaseURL: "https://example.com",
			HTMLPage: func(p html.PageProps, children ...g.Node) g.Node {
				props = p
				return g.Group(children)
			},
			PermissionsGetter: &mockPermissionsGetter{permissions: []model.Permission{"read"}},
			UserActiveChecker: &mockUserActiveChecker{active: true},
		})
		s.setupRoutes()

		req := httptest.NewRequest(http.MethodGet, "/no-such-path", nil)
		req.AddCookie(&http.Cookie{Name: s.r.SM.Cookie.Name, Value: newSessionToken(t, s, "u_123")})

		rec := httptest.NewRecorder()
		s.r.Mux.ServeHTTP(rec, req)

		is.Equal(t, http.StatusNotFound, rec.Code)
		is.Equal(t, "Not found", props.Title)
		is.NotNil(t, props.UserID)
		is.Equal(t, model.UserID("u_123"), *props.UserID)
		is.EqualSlice(t, []model.Permission{"read"}, props.Permissions)
		is.True(t, strings.Contains(rec.Body.String(), "<h1>Not found</h1>"))
	})

	t.Run("still routes matched paths, since the not-found handler only handles the rest", func(t *testing.T) {
		s := NewServer(NewServerOptions{
			BaseURL:  "https://example.com",
			HTMLPage: func(p html.PageProps, children ...g.Node) g.Node { return g.Group(children) },
			HTTPRouterInjector: func(r *Router) {
				r.Get("/thing", func(props html.PageProps) (g.Node, error) {
					return g.Text("the thing"), nil
				})
			},
			UserActiveChecker: &mockUserActiveChecker{active: true},
		})
		s.setupRoutes()

		rec := httptest.NewRecorder()
		s.r.Mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/thing", nil))

		is.Equal(t, http.StatusOK, rec.Code)
		is.Equal(t, "the thing", rec.Body.String())
	})

	t.Run("renders the not-found page for an anonymous user without a user ID", func(t *testing.T) {
		var props html.PageProps
		s := NewServer(NewServerOptions{
			BaseURL: "https://example.com",
			HTMLPage: func(p html.PageProps, children ...g.Node) g.Node {
				props = p
				return g.Group(children)
			},
			UserActiveChecker: &mockUserActiveChecker{active: true},
		})
		s.setupRoutes()

		rec := httptest.NewRecorder()
		s.r.Mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/no-such-path", nil))

		is.Equal(t, http.StatusNotFound, rec.Code)
		is.Equal(t, "Not found", props.Title)
		is.Nil(t, props.UserID)
	})
}

// newSessionToken commits a session holding the given user ID to the server's session store, and returns
// the token to send as the session cookie.
func newSessionToken(t *testing.T, s *Server, userID model.UserID) string {
	t.Helper()

	ctx, err := s.r.SM.Load(context.Background(), "")
	is.NotError(t, err)

	s.r.SM.Put(ctx, SessionUserIDKey, string(userID))

	token, _, err := s.r.SM.Commit(ctx)
	is.NotError(t, err)

	return token
}

type mockUserActiveChecker struct {
	active bool
}

func (m *mockUserActiveChecker) IsUserActive(ctx context.Context, id model.UserID) (bool, error) {
	return m.active, nil
}

type mockPermissionsGetter struct {
	permissions []model.Permission
}

func (m *mockPermissionsGetter) GetPermissions(ctx context.Context, id model.UserID) ([]model.Permission, error) {
	return m.permissions, nil
}
