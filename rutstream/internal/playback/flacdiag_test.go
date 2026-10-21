package playback

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mewkiz/flac"
	"github.com/mixa7/rutstream/internal/testutil"
	"github.com/mixa7/rutstream/internal/torrent"
)

// TestFLACParseFromTorrent диагностика: FLAC-парсер поверх потока торрента.
func TestFLACParseFromTorrent(t *testing.T) {
	if testing.Short() {
		t.Skip("нужна сеть")
	}
	sample := filepath.Join("testdata", "sample.flac")
	if _, err := os.Stat(sample); err != nil {
		t.Skip("нет тестового FLAC")
	}
	ctx := context.Background()
	tmp, err := os.MkdirTemp("", "rutstream-flac-")
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

	// Считаем позицию вручную, чтобы знать, где произошла ошибка.
	counter := &countingReader{r: r}
	stream, err := flac.New(counter)
	if err != nil {
		t.Fatalf("flac.New: %v (прочитано %d байт)", err, counter.n)
	}
	t.Logf("STREAMINFO: rate=%d ch=%d bits=%d samples=%d (прочитано %d байт)",
		stream.Info.SampleRate, stream.Info.NChannels, stream.Info.BitsPerSample,
		stream.Info.NSamples, counter.n)

	frames := 0
	for {
		_, err := stream.ParseNext()
		if err != nil {
			t.Fatalf("кадр %d: %v (прочитано %d байт)",
				frames, err, counter.n)
		}
		frames++
		if frames%200 == 0 {
			t.Logf("кадров: %d, прочитано %d байт (%.1f %%)", frames, counter.n,
				100*float64(counter.n)/float64(fileSize(sample)))
		}
		if frames > 5000 {
			break
		}
	}
	t.Logf("разобрано кадров: %d", frames)
}

// countingReader считает прочитанные байты.
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

var _ = fmt.Sprintf
