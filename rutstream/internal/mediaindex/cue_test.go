package mediaindex

import (
	"strings"
	"testing"
)

const cueFixture = `REM GENRE Rock
REM DATE 1965
REM COMMENT "Super Deluxe Edition"
PERFORMER "The Beatles"
TITLE "Rubber Soul"
FILE "Rubber Soul (2026 Stereo Mix).flac" WAVE
  TRACK 01 AUDIO
    TITLE "Drive My Car (2026 Mix)"
    PERFORMER "The Beatles"
    INDEX 01 00:00:00
  TRACK 02 AUDIO
    TITLE "Norwegian Wood (This Bird Has Flown) (2026 Mix)"
    INDEX 01 02:31:44
  TRACK 03 AUDIO
    TITLE "You Won't See Me (2026 Mix)"
    INDEX 01 05:12:10
FILE "CD 2 (Sessions).flac" WAVE
  TRACK 01 AUDIO
    TITLE "Wait (Take 3 - Instrumental Backing Track)"
    INDEX 01 00:00:00
`

func TestParseCue(t *testing.T) {
	sheet, err := ParseCue(strings.NewReader(cueFixture))
	if err != nil {
		t.Fatal(err)
	}
	if sheet.Performer != "The Beatles" {
		t.Errorf("исполнитель = %q", sheet.Performer)
	}
	if sheet.Year != "1965" {
		t.Errorf("год = %q", sheet.Year)
	}
	if len(sheet.Files) != 2 {
		t.Fatalf("блоков FILE = %d, ожидалось 2", len(sheet.Files))
	}
	first := sheet.Files[0]
	if len(first.Tracks) != 3 {
		t.Fatalf("треков в первом блоке = %d, ожидалось 3", len(first.Tracks))
	}
	if first.Tracks[0].Title != "Drive My Car (2026 Mix)" {
		t.Errorf("название трека = %q", first.Tracks[0].Title)
	}
	if first.Tracks[0].Index != 0 {
		t.Errorf("смещение первого трека = %d", first.Tracks[0].Index)
	}
	// 02:31:44 → (2*60+31)*75+44 = 9644 кадра → 9644*44100/75 = 5 666 672 сэмпла
	want := int64((2*60+31)*75+44) * 44100 / 75
	if first.Tracks[1].Index != want {
		t.Errorf("смещение второго трека = %d, ожидалось %d", first.Tracks[1].Index, want)
	}
}

func TestCueTracksForFile(t *testing.T) {
	sheet, err := ParseCue(strings.NewReader(cueFixture))
	if err != nil {
		t.Fatal(err)
	}
	tracks := sheet.CueTracksForFile("CD 1 (2026 Stereo Mix)/Rubber Soul (2026 Stereo Mix).flac")
	if len(tracks) != 3 {
		t.Fatalf("треков = %d, ожидалось 3", len(tracks))
	}
	// Если имя не совпало — берётся первый блок.
	tracks = sheet.CueTracksForFile("unknown.flac")
	if len(tracks) != 3 {
		t.Fatalf("треков при несовпадении = %d, ожидалось 3", len(tracks))
	}
}

func TestCueTrackSamples(t *testing.T) {
	sheet, err := ParseCue(strings.NewReader(cueFixture))
	if err != nil {
		t.Fatal(err)
	}
	tracks := sheet.CueTracksForFile("Rubber Soul (2026 Stereo Mix).flac")
	// Альбом длится 10 минут = 26 460 000 сэмплов.
	const totalSamples = 600 * 44100
	out := sheet.TrackSamples(tracks, totalSamples)
	if len(out) != 3 {
		t.Fatalf("треков = %d", len(out))
	}
	if out[0].Index != 0 {
		t.Errorf("начало = %d", out[0].Index)
	}
	// Конец трека = начало следующего.
	if out[0].EndSamples != out[1].Index {
		t.Errorf("конец трека 1 = %d, ожидалось %d", out[0].EndSamples, out[1].Index)
	}
	if out[2].EndSamples != totalSamples {
		t.Errorf("конец последнего трека = %d, ожидалось %d", out[2].EndSamples, totalSamples)
	}
	// Границы должны возрастать.
	for i := 1; i < len(out); i++ {
		if out[i].Index <= out[i-1].Index {
			t.Errorf("границы не возрастают на треке %d", i)
		}
	}
}
