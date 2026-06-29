package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/truebad0ur/yomekuro/internal/db"
)

func (s *Server) getProgress(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	p, found, err := db.GetProgress(r.Context(), s.db, id)
	if err != nil && err != pgx.ErrNoRows {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !found {
		respond(w, map[string]any{
			"book_id":     chi.URLParam(r, "id"),
			"spine_index": 0,
			"progression": 0,
			"percentage":  0,
		})
		return
	}
	respond(w, map[string]any{
		"book_id":     db.UUIDString(p.BookID),
		"spine_index": p.SpineIndex,
		"progression": p.Progression,
		"percentage":  p.Percentage,
		"updated_at":  p.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	})
}

func (s *Server) putProgress(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	var req struct {
		SpineIndex  int     `json:"spine_index"`
		Progression float64 `json:"progression"`
		Percentage  float64 `json:"percentage"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	p := db.Progress{
		BookID:      id,
		SpineIndex:  req.SpineIndex,
		Progression: req.Progression,
		Percentage:  req.Percentage,
	}
	if err := db.UpsertProgress(r.Context(), s.db, p); err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
