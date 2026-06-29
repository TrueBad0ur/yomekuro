package api

import (
	"context"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"

	"github.com/truebad0ur/yomekuro/internal/auth"
)

type contextKey string

const ctxUser contextKey = "user"

func userFromCtx(r *http.Request) (auth.User, bool) {
	u, ok := r.Context().Value(ctxUser).(auth.User)
	return u, ok
}

func (s *Server) authRequired(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := sessionToken(r)
		if token == "" {
			respondError(w, http.StatusUnauthorized, "not authenticated")
			return
		}
		user, err := auth.GetUserBySession(r.Context(), s.db, token)
		if err != nil {
			respondError(w, http.StatusUnauthorized, "not authenticated")
			return
		}
		ctx := context.WithValue(r.Context(), ctxUser, user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) adminRequired(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, ok := userFromCtx(r)
		if !ok || !user.IsAdmin {
			respondError(w, http.StatusForbidden, "forbidden")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func sessionToken(r *http.Request) string {
	if c, err := r.Cookie("session"); err == nil && c.Value != "" {
		return c.Value
	}
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimPrefix(h, "Bearer ")
	}
	return ""
}

func parseUUID(s string) ([16]byte, error) {
	clean := strings.ReplaceAll(s, "-", "")
	b, err := hex.DecodeString(clean)
	if err != nil || len(b) != 16 {
		return [16]byte{}, fmt.Errorf("invalid UUID: %q", s)
	}
	var u [16]byte
	copy(u[:], b)
	return u, nil
}
