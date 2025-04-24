package http

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"maragu.dev/gloo/model"
)

const contextUserKey = contextKey("user")

const sessionUserIDKey = "userID"

type sessionDestroyer interface {
	Destroy(ctx context.Context) error
}

type sessionGetter interface {
	sessionDestroyer
	Exists(ctx context.Context, key string) bool
	GetString(ctx context.Context, key string) string
}

type userGetter interface {
	GetUser(ctx context.Context, id model.ID) (model.User, error)
}

// Authenticate is [Middleware] to authenticate users.
// After authentication, the user is stored directly in the request context, and can be retrieved using [GetUserFromContext].
// If there is no session, the middleware does nothing.
// If there is no user, or the user is inactive, the middleware destroys the session.
func Authenticate(sg sessionGetter, db userGetter, log *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// If there is no session, do nothing and return
			if !sg.Exists(r.Context(), sessionUserIDKey) {
				next.ServeHTTP(w, r)
				return
			}

			// Get the user from the database, and destroy the session if the user is not found
			userID := model.ID(sg.GetString(r.Context(), sessionUserIDKey))
			user, err := db.GetUser(r.Context(), userID)
			if err != nil {
				if errors.Is(err, model.ErrorUserNotFound) {
					if err := sg.Destroy(r.Context()); err != nil {
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
			if !user.Active {
				if err := sg.Destroy(r.Context()); err != nil {
					log.Info("Error destroying session for inactive user", "error", err, "userID", userID)
					http.Error(w, "error destroying session after authentication", http.StatusInternalServerError)
					return
				}

				next.ServeHTTP(w, r)
				return
			}

			// Store the user directly in the request context instead of having to use the session manager
			ctx := context.WithValue(r.Context(), contextUserKey, &user)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// GetUserFromContext, which may be nil if the user is not authenticated.
func GetUserFromContext(ctx context.Context) *model.User {
	user := ctx.Value(contextUserKey)
	if user == nil {
		return nil
	}

	return user.(*model.User)
}
