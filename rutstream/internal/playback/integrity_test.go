package playback

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mixa7/rutstream/internal/testutil"
	"github.com/mixa7/rutstream/internal/torrent"
)

// TestMultiReaderIntegrity проверяет, что поток «4 байта + MultiReader»
// отдаёт ровно те же байты, что и исходный файл.
func TestMultiReaderIntegrity(t *testing.T) {
	if testing.Short() {
		t.Skip("нужна сеть")
	}
	sample := filepath.Join("testdata", "sample.flac")
	if _, err := os.Stat(sample); err != nil {
		t.Skip("нет тестового FLAC")
	}
	ctx := context.Background()
	tmp, err := os.MkdirTemp("", "rutstream-int-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)

	albumName := "Artist - Album (2024) [FLAC]"
	files := []testutil.GeneratedFile{
		{Path: "CD1/01 - Real Track.flac", Size: fileSize(sample)},
		{Path: "CD1/02 - Other.flac", Size: 8 << 20},
	}
	mi, err := testutil.MakeTorrentWithContent(albumName, files, 256*1024, tmp, map[string]string{
		"CD1/01 - Real Track.flac": sample,
	})
	if err != nil {
		t.Fatal(err)
	}
	seedDir := filepath.Join(tmp, "seed")
	if err := copyTree(filepath.Join(tmp, albumName), filepath.Join(seedDir, albumName)); err != nil {
		t.Fatal(err)
	}
	seedEngine, err := torrent.New(torrent.Options{DataDir: seedDir, DisableDHT: true, DisablePEX: true,
		DisableWebseeds: true, ListenHost: "127.0.0.1", Seed: true})
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
	dl := time.Now().Add(60 * time.Second)
	for !seedSession.Stats().Complete && time.Now().Before(dl) {
		time.Sleep(100 * time.Millisecond)
	}
	leecherEngine, err := torrent.New(torrent.Options{DataDir: filepath.Join(tmp, "leecher"),
		DisableDHT: true, DisablePEX: true, DisableWebseeds: true, ListenHost: "127.0.0.1", Seed: true})
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
	if err := session.SetWantedFiles([]int{0}); err != nil {
		t.Fatal(err)
	}
	r, err := session.OpenFile(0)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	head := make([]byte, 4)
	if _, err := io.ReadFull(r, head); err != nil {
		t.Fatal(err)
	}
	src := io.MultiReader(bytes.NewReader(head), r)
	h := sha256.New()
	n, err := io.Copy(h, src)
	if err != nil {
		t.Fatalf("чтение через MultiReader: %v (прочитано %d)", err, n)
	}
	got := hex.EncodeToString(h.Sum(nil))

	orig, err := os.ReadFile(sample)
	if err != nil {
		t.Fatal(err)
	}
	oh := sha256.Sum256(orig)
	want := hex.EncodeToString(oh[:])

	t.Logf("прочитано %d байт, ожидалось %d", n, len(orig))
	t.Logf("sha256 через MultiReader: %s", got[:16])
	t.Logf("sha256 исходного файла:  %s", want[:16])
	if got != want {
		t.Fatalf("данные через MultiReader отличаются от исходных")
	}
}
