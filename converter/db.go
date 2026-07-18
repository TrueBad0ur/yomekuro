package main

import (
	"context"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Rescues rows a dead worker left 'running': claimNextJob only takes 'pending'
// and the API won't delete a running row, so they'd hang in the UI forever.
func reclaimOrphanedJobs(ctx context.Context, pool *pgxpool.Pool) error {
	// Asked to pause/stop before the restart — honour that rather than resuming it.
	paused, err := pool.Exec(ctx,
		`UPDATE conversion_jobs
		    SET status='paused', current_volume='', pause_requested=false, updated_at=NOW()
		  WHERE status='running' AND pause_requested`)
	if err != nil {
		return err
	}
	stopped, err := pool.Exec(ctx,
		`UPDATE conversion_jobs
		    SET status='stopped', current_volume='', stop_requested=false, updated_at=NOW()
		  WHERE status='running' AND stop_requested`)
	if err != nil {
		return err
	}
	// Otherwise put it back in the queue. mokuro caches per-volume OCR results,
	// so a resumed batch picks up where it left off instead of redoing it all.
	requeued, err := pool.Exec(ctx,
		`UPDATE conversion_jobs
		    SET status='pending', current_volume='', updated_at=NOW()
		  WHERE status='running'`)
	if err != nil {
		return err
	}
	if n := paused.RowsAffected(); n > 0 {
		slog.Info("watch: orphaned jobs marked paused", "count", n)
	}
	if n := stopped.RowsAffected(); n > 0 {
		slog.Info("watch: orphaned jobs marked stopped", "count", n)
	}
	if n := requeued.RowsAffected(); n > 0 {
		slog.Info("watch: orphaned jobs requeued", "count", n)
	}
	return nil
}

// A claimed row from conversion_jobs. id stays text — it's only ever passed back
// in a WHERE clause.
type job struct {
	ID           string
	Name         string
	InputPath    string
	OutputPath   string
	ForceOCR     bool
	Volume       string
	DetectorSize int
}

// Atomically claims the oldest pending job, or (nil, nil) if the queue is empty.
// SKIP LOCKED lets several workers poll the same table safely.
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
		RETURNING id::text, name, input_path, output_path, force_ocr, volume, detector_size
	`).Scan(&j.ID, &j.Name, &j.InputPath, &j.OutputPath, &j.ForceOCR, &j.Volume, &j.DetectorSize)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &j, nil
}

// Terminal transitions clear stop_requested: it is a request to the worker, and
// the UI keys its disabled "Stopping…" button off the flag alone.
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

// Records which volume mokuro is on, so the UI can show progress within a
// multi-volume job rather than a static "running".
func updateJobVolume(ctx context.Context, pool *pgxpool.Pool, id, volume string) error {
	_, err := pool.Exec(ctx,
		`UPDATE conversion_jobs SET current_volume=$2, updated_at=NOW() WHERE id=$1::uuid`, id, volume)
	return err
}

// Whether the row is flagged for cancellation — polled while mokuro runs.
// pause is checked ahead of stop by the caller: pause never wipes files on
// cancel, stop does when nothing converted yet, so which one wins matters.
func checkJobSignals(ctx context.Context, pool *pgxpool.Pool, id string) (stop, pause bool, err error) {
	err = pool.QueryRow(ctx,
		`SELECT stop_requested, pause_requested FROM conversion_jobs WHERE id=$1::uuid`, id,
	).Scan(&stop, &pause)
	if err == pgx.ErrNoRows {
		return false, false, nil
	}
	return stop, pause, err
}

func markJobStopped(ctx context.Context, pool *pgxpool.Pool, id string) error {
	_, err := pool.Exec(ctx,
		`UPDATE conversion_jobs SET status='stopped', current_volume='', stop_requested=false, updated_at=NOW() WHERE id=$1::uuid`, id)
	return err
}

// markJobPaused deliberately keeps current_volume (so the UI can show "paused
// at volume X") and never touches input_path/output_path — the whole point of
// pause vs stop is that resuming just flips status back to 'pending' with
// every file exactly where it was.
func markJobPaused(ctx context.Context, pool *pgxpool.Pool, id string) error {
	_, err := pool.Exec(ctx,
		`UPDATE conversion_jobs SET status='paused', pause_requested=false, updated_at=NOW() WHERE id=$1::uuid`, id)
	return err
}

// Whether any row already references inputPath — keeps the manual-folder scan
// off folders the DB queue owns.
func jobExistsForPath(ctx context.Context, pool *pgxpool.Pool, inputPath string) (bool, error) {
	var exists bool
	err := pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM conversion_jobs WHERE input_path = $1)`, inputPath,
	).Scan(&exists)
	return exists, err
}
