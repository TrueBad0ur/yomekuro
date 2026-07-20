package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// Regression test for 2.1: Convert must fail loudly, not silently no-op and
// report success, when a single-volume reconvert names a raw-scan folder that
// doesn't actually exist under input (e.g. raw scan re-uploaded with a
// different naming convention).
func TestConvert_MissingVolumeFolder(t *testing.T) {
	input := t.TempDir()
	output := t.TempDir()
	if err := os.Mkdir(filepath.Join(input, "v01"), 0o755); err != nil {
		t.Fatal(err)
	}
	mkFile(t, filepath.Join(input, "v01", "page-001.jpg"))

	_, _, err := Convert(context.Background(), input, output, "v99-does-not-exist", true, DefaultDetectorSize, nil)
	if err == nil {
		t.Fatal("expected an error for a volume with no matching raw-scan folder, got nil")
	}
}
