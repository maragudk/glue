package http_test

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	g "maragu.dev/gomponents"
	"maragu.dev/is"

	"maragu.dev/glue/html"
	gluehttp "maragu.dev/glue/http"
	"maragu.dev/glue/model"
)

type mockSessionManager struct {
	exists    bool
	destroyed bool
}

func (m *mockSessionManager) Exists(ctx context.Context, key string) bool {
	return m.exists
}

func (m *mockSessionManager) GetString(ctx context.Context, key string) string {
	return "u_123"
}

func (m *mockSessionManager) Destroy(ctx context.Context) error {
	m.destroyed = true
	return nil

}

type mockUserActiveChecker struct {
	active bool
	err    error
}

func (m *mockUserActiveChecker) IsUserActive(ctx context.Context, id model.UserID) (bool, error) {
	return m.active, m.err
}

func TestAuthenticate(t *testing.T) {
	tests := []struct {
		name                    string
		sessionExists           bool
		userActive              bool
		userActiveErr           error
		expectStatus            int
		expectDestroySession    bool
		expectNextHandlerCalled bool
		expectUserIDInContext   bool
	}{
		{
			name:                    "no session",
			sessionExists:           false,
			expectStatus:            http.StatusOK,
			expectDestroySession:    false,
			expectNextHandlerCalled: true,
			expectUserIDInContext:   false,
		},
		{
			name:                    "session exists, user active",
			sessionExists:           true,
			userActive:              true,
			expectStatus:            http.StatusOK,
			expectDestroySession:    false,
			expectNextHandlerCalled: true,
			expectUserIDInContext:   true,
		},
		{
			name:                    "session exists, user not active",
			sessionExists:           true,
			userActive:              false,
			expectStatus:            http.StatusOK,
			expectDestroySession:    true,
			expectNextHandlerCalled: true,
			expectUserIDInContext:   false,
		},
		{
			name:                    "session exists, user not found",
			sessionExists:           true,
			userActiveErr:           model.ErrorUserNotFound,
			expectStatus:            http.StatusOK,
			expectDestroySession:    true,
			expectNextHandlerCalled: true,
			expectUserIDInContext:   false,
		},
		{
			name:                    "session exists, error checking user",
			sessionExists:           true,
			userActiveErr:           errors.New("oh no"),
			expectStatus:            http.StatusInternalServerError,
			expectDestroySession:    false,
			expectNextHandlerCalled: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sm := &mockSessionManager{exists: test.sessionExists}
			userActiveChecker := &mockUserActiveChecker{active: test.userActive, err: test.userActiveErr}

			authenticate := gluehttp.Authenticate(slog.New(slog.DiscardHandler), sm, userActiveChecker)

			var called bool
			var userID *model.UserID
			h := authenticate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				userID = gluehttp.GetUserIDFromContext(r.Context())
			}))

			req := httptest.NewRequest(http.MethodGet, "/", nil)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			is.Equal(t, test.expectStatus, rec.Code)
			is.Equal(t, test.expectDestroySession, sm.destroyed)
			is.Equal(t, test.expectNextHandlerCalled, called)
			if test.expectUserIDInContext {
				is.NotNil(t, userID)
				is.Equal(t, "u_123", *userID)
			} else {
				is.Nil(t, userID)
			}
		})
	}
}

type mockPermissionsChecker struct {
	hasPermissions bool
	err            error
}

func (m *mockPermissionsChecker) HasPermissions(ctx context.Context, id model.UserID, permissions []model.Permission) (bool, error) {
	return m.hasPermissions, m.err
}

type mockPermissionsGetter struct {
	permissions []model.Permission
	err         error
}

func (m *mockPermissionsGetter) GetPermissions(ctx context.Context, id model.UserID) ([]model.Permission, error) {
	return m.permissions, m.err
}

func TestAuthorize(t *testing.T) {
	tests := []struct {
		name                    string
		userIDInContext         bool
		hasPermissions          bool
		hasPermissionsErr       error
		expectStatus            int
		expectNextHandlerCalled bool
		expectRedirectToLogin   bool
	}{
		{
			name:                    "no user ID in context",
			userIDInContext:         false,
			expectStatus:            http.StatusTemporaryRedirect,
			expectNextHandlerCalled: false,
			expectRedirectToLogin:   true,
		},
		{
			name:                    "user has permissions",
			userIDInContext:         true,
			hasPermissions:          true,
			expectStatus:            http.StatusOK,
			expectNextHandlerCalled: true,
		},
		{
			name:                    "user does not have permissions",
			userIDInContext:         true,
			hasPermissions:          false,
			expectStatus:            http.StatusForbidden,
			expectNextHandlerCalled: false,
		},
		{
			name:                    "error checking permissions",
			userIDInContext:         true,
			hasPermissionsErr:       errors.New("oh no"),
			expectStatus:            http.StatusInternalServerError,
			expectNextHandlerCalled: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pc := &mockPermissionsChecker{
				hasPermissions: test.hasPermissions,
				err:            test.hasPermissionsErr,
			}

			authorize := gluehttp.Authorize(slog.New(slog.DiscardHandler), pc, "read", "write")

			var called bool
			h := authorize(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
			}))

			req := httptest.NewRequest(http.MethodGet, "/protected", nil)

			if test.userIDInContext {
				userID := model.UserID("u_123")
				ctx := context.WithValue(req.Context(), gluehttp.ContextKey("userID"), &userID)
				req = req.WithContext(ctx)
			}

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			is.Equal(t, test.expectStatus, rec.Code)
			is.Equal(t, test.expectNextHandlerCalled, called)

			if test.expectRedirectToLogin {
				is.Equal(t, "/login?redirect=%2Fprotected", rec.Header().Get("Location"))
			}
		})
	}
}

func TestSavePermissionsInContext(t *testing.T) {
	tests := []struct {
		name                    string
		userIDInContext         bool
		permissions             []model.Permission
		getPermissionsErr       error
		expectStatus            int
		expectNextHandlerCalled bool
	}{
		{
			name:                    "no user ID in context",
			userIDInContext:         false,
			expectStatus:            http.StatusOK,
			expectNextHandlerCalled: true,
		},
		{
			name:                    "user with permissions",
			userIDInContext:         true,
			permissions:             []model.Permission{"read", "write"},
			expectStatus:            http.StatusOK,
			expectNextHandlerCalled: true,
		},
		{
			name:                    "user with no permissions",
			userIDInContext:         true,
			permissions:             []model.Permission{},
			expectStatus:            http.StatusOK,
			expectNextHandlerCalled: true,
		},
		{
			name:                    "error getting permissions",
			userIDInContext:         true,
			getPermissionsErr:       errors.New("oh no"),
			expectStatus:            http.StatusInternalServerError,
			expectNextHandlerCalled: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pg := &mockPermissionsGetter{
				permissions: test.permissions,
				err:         test.getPermissionsErr,
			}

			savePermissions := gluehttp.SavePermissionsInContext(slog.New(slog.DiscardHandler), pg)

			var called bool
			var permissions []model.Permission
			h := savePermissions(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				permissions = gluehttp.GetPermissionsFromContext(r.Context())
			}))

			req := httptest.NewRequest(http.MethodGet, "/", nil)

			if test.userIDInContext {
				userID := model.UserID("u_123")
				ctx := context.WithValue(req.Context(), gluehttp.ContextKey("userID"), &userID)
				req = req.WithContext(ctx)
			}

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			is.Equal(t, test.expectStatus, rec.Code)
			is.Equal(t, test.expectNextHandlerCalled, called)
			is.EqualSlice(t, test.permissions, permissions)
		})
	}
}

func TestLogout(t *testing.T) {
	tests := []struct {
		name                 string
		userIDInContext      bool
		destroyError         error
		queryRedirect        string
		expectStatus         int
		expectRedirect       string
		expectDestroySession bool
	}{
		{
			name:                 "successful logout with default redirect",
			userIDInContext:      true,
			expectStatus:         http.StatusFound,
			expectRedirect:       "/",
			expectDestroySession: true,
		},
		{
			name:                 "successful logout with custom redirect",
			userIDInContext:      true,
			queryRedirect:        "/dashboard",
			expectStatus:         http.StatusFound,
			expectRedirect:       "/dashboard",
			expectDestroySession: true,
		},
		{
			name:                 "no user in context",
			userIDInContext:      false,
			expectStatus:         http.StatusFound,
			expectRedirect:       "/",
			expectDestroySession: false,
		},
		{
			name:                 "no user in context with custom redirect",
			userIDInContext:      false,
			queryRedirect:        "/dashboard",
			expectStatus:         http.StatusFound,
			expectRedirect:       "/dashboard",
			expectDestroySession: false,
		},
		{
			name:                 "destroy session error",
			userIDInContext:      true,
			destroyError:         errors.New("destroy error"),
			expectStatus:         http.StatusInternalServerError,
			expectDestroySession: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sm := &mockSessionDestroyer{err: test.destroyError}

			mux := chi.NewRouter()
			router := &gluehttp.Router{Mux: mux}
			mockPage := func(props html.PageProps, children ...g.Node) g.Node {
				return g.Text("error")
			}
			gluehttp.Logout(router, slog.New(slog.DiscardHandler), sm, mockPage)

			req := httptest.NewRequest(http.MethodPost, "/logout?redirect="+test.queryRedirect, nil)

			if test.userIDInContext {
				userID := model.UserID("u_123")
				ctx := context.WithValue(req.Context(), gluehttp.ContextKey("userID"), &userID)
				req = req.WithContext(ctx)
			}

			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)

			is.Equal(t, test.expectStatus, rec.Code)
			is.Equal(t, test.expectDestroySession, sm.destroyed)
			is.Equal(t, test.expectRedirect, rec.Header().Get("Location"))
		})
	}
}

type mockSessionDestroyer struct {
	destroyed bool
	err       error
}

func (m *mockSessionDestroyer) Destroy(ctx context.Context) error {
	m.destroyed = true
	return m.err
}

func TestRedirectIfAuthenticated(t *testing.T) {
	tests := []struct {
		name                    string
		userIDInContext         bool
		redirectTo              string
		expectStatus            int
		expectRedirect          string
		expectNextHandlerCalled bool
	}{
		{
			name:                    "user authenticated",
			userIDInContext:         true,
			redirectTo:              "/dashboard",
			expectStatus:            http.StatusTemporaryRedirect,
			expectRedirect:          "/dashboard",
			expectNextHandlerCalled: false,
		},
		{
			name:                    "user not authenticated",
			userIDInContext:         false,
			redirectTo:              "/dashboard",
			expectStatus:            http.StatusOK,
			expectNextHandlerCalled: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			middleware := gluehttp.RedirectIfAuthenticated(test.redirectTo)

			var called bool
			h := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
			}))

			req := httptest.NewRequest(http.MethodGet, "/login", nil)

			if test.userIDInContext {
				userID := model.UserID("u_123")
				ctx := context.WithValue(req.Context(), gluehttp.ContextKey("userID"), &userID)
				req = req.WithContext(ctx)
			}

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			is.Equal(t, test.expectStatus, rec.Code)
			is.Equal(t, test.expectNextHandlerCalled, called)
			is.Equal(t, test.expectRedirect, rec.Header().Get("Location"))
		})
	}
}
