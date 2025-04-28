package http_test

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"maragu.dev/is"

	ghttp "maragu.dev/gloo/http"
	"maragu.dev/gloo/model"
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

func (m *mockUserActiveChecker) IsUserActive(ctx context.Context, id model.ID) (bool, error) {
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

			authenticate := ghttp.Authenticate(slog.New(slog.DiscardHandler), sm, userActiveChecker)

			var called bool
			var userID *model.ID
			h := authenticate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				userID = ghttp.GetUserIDFromContext(r.Context())
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

func (m *mockPermissionsChecker) HasPermissions(ctx context.Context, userID *model.ID, permissions []string) (bool, error) {
	return m.hasPermissions, m.err
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

			authorize := ghttp.Authorize(slog.New(slog.DiscardHandler), pc, "read", "write")

			var called bool
			h := authorize(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
			}))

			req := httptest.NewRequest(http.MethodGet, "/protected", nil)

			if test.userIDInContext {
				userID := model.ID("u_123")
				ctx := context.WithValue(req.Context(), ghttp.ContextKey("userID"), &userID)
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
