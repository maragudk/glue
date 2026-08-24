package http

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"time"

	"go.opentelemetry.io/otel/attribute"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"
	g "maragu.dev/gomponents"

	"maragu.dev/glue/html"
	"maragu.dev/glue/model"
)

const contextUserIDKey = ContextKey("userID")
const contextPermissionsKey = ContextKey("permissions")

const SessionUserIDKey = "userID"

type sessionDestroyer interface {
	Destroy(ctx context.Context) error
}

type sessionGetter interface {
	Exists(ctx context.Context, key string) bool
	GetString(ctx context.Context, key string) string
}

type sessionGetterDestroyer interface {
	sessionDestroyer
	sessionGetter
}

type userActiveChecker interface {
	IsUserActive(ctx context.Context, id model.UserID) (bool, error)
}

// Authenticate is [Middleware] to authenticate users.
// After authentication, the user ID is stored in the request context, and can be retrieved using [GetUserIDFromContext].
// If there is no session, the middleware does nothing and just calls the next handler.
// If there is no user (anymore) but the ID is in the session, or the user is inactive, the middleware destroys the session and calls the next handler.
func Authenticate(log *slog.Logger, sgd sessionGetterDestroyer, uac userActiveChecker) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()

			// The clock stops where this hands off, so the measurement covers this middleware's own
			// work and never the handlers below it. Paths which never hand off leave it unset, and the
			// defer stamps the moment it fires, which for those is where the work ended.
			start := time.Now()
			var stop time.Time
			defer func() {
				if stop.IsZero() {
					stop = time.Now()
				}
				setSpanDuration(ctx, "authn.duration_ms", stop.Sub(start))
			}()

			// If there is no session, do nothing and call the next handler
			if !sgd.Exists(ctx, SessionUserIDKey) {
				stop = time.Now()
				next.ServeHTTP(w, r)
				return
			}

			// Get the user from the database, and destroy the session if the user is not found
			userID := model.UserID(sgd.GetString(ctx, SessionUserIDKey))
			active, err := uac.IsUserActive(ctx, userID)
			if err != nil {
				if errors.Is(err, model.ErrorUserNotFound) {
					if err := sgd.Destroy(ctx); err != nil {
						log.InfoContext(ctx, "Error destroying session for nonexistent user", "error", err, "userID", userID)
						recordErrorOnSpan(ctx, err, "error destroying session after authentication")
						http.Error(w, "error destroying session after authentication", http.StatusInternalServerError)
						return
					}

					// The invalid session is destroyed, and the request can continue
					stop = time.Now()
					next.ServeHTTP(w, r)
					return
				}

				log.InfoContext(ctx, "Error getting user after authentication", "error", err, "userID", userID)
				recordErrorOnSpan(ctx, err, "error getting user after authentication")
				http.Error(w, "error getting user after authentication", http.StatusInternalServerError)
				return
			}

			// Destroy the session if the user is not active, but continue processing the request
			if !active {
				if err := sgd.Destroy(ctx); err != nil {
					log.InfoContext(ctx, "Error destroying session for inactive user", "error", err, "userID", userID)
					recordErrorOnSpan(ctx, err, "error destroying session after authentication")
					http.Error(w, "error destroying session after authentication", http.StatusInternalServerError)
					return
				}

				stop = time.Now()
				next.ServeHTTP(w, r)
				return
			}

			if span := trace.SpanFromContext(ctx); span.IsRecording() {
				span.SetAttributes(semconv.EnduserPseudoID(string(userID)))
			}

			// Store the user directly in the request context instead of having to use the session manager
			stop = time.Now()
			next.ServeHTTP(w, r.WithContext(context.WithValue(ctx, contextUserIDKey, &userID)))
		})
	}
}

// GetUserIDFromContext, which may be nil if the user is not authenticated.
func GetUserIDFromContext(ctx context.Context) *model.UserID {
	id := ctx.Value(contextUserIDKey)
	if id == nil {
		return nil
	}

	return id.(*model.UserID)
}

func Authorize(log *slog.Logger, pg permissionsGetter, requiredPermissions ...model.Permission) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()

			// The clock stops where this hands off, so the measurement covers this middleware's own
			// work and never the handlers below it. Paths which never hand off leave it unset, and the
			// defer stamps the moment it fires, which for those is where the work ended.
			start := time.Now()
			var stop time.Time
			defer func() {
				if stop.IsZero() {
					stop = time.Now()
				}
				setSpanDuration(ctx, "authz.duration_ms", stop.Sub(start))
			}()

			userID := GetUserIDFromContext(ctx)

			if userID == nil {
				http.Redirect(w, r, "/login?redirect="+url.QueryEscape(r.URL.Path), http.StatusTemporaryRedirect)
				return
			}

			permissions, err := pg.GetPermissions(ctx, *userID)
			if err != nil {
				log.InfoContext(ctx, "Error getting permissions", "error", err, "userID", userID)
				recordErrorOnSpan(ctx, err, "error getting permissions")
				http.Error(w, "error getting permissions", http.StatusInternalServerError)
				return
			}

			setPermissionsOnSpan(ctx, permissions)

			hasRequiredPermissions := true
			for _, requiredPermission := range requiredPermissions {
				if !slices.Contains(permissions, requiredPermission) {
					hasRequiredPermissions = false
					break
				}
			}

			if !hasRequiredPermissions {
				http.Error(w, "unauthorized", http.StatusForbidden)
				return
			}

			stop = time.Now()
			next.ServeHTTP(w, r)
		})
	}
}

type permissionsGetter interface {
	GetPermissions(ctx context.Context, id model.UserID) ([]model.Permission, error)
}

func SavePermissionsInContext(log *slog.Logger, pg permissionsGetter) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()

			// The clock stops where this hands off, so the measurement covers this middleware's own
			// work and never the handlers below it. Paths which never hand off leave it unset, and the
			// defer stamps the moment it fires, which for those is where the work ended.
			start := time.Now()
			var stop time.Time
			defer func() {
				if stop.IsZero() {
					stop = time.Now()
				}
				setSpanDuration(ctx, "permissions.duration_ms", stop.Sub(start))
			}()

			userID := GetUserIDFromContext(ctx)

			if userID == nil {
				stop = time.Now()
				next.ServeHTTP(w, r)
				return
			}

			permissions, err := pg.GetPermissions(ctx, *userID)
			if err != nil {
				log.ErrorContext(ctx, "Error getting permissions", "error", err, "userID", userID)
				recordErrorOnSpan(ctx, err, "error getting permissions")
				http.Error(w, "error getting permissions", http.StatusInternalServerError)
				return
			}

			setPermissionsOnSpan(ctx, permissions)

			stop = time.Now()
			next.ServeHTTP(w, r.WithContext(context.WithValue(ctx, contextPermissionsKey, permissions)))
		})
	}
}

// setPermissionsOnSpan as the enduser.permissions attribute on the current span in the context, if it is
// recording. See [setSpanDuration] for which span that is.
func setPermissionsOnSpan(ctx context.Context, permissions []model.Permission) {
	span := trace.SpanFromContext(ctx)
	if !span.IsRecording() {
		return
	}

	permissionStrings := make([]string, 0, len(permissions))
	for _, p := range permissions {
		permissionStrings = append(permissionStrings, string(p))
	}

	span.SetAttributes(attribute.StringSlice("enduser.permissions", permissionStrings))
}

func GetPermissionsFromContext(ctx context.Context) []model.Permission {
	permissions := ctx.Value(contextPermissionsKey)
	if permissions == nil {
		return nil
	}
	return permissions.([]model.Permission)
}

// Logout creates an http.Handler for logging out.
// It just destroys the current user session.
func Logout(r *Router, log *slog.Logger, sd sessionDestroyer, page html.PageFunc) {
	r.Post("/logout", func(props html.PageProps) (g.Node, error) {
		redirect := props.R.URL.Query().Get("redirect")
		if redirect == "" {
			redirect = "/"
		}

		userID := GetUserIDFromContext(props.Ctx)
		if userID == nil {
			http.Redirect(props.W, props.R, redirect, http.StatusFound)
			return nil, nil
		}

		if err := sd.Destroy(props.Ctx); err != nil {
			log.ErrorContext(props.Ctx, "Error logging out", "error", err)
			return html.ErrorPage(page), err
		}

		http.Redirect(props.W, props.R, redirect, http.StatusFound)

		return nil, nil
	})
}

func RedirectIfAuthenticated(redirect string) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			userID := GetUserIDFromContext(r.Context())

			if userID != nil {
				http.Redirect(w, r, redirect, http.StatusTemporaryRedirect)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
