package torrent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mixa7/rutstream/internal/testutil"
)

const mb = 1 << 20

// TestSelectiveFileDownload — ключевая проверка: из мультифайловой раздачи
// скачивается ТОЛЬКО нужный файл, а не вся раздача.
func TestSelectiveFileDownload(t *testing.T) {
	if testing.Short() {
		t.Skip("нужна сеть (локальные пиры)")
	}
	ctx := context.Background()
	// Используем os.MkdirTemp, а не t.TempDir: клиент BitTorrent держит файлы
	// открытыми ещё секунду после закрытия, и t.TempDir падает на очистке.
	tmp, err := os.MkdirTemp("", "rutstream-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)

	const fileCount = 12
	const fileSize = 2 * mb
	name := "Artist - Album (2024) [FLAC]"
	files := make([]testutil.GeneratedFile, 0, fileCount)
	for i := 1; i <= fileCount; i++ {
		files = append(files, testutil.GeneratedFile{
			Path: "CD1/" + twoDigits(i) + " - Track " + twoDigits(i) + ".flac",
			Size: fileSize,
		})
	}
	mi, err := testutil.MakeTorrent(name, files, 256*1024, tmp)
	if err != nil {
		t.Fatal(err)
	}
	info, err := mi.UnmarshalInfo()
	if err != nil {
		t.Fatal(err)
	}
	total := info.TotalLength()
	if total != fileCount*fileSize {
		t.Fatalf("размер раздачи = %d, ожидалось %d", total, fileCount*fileSize)
	}

	// --- сидер ---
	seedDir := filepath.Join(tmp, "seed")
	if err := copyTree(filepath.Join(tmp, name), filepath.Join(seedDir, name)); err != nil {
		t.Fatal(err)
	}
	seedEngine, err := New(Options{
		DataDir: seedDir, DisableDHT: true, DisablePEX: true, DisableWebseeds: true,
		ListenHost: "127.0.0.1", Seed: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer seedEngine.Close()
	seedSession, err := seedEngine.Start(ctx, mi, StartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := seedSession.Torrent().VerifyData(); err != nil {
		t.Fatalf("проверка данных сидера: %v", err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for !seedSession.Stats().Complete && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	if !seedSession.Stats().Complete {
		t.Fatal("сидер не завершил проверку данных")
	}

	// --- личер ---
	leecherEngine, err := New(Options{
		DataDir: filepath.Join(tmp, "leecher"), DisableDHT: true, DisablePEX: true,
		DisableWebseeds: true, ListenHost: "127.0.0.1", Seed: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer leecherEngine.Close()
	leecher, err := leecherEngine.Start(ctx, mi, StartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if n := leecher.AddClientPeer(seedEngine.Client()); n == 0 {
		t.Fatal("не удалось добавить пира")
	}

	// Нужен только файл №7 (индекс 7).
	const target = 7
	if err := leecher.SetWantedFiles([]int{target}); err != nil {
		t.Fatal(err)
	}
	r, err := leecher.OpenFile(target)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	h := sha256.New()
	readDone := make(chan error, 1)
	go func() {
		_, err := io.Copy(h, r)
		readDone <- err
	}()
	select {
	case err := <-readDone:
		if err != nil {
			t.Fatalf("чтение: %v", err)
		}
	case <-time.After(60 * time.Second):
		t.Fatal("таймаут чтения файла")
	}

	// Сверяем содержимое с оригиналом.
	want, err := fileHash(filepath.Join(tmp, name, filepath.FromSlash(files[target].Path)))
	if err != nil {
		t.Fatal(err)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		t.Fatalf("содержимое не совпало: got %s want %s", got, want)
	}

	st := leecher.Stats()
	t.Logf("скачано %d байт из раздачи %d (%.2f %%)", st.BytesDownloaded, total,
		100*float64(st.BytesDownloaded)/float64(total))

	// Должны быть скачаны только piece'ы нужного файла (+ возможно 1 граничный).
	limit := int64(fileSize + 2*256*1024)
	if st.BytesDownloaded > limit {
		t.Errorf("скачано %d байт — ожидалось не более %d (файл %d + запас)", st.BytesDownloaded, limit, fileSize)
	}
	if st.BytesDownloaded < fileSize {
		t.Errorf("скачано %d байт — меньше размера файла %d", st.BytesDownloaded, fileSize)
	}

	// Закрываем всё до очистки временного каталога.
	_ = r.Close()
	_ = leecher.Close()
	_ = leecherEngine.Close()
	_ = seedSession.Close()
	_ = seedEngine.Close()
	time.Sleep(300 * time.Millisecond)
}

func twoDigits(n int) string {
	if n < 10 {
		return "0" + string(rune('0'+n))
	}
	return string(rune('0'+n/10)) + string(rune('0'+n%10))
}

func fileHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

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
