package audio

import (
	"os"
	"path/filepath"
	"testing"
)

// TestDecodeLocalFLAC проверяет декодер на реальном файле (без торрента).
func TestDecodeLocalFLAC(t *testing.T) {
	path := filepath.Join("testdata", "sample.flac")
	f, err := os.Open(path)
	if err != nil {
		t.Skipf("нет тестового файла: %v", err)
	}
	defer f.Close()

	dec, err := Open(f)
	if err != nil {
		t.Fatal(err)
	}
	defer dec.Close()
	t.Logf("формат: %s, kind=%s", FormatName(dec.Format()), dec.Kind())

	buf := make([]byte, 32*1024)
	total := 0
	for {
		n, err := dec.Read(buf)
		total += n
		if err != nil {
			break
		}
		if total > 50<<20 {
			break
		}
	}
	if total == 0 {
		t.Fatal("декодировано 0 байт")
	}
	t.Logf("декодировано %d байт PCM", total)
}
