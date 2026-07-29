package db

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type UserCategory struct {
	ID        [16]byte
	UserID    [16]byte
	Name      string
	IsSystem  bool
	CreatedAt time.Time
	Series    []CategorySeries
}

type CategorySeries struct {
	Name        string
	BookCount   int
	CoverBookID [16]byte
}

func EnsureDefaultCategory(ctx context.Context, pool *pgxpool.Pool, userID [16]byte) error {
	_, err := pool.Exec(ctx, `
		INSERT INTO user_categories (user_id, name, is_system)
		VALUES ($1, 'Read Later', true)
		ON CONFLICT (user_id, LOWER(name)) DO NOTHING`, userID)
	return err
}

func ListUserCategories(ctx context.Context, pool *pgxpool.Pool, userID [16]byte) ([]UserCategory, error) {
	if err := EnsureDefaultCategory(ctx, pool, userID); err != nil {
		return nil, err
	}
	rows, err := pool.Query(ctx, `
		SELECT c.id, c.user_id, c.name, c.is_system, c.created_at,
		       cs.series_name,
		       COALESCE(s.book_count, 0),
		       s.cover_book_id
		FROM user_categories c
		LEFT JOIN user_category_series cs ON cs.category_id = c.id
		LEFT JOIN LATERAL (
			SELECT COUNT(*)::int AS book_count,
			       (ARRAY_AGG(b.id ORDER BY b.series_index NULLS LAST, b.title))[1] AS cover_book_id
			FROM books b
			WHERE b.series_name = cs.series_name AND b.format != 'html'
		) s ON true
		WHERE c.user_id = $1
		ORDER BY c.is_system DESC, LOWER(c.name), cs.added_at`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	byID := make(map[[16]byte]int)
	out := make([]UserCategory, 0)
	for rows.Next() {
		var c UserCategory
		var seriesName *string
		var bookCount int
		var coverID *[16]byte
		if err := rows.Scan(&c.ID, &c.UserID, &c.Name, &c.IsSystem, &c.CreatedAt,
			&seriesName, &bookCount, &coverID); err != nil {
			return nil, err
		}
		idx, exists := byID[c.ID]
		if !exists {
			idx = len(out)
			byID[c.ID] = idx
			out = append(out, c)
		}
		if seriesName != nil && coverID != nil && bookCount > 0 {
			out[idx].Series = append(out[idx].Series, CategorySeries{
				Name: *seriesName, BookCount: bookCount, CoverBookID: *coverID,
			})
		}
	}
	return out, rows.Err()
}

func CreateUserCategory(ctx context.Context, pool *pgxpool.Pool, userID [16]byte, name string) (UserCategory, error) {
	var c UserCategory
	err := pool.QueryRow(ctx, `
		INSERT INTO user_categories (user_id, name)
		VALUES ($1, $2)
		RETURNING id, user_id, name, is_system, created_at`, userID, name,
	).Scan(&c.ID, &c.UserID, &c.Name, &c.IsSystem, &c.CreatedAt)
	return c, err
}

func RenameUserCategory(ctx context.Context, pool *pgxpool.Pool, id, userID [16]byte, name string) (UserCategory, error) {
	var c UserCategory
	err := pool.QueryRow(ctx, `
		UPDATE user_categories SET name = $3
		WHERE id = $1 AND user_id = $2 AND NOT is_system
		RETURNING id, user_id, name, is_system, created_at`, id, userID, name,
	).Scan(&c.ID, &c.UserID, &c.Name, &c.IsSystem, &c.CreatedAt)
	return c, err
}

func DeleteUserCategory(ctx context.Context, pool *pgxpool.Pool, id, userID [16]byte) (bool, error) {
	tag, err := pool.Exec(ctx, `
		DELETE FROM user_categories
		WHERE id = $1 AND user_id = $2 AND NOT is_system`, id, userID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

func SetSeriesCategory(ctx context.Context, pool *pgxpool.Pool, categoryID, userID [16]byte, seriesName string, included bool) (bool, error) {
	var exists bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM user_categories WHERE id = $1 AND user_id = $2
		)`, categoryID, userID).Scan(&exists); err != nil || !exists {
		return exists, err
	}
	if included {
		var seriesExists bool
		if err := pool.QueryRow(ctx, `
			SELECT EXISTS (SELECT 1 FROM books WHERE series_name = $1 AND format != 'html')`,
			seriesName).Scan(&seriesExists); err != nil {
			return false, err
		}
		if !seriesExists {
			return false, pgx.ErrNoRows
		}
		_, err := pool.Exec(ctx, `
			INSERT INTO user_category_series (category_id, series_name)
			VALUES ($1, $2) ON CONFLICT DO NOTHING`, categoryID, seriesName)
		return true, err
	}
	_, err := pool.Exec(ctx, `
		DELETE FROM user_category_series WHERE category_id = $1 AND series_name = $2`,
		categoryID, seriesName)
	return true, err
}
