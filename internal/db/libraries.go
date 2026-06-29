package db

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Library struct {
	ID        [16]byte
	Name      string
	Path      string
	CreatedAt time.Time
}

func CreateLibrary(ctx context.Context, pool *pgxpool.Pool, name, path string) (Library, error) {
	id, err := NewUUID()
	if err != nil {
		return Library{}, err
	}
	var lib Library
	err = pool.QueryRow(ctx,
		`INSERT INTO libraries (id, name, path) VALUES ($1, $2, $3)
		 RETURNING id, name, path, created_at`,
		id, name, path,
	).Scan(&lib.ID, &lib.Name, &lib.Path, &lib.CreatedAt)
	return lib, err
}

func ListLibraries(ctx context.Context, pool *pgxpool.Pool) ([]Library, error) {
	rows, err := pool.Query(ctx, `SELECT id, name, path, created_at FROM libraries ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var libs []Library
	for rows.Next() {
		var l Library
		if err := rows.Scan(&l.ID, &l.Name, &l.Path, &l.CreatedAt); err != nil {
			return nil, err
		}
		libs = append(libs, l)
	}
	return libs, rows.Err()
}

func GetLibraryByID(ctx context.Context, pool *pgxpool.Pool, id [16]byte) (Library, error) {
	var l Library
	err := pool.QueryRow(ctx,
		`SELECT id, name, path, created_at FROM libraries WHERE id = $1`, id,
	).Scan(&l.ID, &l.Name, &l.Path, &l.CreatedAt)
	return l, err
}

func DeleteLibrary(ctx context.Context, pool *pgxpool.Pool, id [16]byte) error {
	_, err := pool.Exec(ctx, `DELETE FROM libraries WHERE id = $1`, id)
	return err
}
