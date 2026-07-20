package shared

import "testing"

func TestIsJapanese(t *testing.T) {
	cases := []struct {
		r    rune
		want bool
	}{
		{'あ', true},
		{'ア', true},
		{'辞', true},
		{'。', true},
		{'０', true},
		{'a', false},
		{'0', false},
		{' ', false},
	}
	for _, c := range cases {
		if got := IsJapanese(c.r); got != c.want {
			t.Errorf("IsJapanese(%q) = %v, want %v", c.r, got, c.want)
		}
	}
}

func TestToHalfwidth(t *testing.T) {
	cases := []struct{ in, want string }{
		{"葬送のフリーレン（０５）", "葬送のフリーレン(05)"},
		{"Frieren v01", "Frieren v01"},
		{"０１２３４５６７８９", "0123456789"},
	}
	for _, c := range cases {
		if got := ToHalfwidth(c.in); got != c.want {
			t.Errorf("ToHalfwidth(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestIsImageExt(t *testing.T) {
	for _, ext := range ImageExts {
		if !IsImageExt(ext) {
			t.Errorf("IsImageExt(%q) = false, want true (in ImageExts)", ext)
		}
	}
	if !IsImageExt(".JPG") {
		t.Error("IsImageExt should be case-insensitive")
	}
	if IsImageExt(".txt") {
		t.Error("IsImageExt(.txt) = true, want false")
	}
}

func TestLastInteger(t *testing.T) {
	cases := []struct {
		in     string
		want   int
		wantOK bool
	}{
		{"Frieren v14", 14, true},
		{"葬送のフリーレン（０５）", 5, true},
		{"no numbers", 0, false},
		{"v1.5", 5, true}, // decimal point does not glue digits together
	}
	for _, c := range cases {
		n, ok := LastInteger(c.in)
		if n != c.want || ok != c.wantOK {
			t.Errorf("LastInteger(%q) = (%d, %v), want (%d, %v)", c.in, n, ok, c.want, c.wantOK)
		}
	}
}

func TestLastNumber(t *testing.T) {
	cases := []struct {
		in     string
		want   float64
		wantOK bool
	}{
		{"Dungeon Meshi v02", 2, true},
		{"v1.5", 1.5, true},
		{"no numbers", 0, false},
	}
	for _, c := range cases {
		f, ok := LastNumber(c.in)
		if f != c.want || ok != c.wantOK {
			t.Errorf("LastNumber(%q) = (%v, %v), want (%v, %v)", c.in, f, ok, c.want, c.wantOK)
		}
	}
}
