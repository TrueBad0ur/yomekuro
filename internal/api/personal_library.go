package api

import (
	"fmt"
	"net/http"

	"github.com/truebad0ur/yomekuro/internal/db"
)

func (s *Server) personalLibrary(w http.ResponseWriter, r *http.Request) {
	user, ok := userFromCtx(r)
	if !ok {
		respondError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	series, err := db.ListPersonalSeries(r.Context(), s.db, user.ID)
	if err != nil {
		respondInternal(w, "internal error", err)
		return
	}

	type personalSeriesDTO struct {
		Name             string  `json:"name"`
		BookCount        int     `json:"book_count"`
		ReadCount        int     `json:"read_count"`
		ProgressPct      float64 `json:"progress_pct"`
		LastReadAt       string  `json:"last_read_at"`
		CoverURL         string  `json:"cover_url"`
		TargetBookID     string  `json:"target_book_id,omitempty"`
		TargetBookTitle  string  `json:"target_book_title,omitempty"`
		TargetProgress   float64 `json:"target_progress,omitempty"`
		TargetWasStarted bool    `json:"target_was_started,omitempty"`
		Completed        bool    `json:"completed"`
	}

	items := make([]personalSeriesDTO, len(series))
	for i, item := range series {
		items[i] = personalSeriesDTO{
			Name:             item.Name,
			BookCount:        item.BookCount,
			ReadCount:        item.ReadCount,
			ProgressPct:      item.Progress,
			LastReadAt:       item.LastReadAt.UTC().Format("2006-01-02T15:04:05Z"),
			CoverURL:         fmt.Sprintf("/api/books/%s/cover", db.UUIDString(item.CoverBookID)),
			TargetBookTitle:  item.TargetBookTitle,
			TargetProgress:   item.TargetProgress,
			TargetWasStarted: item.TargetWasStarted,
			Completed:        item.Completed,
		}
		if item.TargetBookID != ([16]byte{}) {
			items[i].TargetBookID = db.UUIDString(item.TargetBookID)
		}
	}
	respond(w, map[string]any{"items": items})
}
