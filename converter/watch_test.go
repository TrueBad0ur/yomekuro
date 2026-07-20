package main

import (
	"os"
	"path/filepath"
	"testing"
)

func mkFile(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCountVolumes_Subdirs(t *testing.T) {
	dir := t.TempDir()
	for _, sub := range []string{"v01", "v02", "_ocr"} {
		if err := os.Mkdir(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	n, err := countVolumes(dir)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("countVolumes: got %d, want 2 (should not count _ocr)", n)
	}
}

func TestCountVolumes_FlatImages(t *testing.T) {
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "page-001.jpg"))
	mkFile(t, filepath.Join(dir, "page-002.jpg"))
	n, err := countVolumes(dir)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("countVolumes: flat image folder should count as 1 volume, got %d", n)
	}
}

func TestCountVolumes_PDFsAndSubdirs(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "v01"), 0o755); err != nil {
		t.Fatal(err)
	}
	mkFile(t, filepath.Join(dir, "v02.pdf"))
	n, err := countVolumes(dir)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("countVolumes: got %d, want 2 (1 subdir + 1 pdf)", n)
	}
}

func TestManualFolderNeedsConversion(t *testing.T) {
	inDir := t.TempDir()
	outDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(inDir, "v01"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(inDir, "v02"), 0o755); err != nil {
		t.Fatal(err)
	}

	if !manualFolderNeedsConversion(inDir, outDir) {
		t.Error("expected conversion needed: 2 volumes in, 0 epubs out")
	}

	mkFile(t, filepath.Join(outDir, "v01.epub"))
	if !manualFolderNeedsConversion(inDir, outDir) {
		t.Error("expected conversion still needed: 2 volumes in, 1 epub out")
	}

	mkFile(t, filepath.Join(outDir, "v02.epub"))
	if manualFolderNeedsConversion(inDir, outDir) {
		t.Error("expected no conversion needed: 2 volumes in, 2 epubs out")
	}
}

func TestManualFolderNeedsConversion_EmptyInput(t *testing.T) {
	inDir := t.TempDir()
	outDir := t.TempDir()
	if manualFolderNeedsConversion(inDir, outDir) {
		t.Error("an empty input folder should never trigger conversion")
	}
}
