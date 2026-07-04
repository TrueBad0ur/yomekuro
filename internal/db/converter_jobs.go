package db

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type ConversionJob struct {
	ID            [16]byte
	LibraryID     [16]byte
	Name          string
	InputPath     string
	OutputPath    string
	Status        string
	Error         string
	CurrentVolume string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

func CreateConversionJob(ctx context.Context, pool *pgxpool.Pool, libraryID [16]byte, name, inputPath, outputPath string) (ConversionJob, error) {
	var j ConversionJob
	err := pool.QueryRow(ctx,
		`INSERT INTO conversion_jobs (library_id, name, input_path, output_path)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id, library_id, name, input_path, output_path, status, error, current_volume, created_at, updated_at`,
		libraryID, name, inputPath, outputPath,
	).Scan(&j.ID, &j.LibraryID, &j.Name, &j.InputPath, &j.OutputPath, &j.Status, &j.Error, &j.CurrentVolume, &j.CreatedAt, &j.UpdatedAt)
	return j, err
}

func ListConversionJobs(ctx context.Context, pool *pgxpool.Pool) ([]ConversionJob, error) {
	rows, err := pool.Query(ctx,
		`SELECT id, library_id, name, input_path, output_path, status, error, current_volume, created_at, updated_at
		 FROM conversion_jobs ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []ConversionJob
	for rows.Next() {
		var j ConversionJob
		if err := rows.Scan(&j.ID, &j.LibraryID, &j.Name, &j.InputPath, &j.OutputPath, &j.Status, &j.Error, &j.CurrentVolume, &j.CreatedAt, &j.UpdatedAt); err != nil {
			return nil, err
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

// DeleteConversionJob removes a job's DB row only — the extracted "<name>-in"
// source and any produced "<name>" EPUBs are left on disk untouched.
func DeleteConversionJob(ctx context.Context, pool *pgxpool.Pool, id [16]byte) error {
	_, err := pool.Exec(ctx, `DELETE FROM conversion_jobs WHERE id = $1`, id)
	return err
}

// NameOrPathTaken reports whether a non-terminal (pending/running) job already
// targets this name in this library, or the name collides with an existing
// on-disk "<name>-in"/"<name>" folder. Callers should also stat the
// filesystem paths — this only covers concurrent uploads racing the DB.
func ConversionJobNameTaken(ctx context.Context, pool *pgxpool.Pool, libraryID [16]byte, name string) (bool, error) {
	var exists bool
	err := pool.QueryRow(ctx,
		`SELECT EXISTS(
			SELECT 1 FROM conversion_jobs
			WHERE library_id = $1 AND name = $2 AND status IN ('pending', 'running')
		 )`,
		libraryID, name,
	).Scan(&exists)
	return exists, err
}
