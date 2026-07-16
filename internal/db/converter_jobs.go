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
	StopRequested bool
	ForceOCR      bool
	Volume        string
	DetectorSize  int
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

func CreateConversionJob(ctx context.Context, pool *pgxpool.Pool, libraryID [16]byte, name, inputPath, outputPath string) (ConversionJob, error) {
	var j ConversionJob
	err := pool.QueryRow(ctx,
		`INSERT INTO conversion_jobs (library_id, name, input_path, output_path)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id, library_id, name, input_path, output_path, status, error, current_volume, stop_requested, force_ocr, volume, detector_size, created_at, updated_at`,
		libraryID, name, inputPath, outputPath,
	).Scan(&j.ID, &j.LibraryID, &j.Name, &j.InputPath, &j.OutputPath, &j.Status, &j.Error, &j.CurrentVolume, &j.StopRequested, &j.ForceOCR, &j.Volume, &j.DetectorSize, &j.CreatedAt, &j.UpdatedAt)
	return j, err
}

// CreateReconvertJob queues a full OCR re-run over an existing book's already-staged
// input/output dirs — unlike a fresh upload, mokuro's cache is bypassed entirely.
// An empty volume reconverts every volume in the book; a non-empty one limits the
// run to just that volume (matching the epub's basename in the output dir).
// detectorSize is the text-detector's input resolution — the caller (API layer)
// validates it against the fixed set of choices offered in the UI.
func CreateReconvertJob(ctx context.Context, pool *pgxpool.Pool, libraryID [16]byte, name, inputPath, outputPath, volume string, detectorSize int) (ConversionJob, error) {
	var j ConversionJob
	err := pool.QueryRow(ctx,
		`INSERT INTO conversion_jobs (library_id, name, input_path, output_path, force_ocr, volume, detector_size)
		 VALUES ($1, $2, $3, $4, true, $5, $6)
		 RETURNING id, library_id, name, input_path, output_path, status, error, current_volume, stop_requested, force_ocr, volume, detector_size, created_at, updated_at`,
		libraryID, name, inputPath, outputPath, volume, detectorSize,
	).Scan(&j.ID, &j.LibraryID, &j.Name, &j.InputPath, &j.OutputPath, &j.Status, &j.Error, &j.CurrentVolume, &j.StopRequested, &j.ForceOCR, &j.Volume, &j.DetectorSize, &j.CreatedAt, &j.UpdatedAt)
	return j, err
}

func ListConversionJobs(ctx context.Context, pool *pgxpool.Pool) ([]ConversionJob, error) {
	rows, err := pool.Query(ctx,
		`SELECT id, library_id, name, input_path, output_path, status, error, current_volume, stop_requested, force_ocr, volume, detector_size, created_at, updated_at
		 FROM conversion_jobs ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []ConversionJob
	for rows.Next() {
		var j ConversionJob
		if err := rows.Scan(&j.ID, &j.LibraryID, &j.Name, &j.InputPath, &j.OutputPath, &j.Status, &j.Error, &j.CurrentVolume, &j.StopRequested, &j.ForceOCR, &j.Volume, &j.DetectorSize, &j.CreatedAt, &j.UpdatedAt); err != nil {
			return nil, err
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

// GetConversionJob fetches a single job by id.
func GetConversionJob(ctx context.Context, pool *pgxpool.Pool, id [16]byte) (ConversionJob, error) {
	var j ConversionJob
	err := pool.QueryRow(ctx,
		`SELECT id, library_id, name, input_path, output_path, status, error, current_volume, stop_requested, force_ocr, volume, detector_size, created_at, updated_at
		 FROM conversion_jobs WHERE id = $1`, id,
	).Scan(&j.ID, &j.LibraryID, &j.Name, &j.InputPath, &j.OutputPath, &j.Status, &j.Error, &j.CurrentVolume, &j.StopRequested, &j.ForceOCR, &j.Volume, &j.DetectorSize, &j.CreatedAt, &j.UpdatedAt)
	return j, err
}

// Removes a job's row, returning its paths to clean up too — left on disk, the
// manual scan repicks it. Not for a 'running' job: that races the live mokuro.
func DeleteConversionJob(ctx context.Context, pool *pgxpool.Pool, id [16]byte) (inputPath, outputPath string, err error) {
	err = pool.QueryRow(ctx,
		`DELETE FROM conversion_jobs WHERE id = $1 RETURNING input_path, output_path`, id,
	).Scan(&inputPath, &outputPath)
	if err == pgx.ErrNoRows {
		return "", "", nil
	}
	return inputPath, outputPath, err
}

// Flags a running job for cancellation rather than deleting it: the worker polls
// this, kills mokuro, and only then cleans up the row and its files.
func RequestStopConversionJob(ctx context.Context, pool *pgxpool.Pool, id [16]byte) error {
	_, err := pool.Exec(ctx,
		`UPDATE conversion_jobs SET stop_requested = true, updated_at = NOW() WHERE id = $1`, id)
	return err
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
