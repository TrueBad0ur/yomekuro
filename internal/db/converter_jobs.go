package db

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
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

// DeleteConversionJob removes a job's DB row and returns its input/output
// paths so the caller can also clean those up. Leaving them on disk would
// make the job reappear on its own: converter-worker's manual-folder scan
// (converter/watch.go) treats any "<name>-in" folder with no matching DB row
// as an unclaimed manual conversion and picks it right back up.
func DeleteConversionJob(ctx context.Context, pool *pgxpool.Pool, id [16]byte) (inputPath, outputPath string, err error) {
	err = pool.QueryRow(ctx,
		`DELETE FROM conversion_jobs WHERE id = $1 RETURNING input_path, output_path`, id,
	).Scan(&inputPath, &outputPath)
	if err == pgx.ErrNoRows {
		return "", "", nil
	}
	return inputPath, outputPath, err
}

// ConversionJobNameTaken reports whether a non-terminal job already targets
// this name; callers should also stat the filesystem for on-disk collisions.
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
