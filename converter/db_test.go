package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// setupPool applies the server module's schema (converter has no schema of
// its own, and no shared package to pull the migration runner from — see
// CLAUDE.md's cross-module notes) and returns a pool, or skips if
// TEST_DB_DSN is unset. Mirrors internal/scanner/scanner_test.go's pattern.
func setupPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DB_DSN")
	if dsn == "" {
		t.Skip("TEST_DB_DSN not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)

	_, file, _, _ := runtime.Caller(0)
	sqlPath := filepath.Join(filepath.Dir(file), "..", "internal", "db", "migrations", "0001_init.sql")
	schema, err := os.ReadFile(sqlPath)
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	if _, err := pool.Exec(ctx, string(schema)); err != nil {
		t.Fatalf("apply schema: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), "DROP SCHEMA public CASCADE; CREATE SCHEMA public;")
	})
	return pool
}

func testLibrary(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var id string
	err := pool.QueryRow(context.Background(),
		`INSERT INTO libraries (name, path) VALUES ('test', '/tmp/test-lib') RETURNING id::text`,
	).Scan(&id)
	if err != nil {
		t.Fatalf("create library: %v", err)
	}
	return id
}

func insertPendingJob(t *testing.T, pool *pgxpool.Pool, libID, name, volume string) string {
	t.Helper()
	var id string
	err := pool.QueryRow(context.Background(),
		`INSERT INTO conversion_jobs (library_id, name, input_path, output_path, force_ocr, volume)
		 VALUES ($1::uuid, $2, '/in', '/out', true, $3) RETURNING id::text`,
		libID, name, volume,
	).Scan(&id)
	if err != nil {
		t.Fatalf("insert pending job: %v", err)
	}
	// Guarantee a strictly later created_at for the next insert, since
	// claimNextJob orders by it and a same-microsecond tie is possible on a
	// fast test machine.
	time.Sleep(2 * time.Millisecond)
	return id
}

// Regression test for the core of 4.4 (server-side bulk-reconvert queue):
// claimNextJob must serialize pending jobs that share (library_id, name) —
// several volumes of the same book queued at once must run strictly one at a
// time, never concurrently, since they'd otherwise fight over the same
// "-in"/output folders.
func TestClaimNextJob_SerializesSameName(t *testing.T) {
	pool := setupPool(t)
	ctx := context.Background()
	libID := testLibrary(t, pool)

	id1 := insertPendingJob(t, pool, libID, "Frieren", "v01")
	id2 := insertPendingJob(t, pool, libID, "Frieren", "v02")
	id3 := insertPendingJob(t, pool, libID, "Frieren", "v03")

	j, err := claimNextJob(ctx, pool)
	if err != nil {
		t.Fatalf("claimNextJob: %v", err)
	}
	if j == nil || j.ID != id1 {
		t.Fatalf("expected to claim the oldest pending job (v01), got %+v", j)
	}

	// v01 is now 'running' — v02 and v03 must NOT be claimable yet, even
	// though they're still 'pending'.
	j2, err := claimNextJob(ctx, pool)
	if err != nil {
		t.Fatalf("claimNextJob (should find nothing claimable): %v", err)
	}
	if j2 != nil {
		t.Fatalf("expected no claimable job while v01 is running, got %+v", j2)
	}

	// Finish v01 — now v02 should become claimable.
	if _, err := pool.Exec(ctx, `UPDATE conversion_jobs SET status='done' WHERE id=$1::uuid`, id1); err != nil {
		t.Fatal(err)
	}
	j3, err := claimNextJob(ctx, pool)
	if err != nil {
		t.Fatalf("claimNextJob after v01 done: %v", err)
	}
	if j3 == nil || j3.ID != id2 {
		t.Fatalf("expected to claim v02 next, got %+v", j3)
	}

	_ = id3 // still pending, untouched — covered implicitly by the above ordering check
}

// A pending job for a different book must never be blocked by an unrelated
// book's running job — only same (library_id, name) pairs serialize.
func TestClaimNextJob_DifferentNamesRunConcurrently(t *testing.T) {
	pool := setupPool(t)
	ctx := context.Background()
	libID := testLibrary(t, pool)

	insertPendingJob(t, pool, libID, "Frieren", "v01")
	insertPendingJob(t, pool, libID, "Dungeon Meshi", "v01")

	j1, err := claimNextJob(ctx, pool)
	if err != nil || j1 == nil {
		t.Fatalf("claim 1: job=%+v err=%v", j1, err)
	}
	j2, err := claimNextJob(ctx, pool)
	if err != nil || j2 == nil {
		t.Fatalf("claim 2: expected the other book's job to be claimable concurrently, job=%+v err=%v", j2, err)
	}
	if j1.Name == j2.Name {
		t.Fatalf("claimed two jobs for the same name concurrently: %q", j1.Name)
	}
}
