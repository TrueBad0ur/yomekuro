package api

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

func writeTestZip(t *testing.T, path string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	w, err := zw.Create("page1.xhtml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("<html></html>")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
}

// Regression test for audit item 3.1: a reader holding a handle from get()
// must be able to keep using it even after a concurrent get() call
// invalidates or evicts that same cache entry (e.g. a reconvert rewrites the
// EPUB, changing file_hash, while another request is mid-read of the old
// content). Only once every holder calls Release() should the underlying
// zip.ReadCloser actually close.
func TestZipCache_HandleSurvivesInvalidation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "book.epub")
	writeTestZip(t, path)

	c := newZipCache(10)

	h1, err := c.get(path, "hash-a")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(h1.Files()) != 1 {
		t.Fatalf("expected 1 file in zip, got %d", len(h1.Files()))
	}

	// Simulate a concurrent reconvert: the file's hash changed, so the next
	// get() for the same path invalidates h1's entry — but h1 is still held.
	h2, err := c.get(path, "hash-b")
	if err != nil {
		t.Fatalf("second get: %v", err)
	}
	defer h2.Release()

	// h1's entry must be marked evicted, not closed outright, since refs>0.
	if h1.entry.refs != 1 {
		t.Errorf("h1.entry.refs = %d, want 1 (still held)", h1.entry.refs)
	}
	if !h1.entry.evicted {
		t.Error("h1.entry.evicted = false, want true (invalidated while held)")
	}

	// The held handle must still be usable — this is the actual bug: without
	// refcounting, entry.rc.Close() would have already run above, and this
	// read would fail or hit a reused/closed file descriptor.
	if len(h1.Files()) != 1 {
		t.Fatal("h1.Files() broke after concurrent invalidation — use-after-close")
	}
	if _, err := h1.Files()[0].Open(); err != nil {
		t.Errorf("reading from h1 after concurrent invalidation failed: %v", err)
	}

	h1.Release()
	if h1.entry.refs != 0 {
		t.Errorf("h1.entry.refs = %d after Release, want 0", h1.entry.refs)
	}
}

// Regression test for the pre-refcounting behavior: LRU eviction while a
// handle is still held must not close it out from under the reader either.
func TestZipCache_HandleSurvivesLRUEviction(t *testing.T) {
	dir := t.TempDir()
	pathA := filepath.Join(dir, "a.epub")
	pathB := filepath.Join(dir, "b.epub")
	writeTestZip(t, pathA)
	writeTestZip(t, pathB)

	c := newZipCache(1) // capacity 1: opening b evicts a

	ha, err := c.get(pathA, "hash-a")
	if err != nil {
		t.Fatalf("get a: %v", err)
	}

	hb, err := c.get(pathB, "hash-b")
	if err != nil {
		t.Fatalf("get b: %v", err)
	}
	defer hb.Release()

	if !ha.entry.evicted {
		t.Error("ha.entry.evicted = false, want true (LRU-evicted while held)")
	}
	if _, err := ha.Files()[0].Open(); err != nil {
		t.Errorf("reading from ha after LRU eviction failed: %v", err)
	}
	ha.Release()
}
