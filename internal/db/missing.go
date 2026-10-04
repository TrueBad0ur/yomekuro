package db

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type MissingItem struct {
	ID          [16]byte
	Category    string
	SeriesName  string
	Description string
	Resolved    bool
	CreatedAt   time.Time
}

func ListMissingItems(ctx context.Context, pool *pgxpool.Pool) ([]MissingItem, error) {
	rows, err := pool.Query(ctx, `
		SELECT id, category, series_name, description, resolved, created_at
		FROM missing_items
		ORDER BY category, resolved, created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []MissingItem
	for rows.Next() {
		var m MissingItem
		if err := rows.Scan(&m.ID, &m.Category, &m.SeriesName, &m.Description, &m.Resolved, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func CreateMissingItem(ctx context.Context, pool *pgxpool.Pool, category, seriesName, description string) (MissingItem, error) {
	var m MissingItem
	err := pool.QueryRow(ctx, `
		INSERT INTO missing_items (category, series_name, description)
		VALUES ($1, $2, $3)
		RETURNING id, category, series_name, description, resolved, created_at`,
		category, seriesName, description,
	).Scan(&m.ID, &m.Category, &m.SeriesName, &m.Description, &m.Resolved, &m.CreatedAt)
	return m, err
}

func SetMissingItemResolved(ctx context.Context, pool *pgxpool.Pool, id [16]byte, resolved bool) error {
	_, err := pool.Exec(ctx, `UPDATE missing_items SET resolved = $2 WHERE id = $1`, id, resolved)
	return err
}

func DeleteMissingItem(ctx context.Context, pool *pgxpool.Pool, id [16]byte) error {
	_, err := pool.Exec(ctx, `DELETE FROM missing_items WHERE id = $1`, id)
	return err
}
