package playback

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mixa7/rutstream/internal/testutil"
	"github.com/mixa7/rutstream/internal/torrent"
)

// TestReaderBytesMatchSource проверяет, что поток файла из торрента
// совпадает с исходным содержимым байт в байт.
func TestReaderBytesMatchSource(t *testing.T) {
	if testing.Short() {
		t.Skip("нужна сеть")
	}
	sample := filepath.Join("testdata", "sample.flac")
	if _, err := os.Stat(sample); err != nil {
		t.Skip("нет тестового FLAC")
	}
	ctx := context.Background()
	tmp, err := os.MkdirTemp("", "rutstream-read-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)

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

	// Читаем по 64 КБ и сверяем с источником.
	src, err := os.Open(sample)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()

	buf := make([]byte, 64*1024)
	total := 0
	for {
		n, err := r.Read(buf)
		if n > 0 {
			want := make([]byte, n)
			if _, err := io.ReadFull(src, want); err != nil {
				t.Fatalf("источник закончился на %d байт: %v", total, err)
			}
			if !bytes.Equal(buf[:n], want) {
				t.Fatalf("несовпадение байтов на позиции %d", total)
			}
			total += n
		}
		if err != nil {
			if err == io.EOF {
				break
			}
			t.Fatalf("чтение на %d байт: %v", total, err)
		}
	}
	t.Logf("прочитано %d байт — совпадает с источником", total)
}

func fileSize(p string) int64 {
	fi, err := os.Stat(p)
	if err != nil {
		return 0
	}
	return fi.Size()
}
