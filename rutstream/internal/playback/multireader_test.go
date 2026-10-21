package playback

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mewkiz/flac"
	"github.com/mixa7/rutstream/internal/testutil"
	"github.com/mixa7/rutstream/internal/torrent"
)

// TestMultiReaderScenario повторяет сценарий PlayTrack:
// читаем 4 байта, затем декодируем через MultiReader(head, r).
func TestMultiReaderScenario(t *testing.T) {
	if testing.Short() {
		t.Skip("нужна сеть")
	}
	sample := filepath.Join("testdata", "sample.flac")
	if _, err := os.Stat(sample); err != nil {
		t.Skip("нет тестового FLAC")
	}
	r, cleanup := torrentReader(t, sample)
	defer cleanup()

	head := make([]byte, 4)
	n, err := io.ReadFull(r, head)
	t.Logf("ReadFull вернул %d байт, err=%v, head=%q", n, err, head)

	src := io.MultiReader(bytes.NewReader(head), r)
	stream, err := flac.New(src)
	if err != nil {
		t.Fatalf("flac.New: %v", err)
	}
	t.Logf("STREAMINFO: rate=%d samples=%d", stream.Info.SampleRate, stream.Info.NSamples)
	for i := 0; i < 5; i++ {
		if _, err := stream.ParseNext(); err != nil {
			t.Fatalf("кадр %d: %v", i, err)
		}
	}
	t.Log("кадры разбираются нормально")
}

// torrentReader поднимает локальную раздачу с одним FLAC-файлом и
// возвращает поток чтения файла из «личера».
func torrentReader(t *testing.T, sample string) (io.ReadCloser, func()) {
	t.Helper()
	ctx := context.Background()
	tmp, err := os.MkdirTemp("", "rutstream-mr2-")
	if err != nil {
		t.Fatal(err)
	}
	albumName := "Artist - Album (2024) [FLAC]"
	files := []testutil.GeneratedFile{{Path: "CD1/01 - Real Track.flac", Size: fileSize(sample)}}
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
	cleanup := func() {
		r.Close()
		session.Close()
		leecherEngine.Close()
		seedSession.Close()
		seedEngine.Close()
		time.Sleep(200 * time.Millisecond)
		os.RemoveAll(tmp)
	}
	return r, cleanup
}
