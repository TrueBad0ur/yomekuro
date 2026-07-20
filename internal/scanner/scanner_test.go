package scanner_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/truebad0ur/yomekuro/internal/db"
	"github.com/truebad0ur/yomekuro/internal/scanner"
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
	//
	//nolint:unused // pre-existing, unclear if still needed for a planned assertion
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

// Regression test for the scanner data-loss bug (audit item 1.1): if the
// library path becomes inaccessible, ScanLibrary must return an error and
// leave existing books untouched, not silently delete them all.
func TestScanner_ScanLibrary_InaccessiblePath_DoesNotWipe(t *testing.T) {
	pool := setupPool(t)
	ctx := context.Background()

	dataDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dataDir, "covers"), 0o755); err != nil {
		t.Fatal(err)
	}

	lib, err := db.CreateLibrary(ctx, pool, "test-scan-vanish", htmlTestdataDir())
	if err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}
	t.Cleanup(func() { db.DeleteLibrary(ctx, pool, lib.ID) })

	s := scanner.New(pool, dataDir)
	if err := s.ScanLibrary(ctx, lib); err != nil {
		t.Fatalf("initial ScanLibrary: %v", err)
	}

	before, err := db.CountBooks(ctx, pool, lib.ID)
	if err != nil {
		t.Fatalf("CountBooks: %v", err)
	}
	if before == 0 {
		t.Fatal("expected the initial scan to have inserted books")
	}

	vanished := lib
	vanished.Path = filepath.Join(t.TempDir(), "does-not-exist")

	if err := s.ScanLibrary(ctx, vanished); err == nil {
		t.Fatal("expected ScanLibrary to return an error for an inaccessible path")
	}

	after, err := db.CountBooks(ctx, pool, lib.ID)
	if err != nil {
		t.Fatalf("CountBooks: %v", err)
	}
	if after != before {
		t.Errorf("book count changed after failed scan: before=%d after=%d — books were wiped", before, after)
	}
}

// Regression test for audit item 3.2: a book's cover file must be deleted
// from disk when the book itself is removed from the library (not just the
// DB row), otherwise it accumulates as an orphan forever.
func TestScanner_ScanLibrary_RemovesOrphanedCover(t *testing.T) {
	pool := setupPool(t)
	ctx := context.Background()

	dataDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dataDir, "covers"), 0o755); err != nil {
		t.Fatal(err)
	}

	// A writable copy of the library, since the source file gets deleted
	// mid-test to simulate a book disappearing from disk.
	libDir := t.TempDir()
	src, err := os.ReadFile(filepath.Join(htmlTestdataDir(), "hakase_story.html"))
	if err != nil {
		t.Fatal(err)
	}
	bookPath := filepath.Join(libDir, "hakase_story.html")
	if err := os.WriteFile(bookPath, src, 0o644); err != nil {
		t.Fatal(err)
	}
	// A second, untouched book so the library never goes to 0 total files —
	// that's a distinct, deliberately-refused case (see the
	// InaccessiblePath_DoesNotWipe test) unrelated to what this test covers.
	otherPath := filepath.Join(libDir, "other.html")
	if err := os.WriteFile(otherPath, src, 0o644); err != nil {
		t.Fatal(err)
	}

	lib, err := db.CreateLibrary(ctx, pool, "test-scan-orphan-cover", libDir)
	if err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}
	t.Cleanup(func() { db.DeleteLibrary(ctx, pool, lib.ID) })

	s := scanner.New(pool, dataDir)
	if err := s.ScanLibrary(ctx, lib); err != nil {
		t.Fatalf("ScanLibrary: %v", err)
	}

	b, found, err := db.GetBookByPath(ctx, pool, bookPath)
	if err != nil || !found {
		t.Fatalf("GetBookByPath: found=%v err=%v", found, err)
	}
	full, err := db.GetBookByID(ctx, pool, b.ID)
	if err != nil {
		t.Fatalf("GetBookByID: %v", err)
	}
	if full.CoverPath == "" {
		t.Fatal("expected a cover to have been generated for the HTML book")
	}
	if _, err := os.Stat(full.CoverPath); err != nil {
		t.Fatalf("cover file missing right after scan: %v", err)
	}

	// Remove the book from disk and rescan — DeleteBooksNotIn should fire.
	if err := os.Remove(bookPath); err != nil {
		t.Fatal(err)
	}
	if err := s.ScanLibrary(ctx, lib); err != nil {
		t.Fatalf("second ScanLibrary: %v", err)
	}

	if _, err := os.Stat(full.CoverPath); !os.IsNotExist(err) {
		t.Errorf("cover file %s still exists after its book was removed (err=%v)", full.CoverPath, err)
	}
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
