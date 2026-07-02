package htmlbook_test

import (
	"testing"

	"github.com/truebad0ur/yomekuro/internal/htmlbook"
)

func TestOpen(t *testing.T) {
	b, err := htmlbook.Open("testdata/hakase_story.html")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if want := "博士の物語"; b.Title != want {
		t.Errorf("Title = %q, want %q", b.Title, want)
	}
	if want := "ja"; b.Language != want {
		t.Errorf("Language = %q, want %q", b.Language, want)
	}
	if b.ReadingDirection != "ltr" {
		t.Errorf("ReadingDirection = %q, want ltr", b.ReadingDirection)
	}
	if len(b.Authors) != 0 {
		t.Errorf("Authors = %v, want empty", b.Authors)
	}
}
