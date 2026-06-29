package db

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Progress struct {
	BookID      [16]byte
	SpineIndex  int
	Progression float64
	Percentage  float64
	UpdatedAt   time.Time
}

func GetProgress(ctx context.Context, pool *pgxpool.Pool, bookID [16]byte) (Progress, bool, error) {
	var p Progress
	err := pool.QueryRow(ctx,
		`SELECT book_id, spine_index, progression, percentage, updated_at
		 FROM reading_progress WHERE book_id = $1`, bookID,
	).Scan(&p.BookID, &p.SpineIndex, &p.Progression, &p.Percentage, &p.UpdatedAt)
	if err == pgx.ErrNoRows {
		return Progress{}, false, nil
	}
	return p, err == nil, err
}

func UpsertProgress(ctx context.Context, pool *pgxpool.Pool, p Progress) error {
	_, err := pool.Exec(ctx, `
		INSERT INTO reading_progress (book_id, spine_index, progression, percentage)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (book_id) DO UPDATE SET
			spine_index  = EXCLUDED.spine_index,
			progression  = EXCLUDED.progression,
			percentage   = EXCLUDED.percentage,
			updated_at   = NOW()`,
		p.BookID, p.SpineIndex, p.Progression, p.Percentage,
	)
	return err
}
