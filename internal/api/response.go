package api

import (
	"encoding/json"
	"net/http"
)

func respond(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func respondError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
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
