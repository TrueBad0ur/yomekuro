package api

import (
	"encoding/json"
	"net/http"
)

// no-store: every JSON response here is scoped to the caller's own session.
func respond(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(v)
}

func respondError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func parseID(w http.ResponseWriter, idStr string) ([16]byte, bool) {
	id, err := parseUUID(idStr)
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid id")
		return [16]byte{}, false
	}
	return id, true
}
