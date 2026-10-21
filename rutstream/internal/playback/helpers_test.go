package playback

import (
	"encoding/binary"
	"fmt"
	"os"
	"testing"

	"github.com/mixa7/rutstream/internal/audio"
)

// decodeLocal декодирует локальный аудиофайл в PCM.
func decodeLocal(t *testing.T, path string) ([]byte, audio.Format, error) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		return nil, audio.Format{}, err
	}
	defer f.Close()
	pcm, format, err := audio.DecodeAll(f, "flac")
	return pcm, format, err
}

// decodeWAV читает PCM из WAV-файла (всего для проверки совпадения).
func decodeWAV(t *testing.T, path string) ([]byte, audio.Format, error) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, audio.Format{}, err
	}
	if len(data) < 44 {
		return nil, audio.Format{}, fmt.Errorf("WAV слишком короткий")
	}
	channels := int(binary.LittleEndian.Uint16(data[22:24]))
	rate := int(binary.LittleEndian.Uint32(data[24:28]))
	bits := int(binary.LittleEndian.Uint16(data[34:36]))
	// Ищем блок data.
	pos := 12
	for pos+8 <= len(data) {
		id := string(data[pos : pos+4])
		size := int(binary.LittleEndian.Uint32(data[pos+4 : pos+8]))
		if id == "data" {
			end := pos + 8 + size
			if end > len(data) {
				end = len(data)
			}
			return data[pos+8 : end], audio.Format{
				SampleRate: rate, Channels: channels, BitsPerSample: bits,
			}, nil
		}
		pos += 8 + size + size%2
	}
	return nil, audio.Format{}, fmt.Errorf("блок data не найден")
}

// humanBytes форматирует размер.
func humanBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.2f МБ", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f КБ", float64(n)/1024)
	}
	return fmt.Sprintf("%d Б", n)
}
