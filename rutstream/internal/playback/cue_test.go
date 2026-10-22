package playback

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mixa7/rutstream/internal/testutil"
	"github.com/mixa7/rutstream/internal/torrent"
)

// TestIndexWithCueFile — раздача вида «альбом одним FLAC + cue-файл»:
// треки альбома лежат внутри одного файла, границы берём из cue.
func TestIndexWithCueFile(t *testing.T) {
	if testing.Short() {
		t.Skip("нужна сеть (локальные пиры)")
	}
	dir := t.TempDir()
	sample, _ := testutil.WriteTestFLAC(dir, "sample.flac", 70)
	flacData, err := os.ReadFile(sample)
	if err != nil {
		t.Fatal(err)
	}
	// Тестовый FLAC длится ~70 с, поэтому треки ставим на 0/10/20 с.
	const bytesPerSample = 4 // стерео, 16 бит
	cue := "REM GENRE Metal\n" +
		"PERFORMER \"Test Artist\"\n" +
		"TITLE \"Test Album\"\n" +
		"FILE \"Test Album.flac\" WAVE\n" +
		"  TRACK 01 AUDIO\n    TITLE \"First Track\"\n    INDEX 01 00:00:00\n" +
		"  TRACK 02 AUDIO\n    TITLE \"Second Track\"\n    INDEX 01 00:10:00\n" +
		"  TRACK 03 AUDIO\n    TITLE \"Third Track\"\n    INDEX 01 00:20:00\n"

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

	// Сколько реально скачается при проигрывании третьего трека.
	before := session.Stats().BytesDownloaded
	builder := &IndexBuilder{Session: session, Info: &info}
	ix, err := builder.Build(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("треков в индексе: %d, диагностика: %q", len(ix.Tracks), ix.Diag)
	for i, tr := range ix.Tracks {
		t.Logf("  %d. %q длительность %.1f с, пропуск %d, лимит %d, оценка %d Б (%s)",
			i+1, tr.Title, tr.DurationSec, tr.SkipSamples, tr.LimitSamples, tr.EstimateBytes, tr.Source)
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
	// Длительность треков — из cue: 10 с каждый, последний — 50 с.
	for i, tr := range ix.Tracks {
		want := 10.0
		if i == 2 {
			want = 50.0 // последний трек тянется до конца альбома (70 с)
		}
		if math.Abs(tr.DurationSec-want) > 0.5 {
			t.Errorf("трек %d: длительность %.1f с, ожидалось %.1f", i+1, tr.DurationSec, want)
		}
	}
	// Пропуск и лимит — в сэмплах.
	if ix.Tracks[1].SkipSamples != 10*44100 {
		t.Errorf("пропуск = %d, ожидалось %d", ix.Tracks[1].SkipSamples, 10*44100)
	}
	if ix.Tracks[1].LimitSamples != 10*44100 {
		t.Errorf("лимит = %d, ожидалось %d", ix.Tracks[1].LimitSamples, 10*44100)
	}
	// Оценка объёма — пропорционально доле трека в альбоме.
	if ix.Tracks[1].EstimateBytes <= 0 || ix.Tracks[1].EstimateBytes >= int64(len(flacData)) {
		t.Errorf("оценка объёма = %d (файл %d)", ix.Tracks[1].EstimateBytes, len(flacData))
	}

	t.Logf("после индекса: скачано %d байт, готово piece'ов %d",
		session.Stats().BytesDownloaded-before, session.Stats().PiecesComplete)

	// Проверяем, тянет ли клиент остаток файла сам по себе (без чтения).
	//
	// Сначала даём затухнуть запросам, отправленным при чтении заголовка
	// (окно опережающего чтения), и только потом меряем «фоновый» трафик.
	time.Sleep(500 * time.Millisecond)
	if err := session.SetWantedFiles(nil); err != nil {
		t.Fatal(err)
	}
	idleFrom := session.Stats().BytesDownloaded
	time.Sleep(700 * time.Millisecond)
	idle := session.Stats().BytesDownloaded - idleFrom
	if idle > 64*1024 {
		t.Errorf("без чтения скачано %d байт — куски тянутся сами по себе", idle)
	}

	// Проигрываем трек №2 и смотрим, сколько данных скачалось.
	player := New(session)
	wav := filepath.Join(tmp, "out.wav")
	res, err := player.PlayTrack(ctx, Track{
		FileIdx:      ix.Tracks[1].FileIdx,
		Path:         "Test Album.flac",
		Codec:        "flac",
		Title:        ix.Tracks[1].Title,
		SkipSamples:  ix.Tracks[1].SkipSamples,
		LimitSamples: ix.Tracks[1].LimitSamples,
	}, wav)
	if err != nil {
		t.Fatal(err)
	}
	downloaded := session.Stats().BytesDownloaded - before
	t.Logf("после проигрывания: piece'ов готово %d из %d, скачано %d, прочитано %d",
		session.Stats().PiecesComplete, info.NumPieces(), downloaded, res.BytesRead)
	t.Logf("трек %q: %d сэмплов (%.1f с), скачано %d байт (файл %d байт)",
		ix.Tracks[1].Title, res.Samples, res.DurationSec, downloaded, len(flacData))
	// Звук должен быть ровно длительностью трека (10 с).
	if math.Abs(res.DurationSec-10) > 0.5 {
		t.Errorf("длительность звука %.1f с, ожидалось 10", res.DurationSec)
	}
	// Скачано должно быть заметно меньше всего альбома: трек №2 заканчивается
	// на 20-й секунде из 70.
	if downloaded >= int64(len(flacData)) {
		t.Errorf("скачан весь файл (%d) — ограничение по объёму не сработало", downloaded)
	}
	if downloaded > int64(len(flacData))*60/100 {
		t.Errorf("скачано %d байт при файле %d — ожидалось заметно меньше", downloaded, len(flacData))
	}
	// Прочитано должно быть примерно до конца трека плюс запас.
	if res.BytesRead > int64(len(flacData))*40/100 {
		t.Errorf("прочитано %d байт при файле %d — ожидалось около трети", res.BytesRead, len(flacData))
	}
}
