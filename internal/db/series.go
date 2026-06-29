package db

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Series struct {
	Name        string
	BookCount   int
	CoverBookID [16]byte
}

func ListSeries(ctx context.Context, pool *pgxpool.Pool, libraryID string) ([]Series, error) {
	where := "series_name != '' AND series_name IS NOT NULL"
	args := []any{}

	if libraryID != "" {
		if id, err := ParseUUID(libraryID); err == nil {
			where += " AND library_id = $1"
			args = append(args, id)
		}
	}

	rows, err := pool.Query(ctx, `
		SELECT
			series_name,
			COUNT(*) AS book_count,
			(SELECT id FROM books b2
			 WHERE b2.series_name = b.series_name
			 ORDER BY series_index NULLS LAST LIMIT 1) AS cover_book_id
		FROM books b
		WHERE `+where+`
		GROUP BY series_name
		ORDER BY series_name`,
		args...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Series
	for rows.Next() {
		var s Series
		if err := rows.Scan(&s.Name, &s.BookCount, &s.CoverBookID); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
