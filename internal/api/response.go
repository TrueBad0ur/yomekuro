package api

import (
	"encoding/json"
	"net/http"
)

// no-store, not just no-cache: every JSON response here is scoped to the
// caller's own session (book lists, progress, jobs, ...) — a shared proxy/CDN
// in front of this server must never be allowed to cache and replay one
// user's response to a different, possibly unauthenticated, request.
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
