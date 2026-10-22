package playback

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mixa7/rutstream/internal/audio"
	"github.com/mixa7/rutstream/internal/testutil"
	"github.com/mixa7/rutstream/internal/torrent"
)

// TestEndToEndStreamAndPlay — полный сценарий:
// раздача с «альбомом» → выборочная докачка одного трека → декодирование → WAV.
//
// Это доказывает главную идею проекта: трек из большой раздачи можно
// получить и воспроизвести, скачав лишь малую часть её объёма.
func TestEndToEndStreamAndPlay(t *testing.T) {
	if testing.Short() {
		t.Skip("нужна сеть (локальные пиры)")
	}
	// Реальный FLAC-файл: если нет — тест пропускается.
	sample := findSample(t)
	info, err := os.Stat(sample)
	if err != nil {
		t.Skipf("нет тестового FLAC: %v", err)
	}

	ctx := context.Background()
	tmp, err := os.MkdirTemp("", "rutstream-e2e-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)

	// Строим раздачу: 1 настоящий трек + 4 «чужих» файла по 8 МБ.
	albumName := "Artist - Album (2024) [FLAC]"
	files := []testutil.GeneratedFile{
		{Path: "CD1/01 - Real Track.flac", Size: info.Size()},
	}
	for i := 2; i <= 5; i++ {
		files = append(files, testutil.GeneratedFile{
			Path: "CD1/" + string(rune('0'+i)) + " - Other.flac",
			Size: 8 << 20,
		})
	}
	// Копируем настоящий трек в раздачу.
	mi, err := testutil.MakeTorrentWithContent(albumName, files, 256*1024, tmp, map[string]string{
		"CD1/01 - Real Track.flac": sample,
	})
	if err != nil {
		t.Fatal(err)
	}
	miInfo, err := mi.UnmarshalInfo()
	if err != nil {
		t.Fatal(err)
	}
	total := miInfo.TotalLength()
	t.Logf("раздача: %d файлов, %s", len(miInfo.Files), human(total))

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
	deadline := time.Now().Add(60 * time.Second)
	for !seedSession.Stats().Complete && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	if !seedSession.Stats().Complete {
		t.Fatal("сидер не готов")
	}

	// Личер.
	leecherEngine, err := torrent.New(torrent.Options{
		DataDir: filepath.Join(tmp, "leecher"), DisableDHT: true, DisablePEX: true,
		DisableWebseeds: true, ListenHost: "127.0.0.1", Seed: true,
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
		t.Fatal("не удалось добавить пира")
	}

	// Играем трек №0 (настоящий FLAC).
	player := New(session)
	wavPath := filepath.Join(tmp, "out.wav")
	res, err := player.PlayTrack(ctx, Track{
		FileIdx: 0,
		Path:    "CD1/01 - Real Track.flac",
		Codec:   "flac",
		Title:   "Real Track",
	}, wavPath)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("декодировано: %d сэмплов, %.2f с, формат %s",
		res.Samples, res.DurationSec, audio.FormatName(res.Format))
	t.Logf("скачано по сети: %s из %s (%.2f %%)",
		human(res.BytesDownloaded), human(total), 100*float64(res.BytesDownloaded)/float64(total))

	if res.Samples == 0 {
		t.Fatal("декодировано 0 сэмплов")
	}
	if res.Format.SampleRate == 0 {
		t.Error("не определена частота дискретизации")
	}
	// WAV должен существовать и быть непустым.
	if fi, err := os.Stat(wavPath); err != nil || fi.Size() == 0 {
		t.Errorf("WAV не записан: %v", err)
	}

	// Проверяем, что скачано заметно меньше раздачи.
	limit := int64(total/2 + 4<<20)
	if res.BytesDownloaded > limit {
		t.Errorf("скачано %s — слишком много для одного трека из %s", human(res.BytesDownloaded), human(total))
	}

	_ = session.Close()
	_ = leecherEngine.Close()
	_ = seedSession.Close()
	_ = seedEngine.Close()
	time.Sleep(300 * time.Millisecond)
}

// findSample ищет тестовый FLAC в каталоге testdata.
func findSample(t *testing.T) string {
	path, _ := testutil.WriteTestFLAC(t.TempDir(), "sample.flac", 70)
	return path
}

func human(n int64) string {
	switch {
	case n >= 1<<30:
		return fmtMB(n) + " ГБ"
	case n >= 1<<20:
		return fmtMB(n) + " МБ"
	case n >= 1<<10:
		return fmt.Sprintf("%.1f КБ", float64(n)/1024)
	}
	return fmt.Sprintf("%d Б", n)
}

func fmtMB(n int64) string {
	return fmt.Sprintf("%.2f", float64(n)/(1<<20))
}

// copyTree копирует каталог целиком (для сидера).
func copyTree(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		out, err := os.Create(target)
		if err != nil {
			return err
		}
		defer out.Close()
		_, err = io.Copy(out, in)
		return err
	})
}
