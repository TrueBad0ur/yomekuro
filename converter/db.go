package main

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// job is a claimed row from yomekuro's conversion_jobs table (upload-driven
// path). id is kept as text — this module has no need for a UUID type beyond
// passing it back in a WHERE clause.
type job struct {
	ID         string
	Name       string
	InputPath  string
	OutputPath string
}

// claimNextJob atomically claims the oldest pending job, or returns (nil, nil)
// if the queue is empty. FOR UPDATE SKIP LOCKED lets multiple worker
// instances poll the same table without double-processing a row.
func claimNextJob(ctx context.Context, pool *pgxpool.Pool) (*job, error) {
	var j job
	err := pool.QueryRow(ctx, `
		UPDATE conversion_jobs
		SET status = 'running', updated_at = NOW()
		WHERE id = (
			SELECT id FROM conversion_jobs
			WHERE status = 'pending'
			ORDER BY created_at
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		RETURNING id::text, name, input_path, output_path
	`).Scan(&j.ID, &j.Name, &j.InputPath, &j.OutputPath)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &j, nil
}

// Every terminal transition also clears stop_requested. The flag is a *request*
// to the worker, so once the job has settled it is stale — and the UI keys its
// "Stopping…" (disabled) button off the flag alone, so leaving it set would
// strand the finished job in the list with no way to remove it. A job can also
// reach done/failed just as a stop comes in, which is why they clear it too.
func markJobDone(ctx context.Context, pool *pgxpool.Pool, id string) error {
	_, err := pool.Exec(ctx,
		`UPDATE conversion_jobs SET status='done', error='', current_volume='', stop_requested=false, updated_at=NOW() WHERE id=$1::uuid`, id)
	return err
}

func markJobFailed(ctx context.Context, pool *pgxpool.Pool, id, errMsg string) error {
	_, err := pool.Exec(ctx,
		`UPDATE conversion_jobs SET status='failed', error=$2, current_volume='', stop_requested=false, updated_at=NOW() WHERE id=$1::uuid`, id, errMsg)
	return err
}

// updateJobVolume records which volume mokuro is currently OCR'ing, so the
// settings UI can show progress within a multi-volume job instead of just a
// static "running" label.
func updateJobVolume(ctx context.Context, pool *pgxpool.Pool, id, volume string) error {
	_, err := pool.Exec(ctx,
		`UPDATE conversion_jobs SET current_volume=$2, updated_at=NOW() WHERE id=$1::uuid`, id, volume)
	return err
}

// stopRequested reports whether id's row has been flagged for cancellation
// (see internal/db.RequestStopConversionJob) — polled periodically while a
// job's mokuro subprocess is running, see watch.go.
func stopRequested(ctx context.Context, pool *pgxpool.Pool, id string) (bool, error) {
	var stop bool
	err := pool.QueryRow(ctx,
		`SELECT stop_requested FROM conversion_jobs WHERE id=$1::uuid`, id,
	).Scan(&stop)
	if err == pgx.ErrNoRows {
		return false, nil
	}
	return stop, err
}

func markJobStopped(ctx context.Context, pool *pgxpool.Pool, id string) error {
	_, err := pool.Exec(ctx,
		`UPDATE conversion_jobs SET status='stopped', current_volume='', stop_requested=false, updated_at=NOW() WHERE id=$1::uuid`, id)
	return err
}

// jobExistsForPath reports whether any conversion_jobs row (any status)
// already references inputPath — used to keep the manual "<name>-in" filesystem
// scan (see runWatch) from reprocessing a folder the DB queue owns.
func jobExistsForPath(ctx context.Context, pool *pgxpool.Pool, inputPath string) (bool, error) {
	var exists bool
	err := pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM conversion_jobs WHERE input_path = $1)`, inputPath,
	).Scan(&exists)
	return exists, err
}
