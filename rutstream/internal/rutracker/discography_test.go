package rutracker

import "testing"

func TestAlbumOfTitle(t *testing.T) {
	cases := []struct {
		title string
		album string
		year  int
	}{
		{"[WEB] Phillip Toussaint - Monn Ganny Bless - 2026, FLAC (tracks), lossless [484.4 MB]",
			"Monn Ganny Bless", 2026},
		{"Madonna - The Immaculate Collection (2000) [FLAC] [Tracks]", "The Immaculate Collection", 2000},
		{"Rammstein --complete discography- [FLAC]", "complete discography", 0},
		{"Кино — Группа крови (1988) [MP3 320]", "Группа крови", 1988},
	}
	for _, c := range cases {
		album, year := AlbumOfTitle(c.title)
		if year != c.year {
			t.Errorf("AlbumOfTitle(%q): год = %d, ожидалось %d", c.title, year, c.year)
		}
		if album == "" {
			t.Errorf("AlbumOfTitle(%q): пустой альбом", c.title)
		}
	}
}

func TestIsCollectionTitle(t *testing.T) {
	yes := []string{
		"Rammstein --complete discography- [FLAC]",
		"Artist - Полная дискография [FLAC]",
		"Artist - Studio Albums [2CD]",
		"Artist - Anthology 1990-2000",
	}
	no := []string{
		"[WEB] Artist - Album - 2026, FLAC (tracks), lossless",
		"Artist - Greatest Hits [MP3]",
	}
	for _, s := range yes {
		if !IsCollectionTitle(s) {
			t.Errorf("IsCollectionTitle(%q) = false, ожидалось true", s)
		}
	}
	for _, s := range no {
		if IsCollectionTitle(s) {
			t.Errorf("IsCollectionTitle(%q) = true, ожидалось false", s)
		}
	}
}

func TestIsLosslessTitle(t *testing.T) {
	yes := []string{
		"[WEB] Artist - Album - 2026, FLAC (tracks), lossless [484.4 MB]",
		"Artist - Album [FLAC 24/96]",
		"Artist - Album [ALAC]",
	}
	no := []string{
		"Artist - Album [MP3 320]",
		"Artist - Album [AAC 256]",
	}
	for _, s := range yes {
		if !IsLosslessTitle(s) {
			t.Errorf("IsLosslessTitle(%q) = false, ожидалось true", s)
		}
	}
	for _, s := range no {
		if IsLosslessTitle(s) {
			t.Errorf("IsLosslessTitle(%q) = true, ожидалось false", s)
		}
	}
}

func TestGroupReleases(t *testing.T) {
	topics := []Topic{
		{ID: 1, Title: "[WEB] Artist - First Album - 2001, FLAC (tracks), lossless", Seeds: 5, Size: 300},
		{ID: 2, Title: "Artist - First Album - 2001 [MP3 320]", Seeds: 20, Size: 100},
		{ID: 3, Title: "Artist - Second Album - 2005 [FLAC]", Seeds: 3, Size: 250},
		{ID: 4, Title: "Artist --complete discography- [FLAC]", Seeds: 1, Size: 900},
	}
	releases := GroupReleases("Artist", topics)
	if len(releases) != 3 {
		t.Fatalf("релизов = %d, ожидалось 3: %+v", len(releases), releases)
	}
	// Коллекция должна идти первой.
	if !releases[0].IsCollection {
		t.Errorf("первым должен идти сборник дискографии, получено %q", releases[0].Album)
	}
	// Внутри релиза lossless-версия должна быть первой.
	for _, rel := range releases {
		if rel.Album == "First Album" {
			if !rel.Lossless {
				t.Error("релиз First Album должен быть помечен как lossless")
			}
			if rel.Best.ID != 1 {
				t.Errorf("лучшей должна быть lossless-раздача (id=1), получено %d", rel.Best.ID)
			}
		}
	}
}

func TestExtractCookieSet(t *testing.T) {
	text := "bb_session=0-abc-def; bb_guid=xyz; cf_clearance=token123; bb_ssl=1"
	set := ExtractCookieSet(text)
	if set["bb_session"] != "0-abc-def" {
		t.Errorf("bb_session = %q", set["bb_session"])
	}
	if set["cf_clearance"] != "token123" {
		t.Errorf("cf_clearance = %q", set["cf_clearance"])
	}
	if len(set) != 4 {
		t.Errorf("cookie в наборе: %d, ожидалось 4: %v", len(set), set)
	}
}

func TestExtractSessionCookie(t *testing.T) {
	if got := ExtractSessionCookie("bb_session=0-abc-def; bb_ssl=1"); got != "0-abc-def" {
		t.Errorf("ExtractSessionCookie = %q", got)
	}
	if got := ExtractSessionCookie("bb_guid=lb4Fzsiw7zij; bb_ssl=1"); got != "" {
		t.Errorf("ожидалась пустая строка, получено %q", got)
	}
}
