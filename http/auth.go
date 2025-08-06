package http

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"slices"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	semconv "go.opentelemetry.io/otel/semconv/v1.34.0"
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
	tracer := otel.Tracer("maragu.dev/glue/http")

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, span := tracer.Start(r.Context(), "Authenticate")
			defer span.End()
			r = r.WithContext(ctx)

			// If there is no session, do nothing and call the next handler
			if !sgd.Exists(ctx, SessionUserIDKey) {
				next.ServeHTTP(w, r)
				return
			}

			// Get the user from the database, and destroy the session if the user is not found
			userID := model.UserID(sgd.GetString(ctx, SessionUserIDKey))
			active, err := uac.IsUserActive(ctx, userID)
			if err != nil {
				if errors.Is(err, model.ErrorUserNotFound) {
					if err := sgd.Destroy(ctx); err != nil {
						log.Info("Error destroying session for nonexistent user", "error", err, "userID", userID)
						http.Error(w, "error destroying session after authentication", http.StatusInternalServerError)
						return
					}

					// The invalid session is destroyed, and the request can continue
					next.ServeHTTP(w, r)
					return
				}

				log.Info("Error getting user after authentication", "error", err, "userID", userID)
				http.Error(w, "error getting user after authentication", http.StatusInternalServerError)
				return
			}

			// Destroy the session if the user is not active, but continue processing the request
			if !active {
				if err := sgd.Destroy(ctx); err != nil {
					log.Info("Error destroying session for inactive user", "error", err, "userID", userID)
					http.Error(w, "error destroying session after authentication", http.StatusInternalServerError)
					return
				}

				next.ServeHTTP(w, r)
				return
			}

			// Add user ID to the root span
			if rootSpan := GetRootSpanFromContext(ctx); rootSpan != nil && rootSpan.IsRecording() {
				rootSpan.SetAttributes(semconv.EnduserPseudoID(string(userID)))
			}

			// Store the user directly in the request context instead of having to use the session manager
			ctx = context.WithValue(ctx, contextUserIDKey, &userID)
			next.ServeHTTP(w, r.WithContext(ctx))
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
	tracer := otel.Tracer("maragu.dev/glue/http")

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, span := tracer.Start(r.Context(), "Authorize")
			defer span.End()
			r = r.WithContext(ctx)

			userID := GetUserIDFromContext(ctx)

			if userID == nil {
				http.Redirect(w, r, "/login?redirect="+url.QueryEscape(r.URL.Path), http.StatusTemporaryRedirect)
				return
			}

			permissions, err := pg.GetPermissions(ctx, *userID)
			if err != nil {
				log.Info("Error getting permissions", "error", err, "userID", userID)
				http.Error(w, "error getting permissions", http.StatusInternalServerError)
				return
			}

			// Add permissions to the root span
			if rootSpan := GetRootSpanFromContext(ctx); rootSpan != nil && rootSpan.IsRecording() {
				permissionStrings := make([]string, len(permissions))
				for _, p := range permissions {
					permissionStrings = append(permissionStrings, string(p))
				}
				rootSpan.SetAttributes(attribute.StringSlice("enduser.permissions", permissionStrings))
			}

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

			next.ServeHTTP(w, r)
		})
	}
}

type permissionsGetter interface {
	GetPermissions(ctx context.Context, id model.UserID) ([]model.Permission, error)
}

func SavePermissionsInContext(log *slog.Logger, pg permissionsGetter) Middleware {
	tracer := otel.Tracer("maragu.dev/glue/http")

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, span := tracer.Start(r.Context(), "SavePermissionsInContext")
			defer span.End()
			r = r.WithContext(ctx)

			userID := GetUserIDFromContext(ctx)

			if userID == nil {
				next.ServeHTTP(w, r)
				return
			}

			permissions, err := pg.GetPermissions(ctx, *userID)
			if err != nil {
				log.Error("Error getting permissions", "error", err, "userID", userID)
				http.Error(w, "error getting permissions", http.StatusInternalServerError)
				return
			}

			ctx = context.WithValue(ctx, contextPermissionsKey, permissions)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
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
			log.Error("Error logging out", "error", err)
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
