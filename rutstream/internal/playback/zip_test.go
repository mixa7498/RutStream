package playback

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mixa7/rutstream/internal/mediaindex"
	"github.com/mixa7/rutstream/internal/testutil"
	"github.com/mixa7/rutstream/internal/torrent"
)

// TestStreamTrackFromZipArchive — сценарий «дискография одним ZIP-архивом».
//
// Раздача содержит ОДИН файл-архив; нужный трек лежит внутри. Приложение должно
// скачать только куски архива с этой записью, распаковать её на лету и декодировать.
func TestStreamTrackFromZipArchive(t *testing.T) {
	if testing.Short() {
		t.Skip("нужна сеть")
	}
	sample := filepath.Join("testdata", "sample.flac")
	if _, err := os.Stat(sample); err != nil {
		t.Skip("нет тестового FLAC")
	}
	flacData, err := os.ReadFile(sample)
	if err != nil {
		t.Fatal(err)
	}

	// Собираем ZIP с одним аудиофайлом и парой «обложек».
	var zbuf bytes.Buffer
	zw := zip.NewWriter(&zbuf)
	w, err := zw.CreateHeader(&zip.FileHeader{Name: "CD1/01 - Real Track.flac", Method: zip.Deflate})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(flacData); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"cover.jpg", "CD1/02 - Info.txt"} {
		f, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		if err != nil {
			t.Fatal(err)
		}
		f.Write([]byte("мелочь"))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	archive := zbuf.Bytes()

	ctx := context.Background()
	tmp, err := os.MkdirTemp("", "rutstream-zip-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)

	albumName := "Artist - Album (2024) [FLAC].zip"
	files := []testutil.GeneratedFile{{Path: albumName, Size: int64(len(archive))}}
	mi, err := testutil.MakeTorrentWithContent(albumName, files, 256*1024, tmp, map[string]string{
		albumName: writeTempFile(t, tmp, archive),
	})
	if err != nil {
		t.Fatal(err)
	}
	info, err := mi.UnmarshalInfo()
	if err != nil {
		t.Fatal(err)
	}

	// Индекс раздачи: треки внутри архива.
	ix, err := mediaindex.Build(&info, mediaindex.Hints{
		ReaderFor: func(fileIdx int) (mediaindex.ReaderAt, error) {
			return bytes.NewReader(archive), nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(ix.Tracks) != 1 {
		t.Fatalf("треков в архиве найдено %d, ожидался 1: %+v", len(ix.Tracks), ix.Tracks)
	}
	tr := ix.Tracks[0]
	if tr.Container != "zip" {
		t.Fatalf("контейнер не определён: %+v", tr)
	}
	t.Logf("трек в архиве: %q, байты [%d,%d) в файле", tr.Title, tr.ByteStart, tr.ByteEnd)

	// Сидер и личер.
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
	defer session.Close()

	// Воспроизводим трек из архива.
	player := New(session)
	wav := filepath.Join(tmp, "from-zip.wav")
	res, err := player.PlayTrack(ctx, Track{
		FileIdx:        0,
		Path:           albumName,
		Codec:          tr.Codec,
		Title:          tr.Title,
		Container:      tr.Container,
		ContainerMethod: tr.ContainerMethod,
		ByteOffset:     tr.ByteStart,
		ByteEnd:        tr.ByteEnd,
	}, wav)
	if err != nil {
		t.Fatal(err)
	}
	if res.Samples == 0 {
		t.Fatal("декодировано 0 сэмплов")
	}
	t.Logf("декодировано %d сэмплов (%.1f с), скачано %s из %s",
		res.Samples, res.DurationSec, humanBytes(res.BytesDownloaded), humanBytes(int64(len(archive))))

	// Сравним с эталонным декодированием локального файла.
	wantPCM, _, err := decodeLocal(t, sample)
	if err != nil {
		t.Fatal(err)
	}
	gotPCM, _, err := decodeWAV(t, wav)
	if err != nil {
		t.Fatal(err)
	}
	if len(gotPCM) != len(wantPCM) {
		t.Fatalf("размер PCM различается: %d != %d", len(gotPCM), len(wantPCM))
	}
	h1 := sha256.Sum256(gotPCM)
	h2 := sha256.Sum256(wantPCM)
	if hex.EncodeToString(h1[:]) != hex.EncodeToString(h2[:]) {
		t.Error("PCM из архива не совпадает с эталонным")
	} else {
		t.Log("PCM совпадает с эталонным (sha256 " + hex.EncodeToString(h1[:])[:16] + ")")
	}
}

// writeTempFile кладёт содержимое во временный файл и возвращает путь.
func writeTempFile(t *testing.T, dir string, data []byte) string {
	t.Helper()
	p := filepath.Join(dir, "archive.bin")
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}
