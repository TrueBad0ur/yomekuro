package db

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// GetBookTags returns tag names for a specific book.
func GetBookTags(ctx context.Context, pool *pgxpool.Pool, bookID [16]byte) ([]string, error) {
	rows, err := pool.Query(ctx,
		`SELECT t.name FROM tags t
		 JOIN book_tags bt ON bt.tag_id = t.id
		 WHERE bt.book_id = $1 ORDER BY t.name`, bookID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tags []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		tags = append(tags, name)
	}
	return tags, rows.Err()
}

// ListTags returns all tag names ordered alphabetically.
func ListTags(ctx context.Context, pool *pgxpool.Pool) ([]string, error) {
	rows, err := pool.Query(ctx, `SELECT name FROM tags ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tags []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		tags = append(tags, name)
	}
	return tags, rows.Err()
}

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
