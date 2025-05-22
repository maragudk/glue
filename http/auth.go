package http

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"

	"maragu.dev/glue/model"
)

const contextUserIDKey = ContextKey("userID")

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
			// If there is no session, do nothing and call the next handler
			if !sgd.Exists(r.Context(), SessionUserIDKey) {
				next.ServeHTTP(w, r)
				return
			}

			// Get the user from the database, and destroy the session if the user is not found
			userID := model.UserID(sgd.GetString(r.Context(), SessionUserIDKey))
			active, err := uac.IsUserActive(r.Context(), userID)
			if err != nil {
				if errors.Is(err, model.ErrorUserNotFound) {
					if err := sgd.Destroy(r.Context()); err != nil {
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
				if err := sgd.Destroy(r.Context()); err != nil {
					log.Info("Error destroying session for inactive user", "error", err, "userID", userID)
					http.Error(w, "error destroying session after authentication", http.StatusInternalServerError)
					return
				}

				next.ServeHTTP(w, r)
				return
			}

			// Store the user directly in the request context instead of having to use the session manager
			ctx := context.WithValue(r.Context(), contextUserIDKey, &userID)
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

type permissionsChecker interface {
	HasPermissions(ctx context.Context, id model.UserID, permissions []model.Permission) (bool, error)
}

func Authorize(log *slog.Logger, pc permissionsChecker, permissions ...model.Permission) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			userID := GetUserIDFromContext(r.Context())

			if userID == nil {
				http.Redirect(w, r, "/login?redirect="+url.QueryEscape(r.URL.Path), http.StatusTemporaryRedirect)
				return
			}

			hasPermissions, err := pc.HasPermissions(r.Context(), *userID, permissions)
			if err != nil {
				log.Info("Error checking permissions", "error", err, "userID", userID, "permissions", permissions)
				http.Error(w, "error checking permissions", http.StatusInternalServerError)
				return
			}

			if !hasPermissions {
				http.Error(w, "unauthorized", http.StatusForbidden)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
