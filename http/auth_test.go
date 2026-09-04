package http_test

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/go-chi/chi/v5"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	g "maragu.dev/gomponents"
	"maragu.dev/is"

	"maragu.dev/glue/html"
	gluehttp "maragu.dev/glue/http"
	"maragu.dev/glue/model"
	"maragu.dev/glue/oteltest"
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
		userPermissions         []model.Permission
		getPermissionsErr       error
		requiredPermissions     []model.Permission
		expectStatus            int
		expectNextHandlerCalled bool
		expectRedirectToLogin   bool
	}{
		{
			name:                    "no user ID in context",
			userIDInContext:         false,
			requiredPermissions:     []model.Permission{"read", "write"},
			expectStatus:            http.StatusTemporaryRedirect,
			expectNextHandlerCalled: false,
			expectRedirectToLogin:   true,
		},
		{
			name:                    "user has all required permissions",
			userIDInContext:         true,
			userPermissions:         []model.Permission{"read", "write", "admin"},
			requiredPermissions:     []model.Permission{"read", "write"},
			expectStatus:            http.StatusOK,
			expectNextHandlerCalled: true,
		},
		{
			name:                    "user missing one permission",
			userIDInContext:         true,
			userPermissions:         []model.Permission{"read"},
			requiredPermissions:     []model.Permission{"read", "write"},
			expectStatus:            http.StatusForbidden,
			expectNextHandlerCalled: false,
		},
		{
			name:                    "user has no permissions",
			userIDInContext:         true,
			userPermissions:         []model.Permission{},
			requiredPermissions:     []model.Permission{"read"},
			expectStatus:            http.StatusForbidden,
			expectNextHandlerCalled: false,
		},
		{
			name:                    "error getting permissions",
			userIDInContext:         true,
			getPermissionsErr:       errors.New("oh no"),
			requiredPermissions:     []model.Permission{"read"},
			expectStatus:            http.StatusInternalServerError,
			expectNextHandlerCalled: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pg := &mockPermissionsGetter{
				permissions: test.userPermissions,
				err:         test.getPermissionsErr,
			}

			authorize := gluehttp.Authorize(slog.New(slog.DiscardHandler), pg, test.requiredPermissions...)

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

// TestPermissionsOnSpan runs the same table against each middleware which fetches permissions.
func TestPermissionsOnSpan(t *testing.T) {
	tests := []struct {
		name       string
		middleware func(pg *mockPermissionsGetter) gluehttp.Middleware
	}{
		{
			name: "Authorize",
			middleware: func(pg *mockPermissionsGetter) gluehttp.Middleware {
				return gluehttp.Authorize(slog.New(slog.DiscardHandler), pg, "read")
			},
		},
		{
			name: "SavePermissionsInContext",
			middleware: func(pg *mockPermissionsGetter) gluehttp.Middleware {
				return gluehttp.SavePermissionsInContext(slog.New(slog.DiscardHandler), pg)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name+" should set the permissions on the current span", func(t *testing.T) {
			sr := oteltest.NewSpanRecorder(t)

			pg := &mockPermissionsGetter{permissions: []model.Permission{"read", "write"}}

			ctx, rootSpan := otel.Tracer("test").Start(t.Context(), "root")
			userID := model.UserID("u_123")
			ctx = context.WithValue(ctx, gluehttp.ContextKey("userID"), &userID)

			var called bool
			h := test.middleware(pg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
			}))
			h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx))
			rootSpan.End()

			is.True(t, called)

			attrs := endedSpanNamed(t, sr, "root").Attributes()
			is.True(t, oteltest.HasAttribute(attrs, attribute.StringSlice("enduser.permissions", []string{"read", "write"})),
				"expected enduser.permissions on the current span")
		})

		t.Run(test.name+" should set an empty permissions attribute for a user with no permissions", func(t *testing.T) {
			sr := oteltest.NewSpanRecorder(t)

			pg := &mockPermissionsGetter{permissions: []model.Permission{}}

			ctx, rootSpan := otel.Tracer("test").Start(t.Context(), "root")
			userID := model.UserID("u_123")
			ctx = context.WithValue(ctx, gluehttp.ContextKey("userID"), &userID)

			h := test.middleware(pg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
			h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx))
			rootSpan.End()

			// An empty list means "we looked and there were none", which is not the same as never looking
			attrs := endedSpanNamed(t, sr, "root").Attributes()
			is.True(t, oteltest.HasAttribute(attrs, attribute.StringSlice("enduser.permissions", nil)),
				"expected an empty enduser.permissions on the current span")
		})

		t.Run(test.name+" should set no permissions on a span which is not recording", func(t *testing.T) {
			sr := oteltest.NewSpanRecorder(t)

			pg := &mockPermissionsGetter{permissions: []model.Permission{"read", "write"}}

			ctx, rootSpan := otel.Tracer("test").Start(t.Context(), "root")
			rootSpan.End()

			userID := model.UserID("u_123")
			ctx = context.WithValue(ctx, gluehttp.ContextKey("userID"), &userID)

			h := test.middleware(pg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
			h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx))

			attrs := endedSpanNamed(t, sr, "root").Attributes()
			is.True(t, !oteltest.HasAttributeKey(attrs, "enduser.permissions"),
				"expected no enduser.permissions on a span which had already ended")
		})

		t.Run(test.name+" should set no permissions on the current span when there is no user", func(t *testing.T) {
			sr := oteltest.NewSpanRecorder(t)

			pg := &mockPermissionsGetter{permissions: []model.Permission{"read", "write"}}

			ctx, rootSpan := otel.Tracer("test").Start(t.Context(), "root")

			h := test.middleware(pg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
			h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx))
			rootSpan.End()

			attrs := endedSpanNamed(t, sr, "root").Attributes()
			is.True(t, !oteltest.HasAttributeKey(attrs, "enduser.permissions"),
				"expected no enduser.permissions on the current span")
		})

		t.Run(test.name+" should not panic when there is no span in the context", func(t *testing.T) {
			oteltest.NewSpanRecorder(t)

			pg := &mockPermissionsGetter{permissions: []model.Permission{"read", "write"}}

			userID := model.UserID("u_123")
			ctx := context.WithValue(t.Context(), gluehttp.ContextKey("userID"), &userID)

			var called bool
			h := test.middleware(pg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
			}))
			h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx))

			is.True(t, called)
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

// TestMiddlewareTimings covers the attributes which replaced the per-middleware spans. Each records only
// its own work, so the value must be well under the time the handler below it spends.
func TestMiddlewareTimings(t *testing.T) {
	tests := []struct {
		name       string
		key        attribute.Key
		middleware gluehttp.Middleware
		withUser   bool
	}{
		{
			name:       "Authenticate",
			key:        "authn.duration_ms",
			middleware: gluehttp.Authenticate(slog.New(slog.DiscardHandler), &mockSessionManager{exists: true}, &mockUserActiveChecker{active: true}),
		},
		{
			name:       "Authorize",
			key:        "authz.duration_ms",
			middleware: gluehttp.Authorize(slog.New(slog.DiscardHandler), &mockPermissionsGetter{permissions: []model.Permission{"read"}}, "read"),
			withUser:   true,
		},
		{
			name:       "SavePermissionsInContext",
			key:        "permissions.duration_ms",
			middleware: gluehttp.SavePermissionsInContext(slog.New(slog.DiscardHandler), &mockPermissionsGetter{permissions: []model.Permission{"read"}}),
			withUser:   true,
		},
	}

	for _, test := range tests {
		t.Run(test.name+" should record its own duration on the current span", func(t *testing.T) {
			// Everything runs inside the bubble, where the clock is fake and only advances when something
			// sleeps. The middleware itself never sleeps, so its own work measures exactly zero, and the
			// handler's sleep is the tripwire: had it been counted, the measurement would be exactly 20.
			synctest.Test(t, func(t *testing.T) {
				sr := oteltest.NewSpanRecorder(t)

				ctx, rootSpan := otel.Tracer("test").Start(t.Context(), "root")
				if test.withUser {
					userID := model.UserID("u_123")
					ctx = context.WithValue(ctx, gluehttp.ContextKey("userID"), &userID)
				}

				var called bool
				h := test.middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					called = true
					time.Sleep(20 * time.Millisecond)
				}))
				h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx))
				rootSpan.End()

				is.True(t, called)

				attrs := endedSpanNamed(t, sr, "root").Attributes()
				is.True(t, oteltest.HasAttributeKey(attrs, test.key), "expected "+string(test.key))

				for _, attr := range attrs {
					if attr.Key != test.key {
						continue
					}
					is.Equal(t, attribute.FLOAT64, attr.Value.Type())
					is.Equal(t, float64(0), attr.Value.AsFloat64())
				}
			})
		})

		t.Run(test.name+" should not start a span of its own", func(t *testing.T) {
			sr := oteltest.NewSpanRecorder(t)

			ctx, rootSpan := otel.Tracer("test").Start(t.Context(), "root")
			if test.withUser {
				userID := model.UserID("u_123")
				ctx = context.WithValue(ctx, gluehttp.ContextKey("userID"), &userID)
			}

			h := test.middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
			h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx))
			rootSpan.End()

			// Only the root span the test made, nothing from the middleware
			is.Equal(t, 1, len(sr.Ended()))
		})
	}
}

// TestMiddlewareErrors covers errors raised by this package's own middleware, which fail the request
// before any application handler runs and so cannot be recorded by the application.
func TestMiddlewareErrors(t *testing.T) {
	tests := []struct {
		name              string
		middleware        gluehttp.Middleware
		withUser          bool
		expectDescription string
	}{
		{
			name:              "Authenticate",
			middleware:        gluehttp.Authenticate(slog.New(slog.DiscardHandler), &mockSessionManager{exists: true}, &mockUserActiveChecker{err: errors.New("oh no")}),
			expectDescription: "error getting user after authentication",
		},
		{
			name:              "Authorize",
			middleware:        gluehttp.Authorize(slog.New(slog.DiscardHandler), &mockPermissionsGetter{err: errors.New("oh no")}, "read"),
			withUser:          true,
			expectDescription: "error getting permissions",
		},
		{
			name:              "SavePermissionsInContext",
			middleware:        gluehttp.SavePermissionsInContext(slog.New(slog.DiscardHandler), &mockPermissionsGetter{err: errors.New("oh no")}),
			withUser:          true,
			expectDescription: "error getting permissions",
		},
	}

	for _, test := range tests {
		t.Run(test.name+" should record the error on the current span", func(t *testing.T) {
			sr := oteltest.NewSpanRecorder(t)

			ctx, rootSpan := otel.Tracer("test").Start(t.Context(), "root")
			if test.withUser {
				userID := model.UserID("u_123")
				ctx = context.WithValue(ctx, gluehttp.ContextKey("userID"), &userID)
			}

			var called bool
			h := test.middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
			}))
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx))
			rootSpan.End()

			is.Equal(t, http.StatusInternalServerError, rec.Code)
			is.True(t, !called, "the handler should not run after the middleware fails")

			span := endedSpanNamed(t, sr, "root")
			is.Equal(t, codes.Error, span.Status().Code)

			var recorded bool
			for _, event := range span.Events() {
				if event.Name == "exception" {
					recorded = true
				}
			}
			is.True(t, recorded, "expected the error recorded as an exception event on the current span")
		})

		// The status description is not a safe place for this. otelhttp sets the span status from the
		// response code after the middleware has returned, and the SDK lets an equal status code overwrite,
		// so a 500 replaces the description with the empty string. The description has to reach the backend
		// on the exception event, which nothing above touches.
		t.Run(test.name+" should keep the error description under an otelhttp handler", func(t *testing.T) {
			sr := oteltest.NewSpanRecorder(t)

			ctx := t.Context()
			if test.withUser {
				userID := model.UserID("u_123")
				ctx = context.WithValue(ctx, gluehttp.ContextKey("userID"), &userID)
			}

			h := otelhttp.NewHandler(test.middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})), "GET /")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx))

			is.Equal(t, http.StatusInternalServerError, rec.Code)

			// The span name is otelhttp's to choose and is not what this test is about, so the span is
			// taken as the last one recorded rather than looked up by name.
			span := lastEndedSpan(t, sr)
			is.Equal(t, codes.Error, span.Status().Code)

			var message string
			for _, event := range span.Events() {
				if event.Name != "exception" {
					continue
				}
				for _, attr := range event.Attributes {
					if attr.Key == "exception.message" {
						message = attr.Value.AsString()
					}
				}
			}
			is.True(t, strings.Contains(message, test.expectDescription),
				"expected the exception message to carry "+test.expectDescription+", got "+message)
			is.True(t, strings.Contains(message, "oh no"),
				"expected the exception message to carry the underlying error, got "+message)
		})

		t.Run(test.name+" should not panic when there is no span to record on", func(t *testing.T) {
			oteltest.NewSpanRecorder(t)

			ctx := t.Context()
			if test.withUser {
				userID := model.UserID("u_123")
				ctx = context.WithValue(ctx, gluehttp.ContextKey("userID"), &userID)
			}

			h := test.middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx))

			is.Equal(t, http.StatusInternalServerError, rec.Code)
		})
	}
}

// TestMiddlewareTelemetryComposition pins where the middleware attributes land, in each of the two
// compositions the package supports. The middleware are exported, so running them under some other
// OpenTelemetry middleware instead of this package's own is a valid use of the public API, and the
// telemetry has to survive it rather than silently going nowhere.
func TestMiddlewareTelemetryComposition(t *testing.T) {
	tests := []struct {
		name        string
		middleware  func(pg *mockPermissionsGetter) gluehttp.Middleware
		durationKey attribute.Key
	}{
		{
			name: "Authorize",
			middleware: func(pg *mockPermissionsGetter) gluehttp.Middleware {
				return gluehttp.Authorize(slog.New(slog.DiscardHandler), pg, "read")
			},
			durationKey: "authz.duration_ms",
		},
		{
			name: "SavePermissionsInContext",
			middleware: func(pg *mockPermissionsGetter) gluehttp.Middleware {
				return gluehttp.SavePermissionsInContext(slog.New(slog.DiscardHandler), pg)
			},
			durationKey: "permissions.duration_ms",
		},
	}

	for _, test := range tests {
		// Only one permissions middleware per chain, since both write enduser.permissions and the second
		// would cover for a first which had stopped writing it.
		authenticate := func(next http.Handler) http.Handler {
			return gluehttp.Authenticate(slog.New(slog.DiscardHandler), &mockSessionManager{exists: true},
				&mockUserActiveChecker{active: true})(next)
		}

		assertAttributes := func(t *testing.T, attrs []attribute.KeyValue) {
			t.Helper()

			for _, key := range []attribute.Key{"authn.duration_ms", test.durationKey} {
				is.True(t, oteltest.HasAttributeKey(attrs, key), "expected "+string(key))
			}
			is.True(t, oteltest.HasAttribute(attrs, attribute.String("enduser.pseudo.id", "u_123")),
				"expected enduser.pseudo.id")
			is.True(t, oteltest.HasAttribute(attrs, attribute.StringSlice("enduser.permissions", []string{"read", "write"})),
				"expected enduser.permissions")
		}

		t.Run("Authenticate and "+test.name+" should record on the main span under OpenTelemetry", func(t *testing.T) {
			sr := oteltest.NewSpanRecorder(t)

			var called bool
			mux := chi.NewMux()
			mux.Use(gluehttp.OpenTelemetry)
			mux.Use(authenticate)
			mux.Use(test.middleware(&mockPermissionsGetter{permissions: []model.Permission{"read", "write"}}))
			mux.Get("/things/{id}", func(w http.ResponseWriter, r *http.Request) {
				called = true
			})

			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/things/42", nil))

			is.Equal(t, http.StatusOK, rec.Code)
			is.True(t, called)

			// Everything has to be on the one span marked main, which is the span a backend filters to.
			span := endedSpanNamed(t, sr, "GET /things/{id}")
			is.True(t, oteltest.HasAttribute(span.Attributes(), attribute.Bool("main", true)),
				"expected the attributes on the main span")
			assertAttributes(t, span.Attributes())
		})

		t.Run("Authenticate and "+test.name+" should record on the span of a plain otelhttp handler", func(t *testing.T) {
			sr := oteltest.NewSpanRecorder(t)

			var called bool
			var h http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
			})
			h = test.middleware(&mockPermissionsGetter{permissions: []model.Permission{"read", "write"}})(h)
			h = authenticate(h)
			h = otelhttp.NewHandler(h, "GET /")

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

			is.Equal(t, http.StatusOK, rec.Code)
			is.True(t, called)

			// The span name is otelhttp's to choose and is not what this test is about, so the span is
			// taken as the last one recorded rather than looked up by name.
			assertAttributes(t, lastEndedSpan(t, sr).Attributes())
		})
	}
}
