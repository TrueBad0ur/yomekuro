package epub_test

import (
	"bytes"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/truebad0ur/yomekuro/internal/epub"
)

func testdataPath(name string) string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "testdata", name)
}

func TestOpen_SAO05(t *testing.T) {
	b, err := epub.Open(testdataPath("sao05.epub"), "")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	tests := []struct {
		name string
		got  any
		want any
	}{
		{"Title", b.Title, "ソードアート・オンライン5 ファントム・バレット"},
		{"Language", b.Language, "ja"},
		{"Publisher", b.Publisher, "株式会社KADOKAWA"},
		{"ReadingDirection", b.ReadingDirection, "rtl"},
		{"CoverMediaType", b.CoverMediaType, "image/jpeg"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("got %q, want %q", tt.got, tt.want)
			}
		})
	}

	t.Run("Authors", func(t *testing.T) {
		if len(b.Authors) == 0 {
			t.Fatal("no authors")
		}
		if b.Authors[0].Name != "川原礫" {
			t.Errorf("author name: got %q, want %q", b.Authors[0].Name, "川原礫")
		}
		if b.Authors[0].Role != "writer" {
			t.Errorf("author role: got %q, want %q", b.Authors[0].Role, "writer")
		}
	})

	t.Run("CoverNonEmpty", func(t *testing.T) {
		if len(b.CoverData) == 0 {
			t.Fatal("cover data is empty")
		}
		// JPEG magic: FF D8 FF
		if !bytes.HasPrefix(b.CoverData, []byte{0xFF, 0xD8, 0xFF}) {
			t.Errorf("cover does not start with JPEG magic, got %X", b.CoverData[:4])
		}
	})

	t.Run("SpineNonEmpty", func(t *testing.T) {
		if len(b.Spine) == 0 {
			t.Fatal("spine is empty")
		}
	})

	t.Run("PageCountPositive", func(t *testing.T) {
		if b.PageCount < 1 {
			t.Errorf("page count: got %d, want ≥1", b.PageCount)
		}
	})

	t.Run("ManifestNonEmpty", func(t *testing.T) {
		if len(b.Manifest) == 0 {
			t.Fatal("manifest is empty")
		}
	})
}

func TestOpen_SAO01(t *testing.T) {
	b, err := epub.Open(testdataPath("sao01.epub"), "")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if b.Title == "" {
		t.Error("title is empty")
	}
	if b.Language != "ja" {
		t.Errorf("language: got %q, want ja", b.Language)
	}
	if len(b.CoverData) == 0 {
		t.Error("cover data is empty")
	}
}

func TestNormalizeHref(t *testing.T) {
	// Verify that spine items are resolved under opfDir ("item/...").
	b, err := epub.Open(testdataPath("sao05.epub"), "")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	for _, s := range b.Spine {
		if s.Href == "" {
			t.Error("spine item has empty href")
		}
		// All paths in this epub should start with "item/"
		if len(s.Href) < 4 || s.Href[:5] != "item/" {
			t.Errorf("unexpected spine href %q (expected item/...)", s.Href)
		}
	}
}
