package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/truebad0ur/yomekuro/internal/auth"
	"github.com/truebad0ur/yomekuro/internal/db"
)

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) {
	users, err := auth.ListUsers(r.Context(), s.db)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	type userDTO struct {
		ID        string `json:"id"`
		Username  string `json:"username"`
		IsAdmin   bool   `json:"is_admin"`
		CreatedAt string `json:"created_at"`
	}
	dtos := make([]userDTO, len(users))
	for i, u := range users {
		dtos[i] = userDTO{
			ID:        db.UUIDString(u.ID),
			Username:  u.Username,
			IsAdmin:   u.IsAdmin,
			CreatedAt: u.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		}
	}
	respond(w, map[string]any{"items": dtos})
}

func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
		IsAdmin  bool   `json:"is_admin"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.Username == "" || req.Password == "" {
		respondError(w, http.StatusBadRequest, "username and password required")
		return
	}
	user, err := auth.CreateUser(r.Context(), s.db, req.Username, req.Password, req.IsAdmin)
	if err != nil {
		respondError(w, http.StatusConflict, "username already taken")
		return
	}
	w.WriteHeader(http.StatusCreated)
	respond(w, map[string]any{
		"id":       db.UUIDString(user.ID),
		"username": user.Username,
		"is_admin": user.IsAdmin,
	})
}

func (s *Server) updateUser(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	var req struct {
		Username *string `json:"username"`
		Password *string `json:"password"`
		IsAdmin  *bool   `json:"is_admin"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	username := ""
	if req.Username != nil {
		username = *req.Username
	}
	password := ""
	if req.Password != nil {
		password = *req.Password
	}
	if err := auth.UpdateUser(r.Context(), s.db, id, username, password, req.IsAdmin); err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteUser(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	// prevent self-deletion
	me, _ := userFromCtx(r)
	if me.ID == id {
		respondError(w, http.StatusBadRequest, "cannot delete yourself")
		return
	}
	if err := auth.DeleteUser(r.Context(), s.db, id); err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
