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

// CreateReconvertJob queues a full OCR re-run, bypassing mokuro's cache. An
// empty volume reconverts the whole book; non-empty limits it to one volume.
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

// PauseQueue pauses every job except one actively mid-volume right now. Unlike
// Stop, this never deletes files — resuming flips status back to 'pending'.
func PauseQueue(ctx context.Context, pool *pgxpool.Pool) (int64, error) {
	pending, err := pool.Exec(ctx,
		`UPDATE conversion_jobs SET status='paused', updated_at=NOW() WHERE status='pending'`)
	if err != nil {
		return 0, err
	}
	running, err := pool.Exec(ctx,
		`UPDATE conversion_jobs SET pause_requested=true, updated_at=NOW()
		 WHERE status='running' AND (current_volume = '' OR current_volume IS NULL)`)
	if err != nil {
		return 0, err
	}
	return pending.RowsAffected() + running.RowsAffected(), nil
}

// ResumeQueue flips every paused job back to 'pending' for the worker's
// normal poll loop to reclaim, same as reclaimOrphanedJobs.
func ResumeQueue(ctx context.Context, pool *pgxpool.Pool) (int64, error) {
	tag, err := pool.Exec(ctx,
		`UPDATE conversion_jobs SET status='pending', pause_requested=false, current_volume='', updated_at=NOW()
		 WHERE status='paused'`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// ConversionJobNameTaken reports whether a non-terminal job already targets
// this name; callers should also stat the filesystem for on-disk collisions.
func ConversionJobNameTaken(ctx context.Context, pool *pgxpool.Pool, libraryID [16]byte, name string) (bool, error) {
	var exists bool
	err := pool.QueryRow(ctx,
		`SELECT EXISTS(
			SELECT 1 FROM conversion_jobs
			WHERE library_id = $1 AND name = $2 AND status IN ('pending', 'running', 'paused')
		 )`,
		libraryID, name,
	).Scan(&exists)
	return exists, err
}
