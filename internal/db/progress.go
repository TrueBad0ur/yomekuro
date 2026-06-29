package db

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

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
