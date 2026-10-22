package rutracker

import (
	"strings"
	"testing"
)

func TestSearchPathEncodesCyrillic(t *testing.T) {
	got := searchPath("джон гарик", 0, 0)
	want := "/forum/tracker.php?nm=%D0%B4%D0%B6%D0%BE%D0%BD+%D0%B3%D0%B0%D1%80%D0%B8%D0%BA&start=0"
	if got != want {
		t.Fatalf("searchPath = %q, ожидалось %q", got, want)
	}
	if strings.ContainsFunc(got, func(r rune) bool { return r > 127 }) {
		t.Errorf("в URL остались сырые не-ASCII символы: %q", got)
	}
}

func TestSearchPathForumListing(t *testing.T) {
	got := searchPath("", 1698, 20)
	want := "/forum/tracker.php?f=1698&start=20"
	if got != want {
		t.Fatalf("searchPath = %q, ожидалось %q", got, want)
	}
}

func TestSearchPathEscapesSpecialChars(t *testing.T) {
	got := searchPath("AC/DC & Kiss", 0, 0)
	want := "/forum/tracker.php?nm=AC%2FDC+%26+Kiss&start=0"
	if got != want {
		t.Fatalf("searchPath = %q, ожидалось %q", got, want)
	}
}
