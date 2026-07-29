package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/truebad0ur/yomekuro/internal/db"
)

func categoryJSON(c db.UserCategory) map[string]any {
	items := make([]map[string]any, 0, len(c.Series))
	for _, item := range c.Series {
		items = append(items, map[string]any{
			"name": item.Name, "book_count": item.BookCount,
			"cover_url": fmt.Sprintf("/api/books/%s/cover", db.UUIDString(item.CoverBookID)),
		})
	}
	return map[string]any{
		"id": db.UUIDString(c.ID), "name": c.Name, "is_system": c.IsSystem, "items": items,
	}
}

func validCategoryName(name string) (string, bool) {
	name = strings.TrimSpace(name)
	n := len([]rune(name))
	return name, n > 0 && n <= 60
}

func (s *Server) listUserCategories(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromCtx(r)
	items, err := db.ListUserCategories(r.Context(), s.db, user.ID)
	if err != nil {
		respondInternal(w, "could not list categories", err)
		return
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, categoryJSON(item))
	}
	respond(w, map[string]any{"items": out})
}

func (s *Server) createUserCategory(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromCtx(r)
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	var ok bool
	req.Name, ok = validCategoryName(req.Name)
	if !ok {
		respondError(w, http.StatusBadRequest, "category name must be 1–60 characters")
		return
	}
	item, err := db.CreateUserCategory(r.Context(), s.db, user.ID, req.Name)
	if err != nil {
		respondError(w, http.StatusConflict, "category already exists")
		return
	}
	w.WriteHeader(http.StatusCreated)
	respond(w, categoryJSON(item))
}

func (s *Server) renameUserCategory(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	user, _ := userFromCtx(r)
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	req.Name, ok = validCategoryName(req.Name)
	if !ok {
		respondError(w, http.StatusBadRequest, "category name must be 1–60 characters")
		return
	}
	item, err := db.RenameUserCategory(r.Context(), s.db, id, user.ID, req.Name)
	if errors.Is(err, pgx.ErrNoRows) {
		respondError(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		respondError(w, http.StatusConflict, "category already exists")
		return
	}
	respond(w, categoryJSON(item))
}

func (s *Server) deleteUserCategory(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	user, _ := userFromCtx(r)
	deleted, err := db.DeleteUserCategory(r.Context(), s.db, id, user.ID)
	if err != nil {
		respondInternal(w, "could not delete category", err)
		return
	}
	if !deleted {
		respondError(w, http.StatusNotFound, "not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) setSeriesCategory(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	user, _ := userFromCtx(r)
	var req struct {
		SeriesName string `json:"series_name"`
		Included   bool   `json:"included"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	req.SeriesName = strings.TrimSpace(req.SeriesName)
	if req.SeriesName == "" || len([]rune(req.SeriesName)) > 500 {
		respondError(w, http.StatusBadRequest, "invalid series name")
		return
	}
	found, err := db.SetSeriesCategory(r.Context(), s.db, id, user.ID, req.SeriesName, req.Included)
	if errors.Is(err, pgx.ErrNoRows) || !found {
		respondError(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		respondInternal(w, "could not update category", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
