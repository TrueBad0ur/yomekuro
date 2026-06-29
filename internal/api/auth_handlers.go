package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/truebad0ur/yomekuro/internal/auth"
	"github.com/truebad0ur/yomekuro/internal/db"
)

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

	user, hash, err := auth.GetUserByUsername(r.Context(), s.db, req.Username)
	if err != nil {
		respondError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	if !auth.CheckPassword(hash, req.Password) {
		respondError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	token, err := auth.CreateSession(r.Context(), s.db, user.ID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "session error")
		return
	}

	setSessionCookie(w, token)
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

	setSessionCookie(w, token)
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
		Name:    "session",
		Value:   "",
		Path:    "/",
		MaxAge:  -1,
		Expires: time.Unix(0, 0),
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

func setSessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     "session",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(auth.SessionDuration.Seconds()),
	})
}
