package db

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// SetBookTags replaces all tags for a book with the given names.
// Tags are created if they don't exist. Passing an empty slice clears all tags.
func SetBookTags(ctx context.Context, pool *pgxpool.Pool, bookID [16]byte, names []string) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// Clear existing tags for this book.
	if _, err := tx.Exec(ctx, `DELETE FROM book_tags WHERE book_id = $1`, bookID); err != nil {
		return err
	}

	for _, name := range names {
		if name == "" {
			continue
		}
		// Upsert tag, get id.
		var tagID [16]byte
		err := tx.QueryRow(ctx,
			`INSERT INTO tags (name) VALUES ($1)
			 ON CONFLICT (name) DO UPDATE SET name = EXCLUDED.name
			 RETURNING id`,
			name,
		).Scan(&tagID)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO book_tags (book_id, tag_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
			bookID, tagID,
		); err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}
