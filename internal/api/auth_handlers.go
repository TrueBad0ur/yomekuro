package api

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/truebad0ur/yomekuro/internal/auth"
	"github.com/truebad0ur/yomekuro/internal/db"
)

// dummyPasswordHash lets login run bcrypt.CompareHashAndPassword even when
// the username doesn't exist, so the response time for "wrong password" and
// "no such user" doesn't leak which one happened (a real hash of an
// unguessable, unused string — never a valid login on its own).
var dummyPasswordHash, _ = auth.HashPassword("yomekuro-timing-mitigation-not-a-real-password")

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.Username == "" || req.Password == "" {
		respondError(w, http.StatusBadRequest, "username and password required")
		return
	}

	limitKey := clientIP(r) + ":" + req.Username
	if s.loginLimiter.blocked(limitKey) {
		respondError(w, http.StatusTooManyRequests, "too many failed login attempts, try again later")
		return
	}

	user, hash, err := auth.GetUserByUsername(r.Context(), s.db, req.Username)
	if err != nil {
		// Run a real bcrypt compare against a dummy hash even though there's
		// no such user, so this branch takes roughly as long as the
		// wrong-password branch below — see dummyPasswordHash's comment.
		auth.CheckPassword(dummyPasswordHash, req.Password)
		s.loginLimiter.recordFailure(limitKey)
		respondError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	if !auth.CheckPassword(hash, req.Password) {
		s.loginLimiter.recordFailure(limitKey)
		respondError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	s.loginLimiter.reset(limitKey)

	token, err := auth.CreateSession(r.Context(), s.db, user.ID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "session error")
		return
	}

	setSessionCookie(w, r, token)
	respond(w, map[string]any{
		"id":       db.UUIDString(user.ID),
		"username": user.Username,
		"is_admin": user.IsAdmin,
	})
}

func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.Username == "" || req.Password == "" {
		respondError(w, http.StatusBadRequest, "username and password required")
		return
	}
	if len(req.Password) < auth.MinPasswordLength {
		respondError(w, http.StatusBadRequest, fmt.Sprintf("password must be at least %d characters", auth.MinPasswordLength))
		return
	}
	user, err := auth.CreateUser(r.Context(), s.db, req.Username, req.Password, false)
	if err != nil {
		respondError(w, http.StatusConflict, "username already taken")
		return
	}

	token, err := auth.CreateSession(r.Context(), s.db, user.ID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "session error")
		return
	}

	setSessionCookie(w, r, token)
	w.WriteHeader(http.StatusCreated)
	respond(w, map[string]any{
		"id":       db.UUIDString(user.ID),
		"username": user.Username,
		"is_admin": user.IsAdmin,
	})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	token := sessionToken(r)
	if token != "" {
		_ = auth.DeleteSession(r.Context(), s.db, token)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "session",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   isHTTPS(r),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
		Expires:  time.Unix(0, 0),
	})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	user, ok := userFromCtx(r)
	if !ok {
		respondError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	respond(w, map[string]any{
		"id":       db.UUIDString(user.ID),
		"username": user.Username,
		"is_admin": user.IsAdmin,
	})
}

func setSessionCookie(w http.ResponseWriter, r *http.Request, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     "session",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   isHTTPS(r),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(auth.SessionDuration.Seconds()),
	})
}

// Whether the request arrived over TLS, directly or via a proxy — so the session
// cookie gets Secure without breaking logins on plain HTTP (there's no TLS here).
func isHTTPS(r *http.Request) bool {
	return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}
