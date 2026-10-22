package sniff

import (
	"bytes"
	"archive/zip"
	"io"
	"testing"
)

// TestProbeZip проверяет разбор ZIP-архива: границы записей и смещения данных.
func TestProbeZip(t *testing.T) {
	// Собираем тестовый архив в памяти.
	//
	// Порядок имён фиксирован: итерация по map даёт случайный порядок,
	// а тест проверяет, что смещения записей возрастают.
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	files := []struct {
		name    string
		content string
	}{
		{"01 - First.flac", "aaaa"},
		{"02 - Second.flac", "bbbbbb"},
		{"cover.jpg", "jpg"},
	}
	for _, f := range files {
		// Метод Store: данные в архиве идут без сжатия — так проще проверить смещения.
		w, err := zw.CreateHeader(&zip.FileHeader{Name: f.name, Method: zip.Store})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(f.content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	data := buf.Bytes()

	entries, err := ProbeZip(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("записей = %d, ожидалось 3: %+v", len(entries), entries)
	}

	byName := map[string]ZipEntry{}
	for _, e := range entries {
		byName[e.Name] = e
	}
	first, ok := byName["01 - First.flac"]
	if !ok {
		t.Fatalf("нет записи 01 - First.flac: %+v", entries)
	}
	if !first.IsAudio {
		t.Error("запись не распознана как аудио")
	}
	if first.UncompressedSize != 4 {
		t.Errorf("размер = %d, ожидалось 4", first.UncompressedSize)
	}
	// Данные по найденному смещению должны совпасть с исходными.
	got := make([]byte, 4)
	if _, err := bytes.NewReader(data).ReadAt(got, first.DataOffset); err != nil {
		t.Fatal(err)
	}
	if string(got) != "aaaa" {
		t.Errorf("данные по смещению %d = %q, ожидалось \"aaaa\"", first.DataOffset, got)
	}
	// Второй файл — со смещением после первого.
	second := byName["02 - Second.flac"]
	if second.DataOffset <= first.DataOffset {
		t.Errorf("смещения не возрастают: %d <= %d", second.DataOffset, first.DataOffset)
	}
	got2 := make([]byte, 6)
	if _, err := bytes.NewReader(data).ReadAt(got2, second.DataOffset); err != nil {
		t.Fatal(err)
	}
	if string(got2) != "bbbbbb" {
		t.Errorf("данные второго файла = %q", got2)
	}
}

// TestProbeZipNotArchive проверяет, что не-ZIP не распознаётся.
func TestProbeZipNotArchive(t *testing.T) {
	data := []byte("это не архив, а просто текст")
	if _, err := ProbeZip(bytes.NewReader(data), int64(len(data))); err == nil {
		t.Error("ожидалась ошибка для не-ZIP данных")
	}
}

// TestProbeZipLargeComment проверяет поиск EOCD при большом комментарии.
func TestProbeZipLargeComment(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("track.flac")
	io.WriteString(w, "данные")
	zw.Close()
	data := buf.Bytes()

	entries, err := ProbeZip(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("записей = %d", len(entries))
	}
}
