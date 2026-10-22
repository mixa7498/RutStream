package playback

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mixa7/rutstream/internal/mediaindex"
	"github.com/mixa7/rutstream/internal/testutil"
	"github.com/mixa7/rutstream/internal/torrent"
)

// TestIndexWithCueFile — раздача вида «альбом одним FLAC + cue-файл»:
// треки альбома лежат внутри одного файла, границы берём из cue.
func TestIndexWithCueFile(t *testing.T) {
	if testing.Short() {
		t.Skip("нужна сеть (локальные пиры)")
	}
	sample := filepath.Join("testdata", "sample.flac")
	if _, err := os.Stat(sample); err != nil {
		t.Skip("нет тестового FLAC")
	}
	flacData, err := os.ReadFile(sample)
	if err != nil {
		t.Fatal(err)
	}
	// Тестовый FLAC длится ~70 с, поэтому треки ставим на 0/20/40 с.
	const bytesPerSample = 4 // стерео, 16 бит
	cue := "REM GENRE Metal\n" +
		"PERFORMER \"Test Artist\"\n" +
		"TITLE \"Test Album\"\n" +
		"FILE \"Test Album.flac\" WAVE\n" +
		"  TRACK 01 AUDIO\n    TITLE \"First Track\"\n    INDEX 01 00:00:00\n" +
		"  TRACK 02 AUDIO\n    TITLE \"Second Track\"\n    INDEX 01 00:20:00\n" +
		"  TRACK 03 AUDIO\n    TITLE \"Third Track\"\n    INDEX 01 00:40:00\n"

	ctx := context.Background()
	tmp, err := os.MkdirTemp("", "rutstream-cue-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)

	albumName := "Artist - Album (2026) [FLAC]"
	files := []testutil.GeneratedFile{
		{Path: "Test Album.cue", Size: int64(len(cue))},
		{Path: "Test Album.flac", Size: int64(len(flacData))},
	}
	// Исходники содержимого — отдельно от каталога раздачи.
	cueSrc := filepath.Join(tmp, "cue-src.txt")
	if err := os.WriteFile(cueSrc, []byte(cue), 0o644); err != nil {
		t.Fatal(err)
	}
	mi, err := testutil.MakeTorrentWithContent(albumName, files, 128*1024, tmp, map[string]string{
		"Test Album.cue":  cueSrc,
		"Test Album.flac": sample,
	})
	if err != nil {
		t.Fatal(err)
	}
	info, err := mi.UnmarshalInfo()
	if err != nil {
		t.Fatal(err)
	}

	// Сидер.
	seedDir := filepath.Join(tmp, "seed")
	if err := copyTree(filepath.Join(tmp, albumName), filepath.Join(seedDir, albumName)); err != nil {
		t.Fatal(err)
	}
	seedEngine, err := torrent.New(torrent.Options{
		DataDir: seedDir, DisableDHT: true, DisablePEX: true, DisableWebseeds: true,
		ListenHost: "127.0.0.1", Seed: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer seedEngine.Close()
	seedSession, err := seedEngine.Start(ctx, mi, torrent.StartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := seedSession.Torrent().VerifyData(); err != nil {
		t.Fatal(err)
	}
	dl := time.Now().Add(30 * time.Second)
	for !seedSession.Stats().Complete && time.Now().Before(dl) {
		time.Sleep(100 * time.Millisecond)
	}

	// Личер.
	leecherEngine, err := torrent.New(torrent.Options{
		DataDir: filepath.Join(tmp, "leecher"), DisableDHT: true, DisablePEX: true,
		DisableWebseeds: true, ListenHost: "127.0.0.1", Seed: true,
		StorageMode: "ram", MemLimit: 64 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer leecherEngine.Close()
	session, err := leecherEngine.Start(ctx, mi, torrent.StartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if session.AddClientPeer(seedEngine.Client()) == 0 {
		t.Fatal("нет пира")
	}
	defer session.Close()

	builder := &IndexBuilder{Session: session, Info: &info}
	ix, err := builder.Build(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("треков в индексе: %d, диагностика: %q", len(ix.Tracks), ix.Diag)
	for i, tr := range ix.Tracks {
		t.Logf("  %d. %q [%d..%d) %s", i+1, tr.Title, tr.ByteStart, tr.ByteEnd, tr.Source)
	}
	if len(ix.Tracks) != 3 {
		t.Fatalf("треков = %d, ожидалось 3", len(ix.Tracks))
	}
	if ix.Tracks[0].Title != "First Track" {
		t.Errorf("название = %q", ix.Tracks[0].Title)
	}
	// Границы треков возрастают, каждый трек — свой диапазон.
	for i := 1; i < len(ix.Tracks); i++ {
		if ix.Tracks[i].ByteStart <= ix.Tracks[i-1].ByteStart {
			t.Errorf("границы не возрастают на треке %d", i)
		}
	}
	// Трек внутри альбома — существенно меньше самого файла.
	trackBytes := ix.Tracks[1].ByteEnd - ix.Tracks[1].ByteStart
	if trackBytes >= int64(len(flacData)) {
		t.Errorf("трек занимает весь файл (%d из %d) — границы cue не применились", trackBytes, len(flacData))
	}
	// План загрузки трека — свои piece'ы, а не весь альбом.
	plan, err := mediaindex.PlanForFile(&info, ix.Tracks[1].FileIdx)
	if err != nil {
		t.Fatal(err)
	}
	trackRange := mediaindex.PieceRangeFor(ix.Tracks[1].ByteStart, ix.Tracks[1].ByteEnd, info.PieceLength)
	if trackRange.Len() >= plan.PieceEnd-plan.PieceStart {
		t.Errorf("план трека (%d piece'ов) не меньше плана файла (%d)",
			trackRange.Len(), plan.PieceEnd-plan.PieceStart)
	}
}
