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
	user, _ := userFromCtx(r)

	p, found, err := db.GetProgress(r.Context(), s.db, id, user.ID)
	if err != nil && err != pgx.ErrNoRows {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !found {
		respond(w, map[string]any{
			"book_id":        chi.URLParam(r, "id"),
			"spine_index":    0,
			"progression":    0,
			"percentage":     0,
			"bookmark_spine": nil,
			"bookmark_elem":  nil,
			"bookmark_start": nil,
			"bookmark_end":   nil,
		})
		return
	}
	respond(w, map[string]any{
		"book_id":        db.UUIDString(p.BookID),
		"spine_index":    p.SpineIndex,
		"progression":    p.Progression,
		"percentage":     p.Percentage,
		"bookmark_spine": p.BookmarkSpine,
		"bookmark_elem":  p.BookmarkElem,
		"bookmark_start": p.BookmarkStart,
		"bookmark_end":   p.BookmarkEnd,
		"updated_at":     p.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	})
}

func (s *Server) putProgress(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	user, _ := userFromCtx(r)

	var req struct {
		SpineIndex    int      `json:"spine_index"`
		Progression   float64  `json:"progression"`
		Percentage    float64  `json:"percentage"`
		BookmarkSpine *int     `json:"bookmark_spine"`
		BookmarkElem  *int     `json:"bookmark_elem"`
		BookmarkStart *int     `json:"bookmark_start"`
		BookmarkEnd   *int     `json:"bookmark_end"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	p := db.Progress{
		BookID:        id,
		UserID:        user.ID,
		SpineIndex:    req.SpineIndex,
		Progression:   req.Progression,
		Percentage:    req.Percentage,
		BookmarkSpine: req.BookmarkSpine,
		BookmarkElem:  req.BookmarkElem,
		BookmarkStart: req.BookmarkStart,
		BookmarkEnd:   req.BookmarkEnd,
	}
	if err := db.UpsertProgress(r.Context(), s.db, p); err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
