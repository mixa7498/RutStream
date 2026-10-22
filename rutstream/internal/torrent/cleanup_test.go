package torrent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mixa7/rutstream/internal/testutil"
)

// TestCleanupIncompleteFiles — проверка уборки «дырявых» файлов-соседей.
//
// anacrolix при записи piece'а, пересекающего границу файлов, создаёт все
// затронутые файлы целиком (полного размера), но заполняет только свою часть.
// Рядом с нужным треком остаются «дырявые» соседи — файлы правильного
// размера, но с пропусками вместо данных.
//
// Функция CleanupIncompleteFiles должна такие файлы удалять, а полностью
// скачанные — не трогать.
func TestCleanupIncompleteFiles(t *testing.T) {
	if testing.Short() {
		t.Skip("нужна сеть (локальные пиры)")
	}
	ctx := context.Background()
	tmp, err := os.MkdirTemp("", "rutstream-clean-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)

	const fileCount = 6
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

	// Сидер.
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

	// Личер: качаем только файл №3.
	dataDir := filepath.Join(tmp, "leecher")
	leecherEngine, err := New(Options{
		DataDir: dataDir, DisableDHT: true, DisablePEX: true,
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
	const target = 3
	if err := leecher.SetWantedFiles([]int{target}); err != nil {
		t.Fatal(err)
	}
	r, err := leecher.OpenFile(target)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 64*1024)
		for {
			if _, err := r.Read(buf); err != nil {
				return
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(60 * time.Second):
		t.Fatal("таймаут чтения файла")
	}
	_ = r.Close()

	// Нужный файл должен быть на диске и полного размера.
	wantPath := filepath.Join(dataDir, name, filepath.FromSlash(files[target].Path))
	st, err := os.Stat(wantPath)
	if err != nil {
		t.Fatalf("нужный файл не найден: %v", err)
	}
	if st.Size() != fileSize {
		t.Errorf("размер нужного файла = %d, ожидалось %d", st.Size(), fileSize)
	}

	// Имитируем «дырявого» соседа: файл полного размера, но с пропусками.
	neighbour := filepath.Join(dataDir, name, filepath.FromSlash(files[target+1].Path))
	if err := os.MkdirAll(filepath.Dir(neighbour), 0o755); err != nil {
		t.Fatal(err)
	}
	hole := make([]byte, fileSize)
	for i := range hole {
		hole[i] = 0
	}
	if err := os.WriteFile(neighbour, hole, 0o644); err != nil {
		t.Fatal(err)
	}

	// Очистка: дырявый сосед должен исчезнуть, нужный файл — остаться.
	removed := leecher.CleanupIncompleteFiles()
	if _, err := os.Stat(neighbour); !os.IsNotExist(err) {
		t.Errorf("дырявый файл не удалён (удалено: %v)", removed)
	}
	if _, err := os.Stat(wantPath); err != nil {
		t.Errorf("нужный файл удалён: %v", err)
	}
}
