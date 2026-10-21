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

// TestSelectiveDownloadInRAM — тот же сценарий, что и на диске, но в памяти:
// данные не пишутся на диск вообще, а после освобождения память пуста.
func TestSelectiveDownloadInRAM(t *testing.T) {
	if testing.Short() {
		t.Skip("нужна сеть (локальные пиры)")
	}
	ctx := context.Background()
	tmp, err := os.MkdirTemp("", "rutstream-ram-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)

	const fileCount = 6
	const fileSize = 1 << 20
	name := "Artist - RAM Album [FLAC]"
	files := make([]testutil.GeneratedFile, 0, fileCount)
	for i := 1; i <= fileCount; i++ {
		files = append(files, testutil.GeneratedFile{
			Path: "CD1/" + twoDigits(i) + " - Track.flac",
			Size: fileSize,
		})
	}
	mi, err := testutil.MakeTorrent(name, files, 256*1024, tmp)
	if err != nil {
		t.Fatal(err)
	}

	// Сидер — на диске.
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
		t.Fatal(err)
	}
	dl := time.Now().Add(30 * time.Second)
	for !seedSession.Stats().Complete && time.Now().Before(dl) {
		time.Sleep(100 * time.Millisecond)
	}

	// Личер — только память, каталог данных пустой.
	leecherDir := filepath.Join(tmp, "leecher")
	leecherEngine, err := New(Options{
		DataDir: leecherDir, DisableDHT: true, DisablePEX: true, DisableWebseeds: true,
		ListenHost: "127.0.0.1", Seed: true,
		StorageMode: "ram", MemLimit: 64 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer leecherEngine.Close()
	session, err := leecherEngine.Start(ctx, mi, StartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if session.AddClientPeer(seedEngine.Client()) == 0 {
		t.Fatal("нет пира")
	}
	if err := session.SetWantedFiles([]int{3}); err != nil {
		t.Fatal(err)
	}
	r, err := session.OpenFile(3)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	h := sha256.New()
	if _, err := io.Copy(h, r); err != nil {
		t.Fatal(err)
	}
	got := hex.EncodeToString(h.Sum(nil))

	want, err := fileHash(filepath.Join(tmp, name, filepath.FromSlash(files[3].Path)))
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("содержимое не совпало: %s != %s", got, want)
	}

	// На диске личера не должно быть ни одного файла раздачи.
	var found []string
	_ = filepath.Walk(leecherDir, func(path string, fi os.FileInfo, err error) error {
		if err == nil && fi != nil && !fi.IsDir() {
			found = append(found, path)
		}
		return nil
	})
	if len(found) > 0 {
		t.Errorf("в режиме RAM на диск попали файлы: %v", found)
	}

	// Память освобождается после Release.
	if used := leecherEngine.MemoryUsed(); used == 0 {
		t.Logf("память уже пуста")
	}
	session.ReleasePieces([]int{12, 13, 14, 15})
	if used := leecherEngine.MemoryUsed(); used > 0 {
		t.Errorf("после освобождения осталось в памяти: %d байт", used)
	}
}
