package middleware

import (
	"context"
	"net/http"

	"github.com/emilhauk/chat/internal/auth"
	"github.com/emilhauk/chat/internal/model"
	redisclient "github.com/emilhauk/chat/internal/redis"
)

type contextKey string

const UserContextKey contextKey = "user"

// RequireAuth validates the session cookie and injects the User into the request
// context. Unauthenticated requests are redirected to /login.
func RequireAuth(redis *redisclient.Client, secret []byte) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, err := resolveUser(r, redis, secret)
			if err != nil || user == nil {
				http.Redirect(w, r, "/login", http.StatusFound)
				return
			}
			ctx := context.WithValue(r.Context(), UserContextKey, user)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// UserFromContext retrieves the authenticated user from the context. Returns nil
// if not present.
func UserFromContext(ctx context.Context) *model.User {
	u, _ := ctx.Value(UserContextKey).(*model.User)
	return u
}

func resolveUser(r *http.Request, redis *redisclient.Client, secret []byte) (*model.User, error) {
	token, err := auth.TokenFromRequest(r, secret)
	if err != nil {
		return nil, err
	}
	return redis.GetSession(r.Context(), token)
}
