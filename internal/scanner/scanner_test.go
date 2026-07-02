package scanner_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/truebad0ur/yomekuro/internal/db"
	"github.com/truebad0ur/yomekuro/internal/scanner"
	"github.com/jackc/pgx/v5/pgxpool"
)

func testdataDir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "epub", "testdata")
}

func htmlTestdataDir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "htmlbook", "testdata")
}

func setupPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DB_DSN")
	if dsn == "" {
		t.Skip("TEST_DB_DSN not set")
	}
	ctx := context.Background()
	pool, err := db.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestScanner_ScanLibrary(t *testing.T) {
	pool := setupPool(t)
	ctx := context.Background()

	dataDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dataDir, "covers"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Create a test library pointing at the epub testdata dir.
	lib, err := db.CreateLibrary(ctx, pool, "test-scan", testdataDir())
	if err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}
	t.Cleanup(func() { db.DeleteLibrary(ctx, pool, lib.ID) })

	s := scanner.New(pool, dataDir)

	// First scan: should insert all EPUBs.
	if err := s.ScanLibrary(ctx, lib); err != nil {
		t.Fatalf("ScanLibrary: %v", err)
	}

	count, err := db.CountBooks(ctx, pool, lib.ID)
	if err != nil {
		t.Fatalf("CountBooks: %v", err)
	}

	epubs, _ := filepath.Glob(filepath.Join(testdataDir(), "*.epub"))
	if count != len(epubs) {
		t.Errorf("book count: got %d, want %d", count, len(epubs))
	}

	// Verify cover files exist.
	covers, err := filepath.Glob(filepath.Join(dataDir, "covers", "*.jpg"))
	if err != nil {
		t.Fatal(err)
	}
	if len(covers) == 0 {
		t.Error("no cover files saved")
	}

	// Second scan: no files changed → updated count should be 0.
	// We track this indirectly via updated_at: it should NOT advance.
	type updatedAt struct {
		path string
		at   time.Time
	}
	rows, err := pool.Query(ctx,
		`SELECT path, updated_at FROM books WHERE library_id=$1`, lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	before := make(map[string]time.Time)
	for rows.Next() {
		var p string
		var u time.Time
		rows.Scan(&p, &u)
		before[p] = u
	}
	rows.Close()

	// Tiny sleep so any re-write would produce a different updated_at.
	time.Sleep(50 * time.Millisecond)

	if err := s.ScanLibrary(ctx, lib); err != nil {
		t.Fatalf("second ScanLibrary: %v", err)
	}

	rows, err = pool.Query(ctx,
		`SELECT path, updated_at FROM books WHERE library_id=$1`, lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var p string
		var u time.Time
		rows.Scan(&p, &u)
		if b, ok := before[p]; ok && !u.Equal(b) {
			t.Errorf("book %s was re-written on second scan (updated_at changed)", filepath.Base(p))
		}
	}
	rows.Close()
}

func TestScanner_ScanLibrary_HTML(t *testing.T) {
	pool := setupPool(t)
	ctx := context.Background()

	dataDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dataDir, "covers"), 0o755); err != nil {
		t.Fatal(err)
	}

	lib, err := db.CreateLibrary(ctx, pool, "test-scan-html", htmlTestdataDir())
	if err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}
	t.Cleanup(func() { db.DeleteLibrary(ctx, pool, lib.ID) })

	s := scanner.New(pool, dataDir)
	if err := s.ScanLibrary(ctx, lib); err != nil {
		t.Fatalf("ScanLibrary: %v", err)
	}

	path := filepath.Join(htmlTestdataDir(), "hakase_story.html")
	b, found, err := db.GetBookByPath(ctx, pool, path)
	if err != nil {
		t.Fatalf("GetBookByPath: %v", err)
	}
	if !found {
		t.Fatal("html book not found after scan")
	}
	full, err := db.GetBookByID(ctx, pool, b.ID)
	if err != nil {
		t.Fatalf("GetBookByID: %v", err)
	}
	if full.Format != "html" {
		t.Errorf("Format = %q, want html", full.Format)
	}
	if want := "博士の物語"; full.Title != want {
		t.Errorf("Title = %q, want %q", full.Title, want)
	}
	if full.PageCount != 1 {
		t.Errorf("PageCount = %d, want 1", full.PageCount)
	}
}
