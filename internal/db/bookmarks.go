package db

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Bookmark struct {
	ID           [16]byte
	BookID       [16]byte
	UserID       [16]byte
	SpineIndex   int
	ElemIndex    int
	StartOffset  int
	EndOffset    int
	SelectedText string
	Note         string
	Color        string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

func ListBookmarks(ctx context.Context, pool *pgxpool.Pool, bookID, userID [16]byte) ([]Bookmark, error) {
	rows, err := pool.Query(ctx, `
		SELECT id, book_id, user_id, spine_index, elem_index, start_offset,
		       end_offset, selected_text, note, color, created_at, updated_at
		FROM bookmarks
		WHERE book_id = $1 AND user_id = $2
		ORDER BY spine_index, elem_index, start_offset, created_at`, bookID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]Bookmark, 0)
	for rows.Next() {
		var b Bookmark
		if err := rows.Scan(&b.ID, &b.BookID, &b.UserID, &b.SpineIndex, &b.ElemIndex,
			&b.StartOffset, &b.EndOffset, &b.SelectedText, &b.Note, &b.Color,
			&b.CreatedAt, &b.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func CreateBookmark(ctx context.Context, pool *pgxpool.Pool, b Bookmark) (Bookmark, error) {
	err := pool.QueryRow(ctx, `
		INSERT INTO bookmarks
		    (book_id, user_id, spine_index, elem_index, start_offset, end_offset,
		     selected_text, note, color)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		RETURNING id, created_at, updated_at`,
		b.BookID, b.UserID, b.SpineIndex, b.ElemIndex, b.StartOffset, b.EndOffset,
		b.SelectedText, b.Note, b.Color,
	).Scan(&b.ID, &b.CreatedAt, &b.UpdatedAt)
	return b, err
}

func UpdateBookmark(ctx context.Context, pool *pgxpool.Pool, id, userID [16]byte, note, color string) (Bookmark, error) {
	var b Bookmark
	err := pool.QueryRow(ctx, `
		UPDATE bookmarks SET note = $3, color = $4, updated_at = NOW()
		WHERE id = $1 AND user_id = $2
		RETURNING id, book_id, user_id, spine_index, elem_index, start_offset,
		          end_offset, selected_text, note, color, created_at, updated_at`,
		id, userID, note, color,
	).Scan(&b.ID, &b.BookID, &b.UserID, &b.SpineIndex, &b.ElemIndex,
		&b.StartOffset, &b.EndOffset, &b.SelectedText, &b.Note, &b.Color,
		&b.CreatedAt, &b.UpdatedAt)
	return b, err
}

func DeleteBookmark(ctx context.Context, pool *pgxpool.Pool, id, userID [16]byte) (bool, error) {
	tag, err := pool.Exec(ctx, `DELETE FROM bookmarks WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}
