package db

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PersonalSeries struct {
	Name             string
	BookCount        int
	ReadCount        int
	Progress         float64
	LastReadAt       time.Time
	CoverBookID      [16]byte
	TargetBookID     [16]byte
	TargetBookTitle  string
	TargetProgress   float64
	TargetWasStarted bool
	Completed        bool
}

// ListPersonalSeries returns only series the user has interacted with. A
// series is complete when every currently present volume is marked read.
// For an unfinished series TargetBookID points to the most recently active
// volume, or the first unread volume when the previous one was completed.
func ListPersonalSeries(ctx context.Context, pool *pgxpool.Pool, userID [16]byte) ([]PersonalSeries, error) {
	rows, err := pool.Query(ctx, `
		WITH book_progress AS (
			SELECT
				b.id, b.series_name, b.series_index, b.title,
				COALESCE(rp.percentage, 0) AS percentage,
				rp.updated_at AS read_updated_at
			FROM books b
			LEFT JOIN reading_progress rp
				ON rp.book_id = b.id AND rp.user_id = $1
			WHERE b.series_name IS NOT NULL
			  AND b.series_name != ''
			  AND b.format != 'html'
		),
		series_progress AS (
			SELECT
				series_name,
				COUNT(*)::int AS book_count,
				COUNT(*) FILTER (WHERE percentage >= 1)::int AS read_count,
				AVG(LEAST(GREATEST(percentage, 0), 1)) AS progress,
				MAX(read_updated_at) AS last_read_at,
				BOOL_AND(percentage >= 1) AS completed
			FROM book_progress
			GROUP BY series_name
			HAVING COUNT(*) FILTER (WHERE percentage > 0) > 0
		)
		SELECT
			sp.series_name, sp.book_count, sp.read_count, sp.progress,
			sp.last_read_at, sp.completed,
			cover.id::text,
			COALESCE(target.id::text, ''),
			COALESCE(target.title, ''),
			COALESCE(target.percentage, 0),
			COALESCE(target.percentage > 0, false)
		FROM series_progress sp
		JOIN LATERAL (
			SELECT id
			FROM book_progress bp
			WHERE bp.series_name = sp.series_name
			ORDER BY bp.series_index NULLS LAST, bp.title
			LIMIT 1
		) cover ON true
		LEFT JOIN LATERAL (
			SELECT id, title, percentage
			FROM book_progress bp
			WHERE bp.series_name = sp.series_name
			  AND bp.percentage < 1
			ORDER BY
				CASE WHEN bp.percentage > 0 THEN 0 ELSE 1 END,
				CASE WHEN bp.percentage > 0 THEN bp.read_updated_at END DESC NULLS LAST,
				bp.series_index NULLS LAST,
				bp.title
			LIMIT 1
		) target ON NOT sp.completed
		ORDER BY sp.completed, sp.last_read_at DESC, sp.series_name`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []PersonalSeries
	for rows.Next() {
		var item PersonalSeries
		var coverID, targetID string
		if err := rows.Scan(
			&item.Name, &item.BookCount, &item.ReadCount, &item.Progress,
			&item.LastReadAt, &item.Completed, &coverID, &targetID,
			&item.TargetBookTitle, &item.TargetProgress, &item.TargetWasStarted,
		); err != nil {
			return nil, err
		}
		item.CoverBookID, err = ParseUUID(coverID)
		if err != nil {
			return nil, err
		}
		if targetID != "" {
			item.TargetBookID, err = ParseUUID(targetID)
			if err != nil {
				return nil, err
			}
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

type Progress struct {
	BookID        [16]byte
	UserID        [16]byte
	SpineIndex    int
	Progression   float64
	Percentage    float64
	UpdatedAt     time.Time
	BookmarkSpine *int
	BookmarkElem  *int
	BookmarkStart *int
	BookmarkEnd   *int
}

func GetProgress(ctx context.Context, pool *pgxpool.Pool, bookID, userID [16]byte) (Progress, bool, error) {
	var p Progress
	err := pool.QueryRow(ctx,
		`SELECT book_id, user_id, spine_index, progression, percentage,
		        bookmark_spine, bookmark_elem, bookmark_start, bookmark_end, updated_at
		 FROM reading_progress WHERE book_id = $1 AND user_id = $2`, bookID, userID,
	).Scan(&p.BookID, &p.UserID, &p.SpineIndex, &p.Progression, &p.Percentage,
		&p.BookmarkSpine, &p.BookmarkElem, &p.BookmarkStart, &p.BookmarkEnd, &p.UpdatedAt)
	if err == pgx.ErrNoRows {
		return Progress{}, false, nil
	}
	return p, err == nil, err
}

func UpsertProgress(ctx context.Context, pool *pgxpool.Pool, p Progress) error {
	_, err := pool.Exec(ctx, `
		INSERT INTO reading_progress
		    (book_id, user_id, spine_index, progression, percentage,
		     bookmark_spine, bookmark_elem, bookmark_start, bookmark_end)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (book_id, user_id) DO UPDATE SET
			spine_index    = EXCLUDED.spine_index,
			progression    = EXCLUDED.progression,
			percentage     = EXCLUDED.percentage,
			bookmark_spine = EXCLUDED.bookmark_spine,
			bookmark_elem  = EXCLUDED.bookmark_elem,
			bookmark_start = EXCLUDED.bookmark_start,
			bookmark_end   = EXCLUDED.bookmark_end,
			updated_at     = NOW()`,
		p.BookID, p.UserID, p.SpineIndex, p.Progression, p.Percentage,
		p.BookmarkSpine, p.BookmarkElem, p.BookmarkStart, p.BookmarkEnd,
	)
	return err
}

func GetProgressBatch(ctx context.Context, pool *pgxpool.Pool, userID [16]byte, bookIDs [][16]byte) (map[[16]byte]float64, error) {
	if len(bookIDs) == 0 {
		return nil, nil
	}
	rows, err := pool.Query(ctx,
		`SELECT book_id, percentage FROM reading_progress WHERE user_id = $1 AND book_id = ANY($2)`,
		userID, bookIDs,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[[16]byte]float64)
	for rows.Next() {
		var id [16]byte
		var pct float64
		if err := rows.Scan(&id, &pct); err != nil {
			return nil, err
		}
		result[id] = pct
	}
	return result, rows.Err()
}
