package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/truebad0ur/yomekuro/internal/db"
)

type missingItemDTO struct {
	ID          string `json:"id"`
	Category    string `json:"category"`
	SeriesName  string `json:"series_name"`
	Description string `json:"description"`
	Resolved    bool   `json:"resolved"`
	CreatedAt   string `json:"created_at"`
}

func toMissingItemDTO(m db.MissingItem) missingItemDTO {
	return missingItemDTO{
		ID:          db.UUIDString(m.ID),
		Category:    m.Category,
		SeriesName:  m.SeriesName,
		Description: m.Description,
		Resolved:    m.Resolved,
		CreatedAt:   m.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
}

func (s *Server) listMissingItems(w http.ResponseWriter, r *http.Request) {
	items, err := db.ListMissingItems(r.Context(), s.db)
	if err != nil {
		respondInternal(w, "internal error", err)
		return
	}
	dtos := make([]missingItemDTO, len(items))
	for i, m := range items {
		dtos[i] = toMissingItemDTO(m)
	}
	respond(w, map[string]any{"items": dtos})
}

func (s *Server) createMissingItem(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Category    string `json:"category"`
		SeriesName  string `json:"series_name"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.Category != "cover" && req.Category != "volume" && req.Category != "page" {
		respondError(w, http.StatusBadRequest, "category must be cover, volume, or page")
		return
	}
	if req.SeriesName == "" || req.Description == "" {
		respondError(w, http.StatusBadRequest, "series_name and description are required")
		return
	}
	m, err := db.CreateMissingItem(r.Context(), s.db, req.Category, req.SeriesName, req.Description)
	if err != nil {
		respondInternal(w, "internal error", err)
		return
	}
	w.WriteHeader(http.StatusCreated)
	respond(w, toMissingItemDTO(m))
}

func (s *Server) updateMissingItem(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	var req struct {
		Resolved bool `json:"resolved"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if err := db.SetMissingItemResolved(r.Context(), s.db, id, req.Resolved); err != nil {
		respondInternal(w, "internal error", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteMissingItem(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	if err := db.DeleteMissingItem(r.Context(), s.db, id); err != nil {
		respondInternal(w, "internal error", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
