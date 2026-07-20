package api

import (
	"encoding/json"
	"log/slog"
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

// respondInternal answers with a neutral 500 message and logs the real error
// server-side — the error text itself (pgx query details, filesystem paths
// inside the container) must never reach the client.
func respondInternal(w http.ResponseWriter, msg string, err error) {
	slog.Error(msg, "err", err)
	respondError(w, http.StatusInternalServerError, msg)
}

func parseID(w http.ResponseWriter, idStr string) ([16]byte, bool) {
	id, err := parseUUID(idStr)
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid id")
		return [16]byte{}, false
	}
	return id, true
}
