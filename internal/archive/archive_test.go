package archive

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

func TestExtractZip(t *testing.T) {
	src := filepath.Join(t.TempDir(), "test.zip")
	f, err := os.Create(src)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	writeZipEntry(t, zw, "vol1/001.jpg", "page1")
	writeZipEntry(t, zw, "vol1/002.jpg", "page2")
	writeZipEntry(t, zw, "__MACOSX/vol1/._001.jpg", "junk")
	writeZipEntry(t, zw, ".DS_Store", "junk")
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()

	dest := t.TempDir()
	if err := Extract(src, dest); err != nil {
		t.Fatalf("Extract: %v", err)
	}

	assertFile(t, filepath.Join(dest, "vol1", "001.jpg"), "page1")
	assertFile(t, filepath.Join(dest, "vol1", "002.jpg"), "page2")
	assertAbsent(t, filepath.Join(dest, "__MACOSX"))
	assertAbsent(t, filepath.Join(dest, ".DS_Store"))
}

func TestExtractZipCollapsesWrappingDir(t *testing.T) {
	src := filepath.Join(t.TempDir(), "Big Order.zip")
	f, err := os.Create(src)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	// Filenames deliberately don't follow a clean "001.jpg" pattern — real
	// scanlation releases use arbitrary names; collapse only looks at dirs.
	writeZipEntry(t, zw, "Big Order/vol.01/scan_a7.jpg", "v1pA")
	writeZipEntry(t, zw, "Big Order/vol.01/page_final.jpg", "v1pB")
	writeZipEntry(t, zw, "Big Order/vol.02/00_cover.png", "v2pA")
	writeZipEntry(t, zw, "Big Order/vol.02/17.jpg", "v2pB")
	writeZipEntry(t, zw, "Big Order/vol.03/IMG_9931.JPG", "v3pA")
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()

	dest := t.TempDir()
	if err := Extract(src, dest); err != nil {
		t.Fatalf("Extract: %v", err)
	}

	assertAbsent(t, filepath.Join(dest, "Big Order"))
	assertFile(t, filepath.Join(dest, "vol.01", "scan_a7.jpg"), "v1pA")
	assertFile(t, filepath.Join(dest, "vol.01", "page_final.jpg"), "v1pB")
	assertFile(t, filepath.Join(dest, "vol.02", "00_cover.png"), "v2pA")
	assertFile(t, filepath.Join(dest, "vol.02", "17.jpg"), "v2pB")
	assertFile(t, filepath.Join(dest, "vol.03", "IMG_9931.JPG"), "v3pA")
}

func TestExtractZipSlip(t *testing.T) {
	src := filepath.Join(t.TempDir(), "evil.zip")
	f, err := os.Create(src)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	writeZipEntry(t, zw, "../../etc/evil.jpg", "pwned")
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()

	dest := t.TempDir()
	if err := Extract(src, dest); err == nil {
		t.Fatal("expected zip-slip rejection, got nil error")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dest), "etc", "evil.jpg")); !os.IsNotExist(err) {
		t.Fatal("zip-slip entry was written outside dest")
	}
}

func TestExtractTarGz(t *testing.T) {
	src := filepath.Join(t.TempDir(), "test.tar.gz")
	f, err := os.Create(src)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	writeTarEntry(t, tw, "vol1/001.jpg", "page1")
	writeTarEntry(t, tw, "vol1/._001.jpg", "junk")
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()

	dest := t.TempDir()
	if err := Extract(src, dest); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	assertFile(t, filepath.Join(dest, "vol1", "001.jpg"), "page1")
	assertAbsent(t, filepath.Join(dest, "vol1", "._001.jpg"))
}

// Needs a real .rar fixture at testdata/sample.rar (not committed, no free
// encoder exists to synthesize one) — skipped if absent.
func TestExtractRar(t *testing.T) {
	src := filepath.Join("testdata", "sample.rar")
	if _, err := os.Stat(src); os.IsNotExist(err) {
		t.Skip("no testdata/sample.rar fixture present")
	}

	dest := t.TempDir()
	if err := Extract(src, dest); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if !containsAnyFile(t, dest) {
		t.Fatal("extraction produced no files")
	}
}

func containsAnyFile(t *testing.T, dir string) bool {
	t.Helper()
	found := false
	filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			found = true
		}
		return nil
	})
	return found
}

func writeZipEntry(t *testing.T, zw *zip.Writer, name, content string) {
	t.Helper()
	w, err := zw.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
}

func writeTarEntry(t *testing.T, tw *tar.Writer, name, content string) {
	t.Helper()
	hdr := &tar.Header{Name: name, Size: int64(len(content)), Mode: 0o644}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
}

func assertFile(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if !bytes.Equal(got, []byte(want)) {
		t.Errorf("%s content = %q, want %q", path, got, want)
	}
}

func assertAbsent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("%s should not exist", path)
	}
}
