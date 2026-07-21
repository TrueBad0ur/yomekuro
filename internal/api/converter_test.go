package api

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/text/unicode/norm"
)

func TestSanitizeName(t *testing.T) {
	cases := []struct {
		name    string
		wantErr bool
	}{
		{"Frieren v01", false},
		{"", true},
		{".hidden", true},
		{"../escape", true},
		{"sub/dir", true},
		{"Frieren-in", true}, // reserved suffix, see 4.8
	}
	for _, c := range cases {
		_, err := sanitizeName(c.name)
		if (err != nil) != c.wantErr {
			t.Errorf("sanitizeName(%q): err=%v, wantErr=%v", c.name, err, c.wantErr)
		}
	}
}

func TestLastNumber(t *testing.T) {
	cases := []struct {
		name   string
		wantN  int
		wantOK bool
	}{
		{"Frieren v01", 1, true},
		{"Frieren v14", 14, true},
		{"葬送のフリーレン（０５）", 5, true}, // fullwidth digits
		{"no numbers here", 0, false},
	}
	for _, c := range cases {
		n, ok := lastNumber(c.name)
		if n != c.wantN || ok != c.wantOK {
			t.Errorf("lastNumber(%q) = (%d, %v), want (%d, %v)", c.name, n, ok, c.wantN, c.wantOK)
		}
	}
}

func TestFindRawScanRoot_NFCNFDMismatch(t *testing.T) {
	base := t.TempDir()
	// Simulate a macOS/HFS+-sourced folder name: NFD-normalized.
	nameNFD := norm.NFD.String("解雇された暗黒兵士（30代）のスローなセカンドライフ")
	if err := os.Mkdir(filepath.Join(base, nameNFD+"-in"), 0o755); err != nil {
		t.Fatal(err)
	}

	// The book's own EPUB folder is NFC (server-created), as would come from
	// the DB's `books.series_name` / directory naming convention.
	nameNFC := norm.NFC.String("解雇された暗黒兵士（30代）のスローなセカンドライフ")

	dir, ok := findRawScanRoot(base, nameNFC)
	if !ok {
		t.Fatalf("findRawScanRoot: expected a match despite NFC/NFD mismatch, got none")
	}
	if filepath.Base(dir) != nameNFD+"-in" {
		t.Errorf("findRawScanRoot: resolved to %q, want %q", filepath.Base(dir), nameNFD+"-in")
	}
}

func TestFindRawScanRoot_ExactMatch(t *testing.T) {
	base := t.TempDir()
	if err := os.Mkdir(filepath.Join(base, "Frieren-in"), 0o755); err != nil {
		t.Fatal(err)
	}
	dir, ok := findRawScanRoot(base, "Frieren")
	if !ok || filepath.Base(dir) != "Frieren-in" {
		t.Fatalf("findRawScanRoot exact match failed: dir=%q ok=%v", dir, ok)
	}
}

func TestFindRawScanRoot_NoMatch(t *testing.T) {
	base := t.TempDir()
	if _, ok := findRawScanRoot(base, "Nonexistent"); ok {
		t.Errorf("findRawScanRoot: expected no match, got one")
	}
}

func TestRawScanNewerThanEPUB(t *testing.T) {
	dir := t.TempDir()
	old := time.Now().Add(-time.Hour)
	mkFile := func(name string, mtime time.Time) {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}
	mkFile("page1.jpg", old)
	mkFile("page2.jpg", old)

	if rawScanNewerThanEPUB(dir, time.Now()) {
		t.Error("expected false: no file newer than the EPUB")
	}

	mkFile("page3.jpg", time.Now().Add(time.Hour))
	if !rawScanNewerThanEPUB(dir, time.Now()) {
		t.Error("expected true: page3.jpg is newer than the EPUB")
	}
}

// Regression test for a gap found while manually fixing books on 2026-07-21:
// findRawScanRoot/findRawScanVolumeDir (2.1-2.5) only covered the "-in" raw
// scan side. A book's own OUTPUT folder and the .epub filenames inside it can
// independently carry different Unicode normalization from each other (and
// from the raw scan) for the same book — hit for real on
// 解雇された暗黒兵士（30代）のスローなセカンドライフ, whose output folder was NFD
// while its own .epub filenames were NFC. findOutputRoot/findEpubFile close
// that gap the same way findRawScanRoot/findRawScanVolumeDir already do.
func TestFindOutputRoot_NFCNFDMismatch(t *testing.T) {
	base := t.TempDir()
	nameNFD := norm.NFD.String("解雇された暗黒兵士（30代）のスローなセカンドライフ")
	if err := os.Mkdir(filepath.Join(base, nameNFD), 0o755); err != nil {
		t.Fatal(err)
	}
	nameNFC := norm.NFC.String("解雇された暗黒兵士（30代）のスローなセカンドライフ")

	dir, ok := findOutputRoot(base, nameNFC)
	if !ok {
		t.Fatalf("findOutputRoot: expected a match despite NFC/NFD mismatch, got none")
	}
	if filepath.Base(dir) != nameNFD {
		t.Errorf("findOutputRoot: resolved to %q, want %q", filepath.Base(dir), nameNFD)
	}
}

func TestFindOutputRoot_IgnoresRawScanFolder(t *testing.T) {
	base := t.TempDir()
	// Only the "-in" folder exists — must NOT be mistaken for the output folder.
	if err := os.Mkdir(filepath.Join(base, "Frieren-in"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, ok := findOutputRoot(base, "Frieren"); ok {
		t.Errorf("findOutputRoot matched the raw-scan folder, want no match")
	}
}

func TestFindEpubFile_NFCNFDMismatch(t *testing.T) {
	outDir := t.TempDir()
	// The folder's own name is irrelevant here — only the epub filename's
	// normalization matters, which findEpubFile must tolerate independently.
	volNFC := norm.NFC.String("解雇された暗黒兵士（30代）のスローなセカンドライフ v10")
	fname := norm.NFD.String(volNFC) + ".epub"
	if err := os.WriteFile(filepath.Join(outDir, fname), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	path, ok := findEpubFile(outDir, volNFC)
	if !ok {
		t.Fatalf("findEpubFile: expected a match despite NFC/NFD mismatch, got none")
	}
	if filepath.Base(path) != fname {
		t.Errorf("findEpubFile: resolved to %q, want %q", filepath.Base(path), fname)
	}
}

func TestFindEpubFile_ExactMatch(t *testing.T) {
	outDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(outDir, "Frieren v01.epub"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := findEpubFile(outDir, "Frieren v01"); !ok {
		t.Error("findEpubFile: exact match failed")
	}
	if _, ok := findEpubFile(outDir, "Frieren v99"); ok {
		t.Error("findEpubFile: expected no match for a nonexistent volume")
	}
}

func TestFindRawScanVolumeDir(t *testing.T) {
	base := t.TempDir()
	for _, d := range []string{"Ossan Bokensha v01", "Ossan Bokensha v02"} {
		if err := os.Mkdir(filepath.Join(base, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	// Exact match.
	if d, ok := findRawScanVolumeDir(base, "Ossan Bokensha v01"); !ok || d != "Ossan Bokensha v01" {
		t.Errorf("exact match failed: %q %v", d, ok)
	}

	// No exact/NFC match, but unambiguous trailing-number match: a raw scan
	// re-uploaded under a differently-styled name for the same volume number.
	if err := os.Mkdir(filepath.Join(base, "おっさん冒険者ケインの善行 03"), 0o755); err != nil {
		t.Fatal(err)
	}
	if d, ok := findRawScanVolumeDir(base, "Ossan Bokensha Kein no Zenko v03"); !ok || d != "おっさん冒険者ケインの善行 03" {
		t.Errorf("numeric fallback match failed: %q %v", d, ok)
	}

	// Ambiguous numeric fallback (two candidates share the same trailing
	// number) must not guess.
	if err := os.Mkdir(filepath.Join(base, "Other Series v01"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(base, "Yet Another v01"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, ok := findRawScanVolumeDir(base, "Some Unrelated Name v01"); ok {
		t.Errorf("expected ambiguous numeric fallback to refuse a match, but it matched")
	}
}
