package api

import (
	"net/http"

	"github.com/truebad0ur/yomekuro/internal/sysstats"
)

// systemStatus serves the Settings → Server Status tab: a live snapshot plus
// recent history for the graph. Admin-only, like every other Settings feature.
func (s *Server) systemStatus(w http.ResponseWriter, r *http.Request) {
	respond(w, map[string]any{
		"latest":  s.sysStats.Latest(),
		"history": historyOrEmpty(s.sysStats.History()),
	})
}

func historyOrEmpty(h []sysstats.Sample) []sysstats.Sample {
	if h == nil {
		return []sysstats.Sample{}
	}
	return h
}
