package audio

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/hajimehoshi/oto"
)

// WAVWriter пишет PCM s16le в WAV-файл.
type WAVWriter struct {
	f          *os.File
	format     Format
	written    int64
	headerSize int64
}

// NewWAVWriter создаёт WAV-файл.
func NewWAVWriter(path string, format Format) (*WAVWriter, error) {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	w := &WAVWriter{f: f, format: format, headerSize: 44}
	if err := w.writeHeader(0); err != nil {
		f.Close()
		return nil, err
	}
	return w, nil
}

func (w *WAVWriter) writeHeader(dataLen int64) error {
	var h [44]byte
	copy(h[0:4], "RIFF")
	binary.LittleEndian.PutUint32(h[4:8], uint32(36+dataLen))
	copy(h[8:12], "WAVE")
	copy(h[12:16], "fmt ")
	binary.LittleEndian.PutUint32(h[16:20], 16)
	binary.LittleEndian.PutUint16(h[20:22], 1) // PCM
	binary.LittleEndian.PutUint16(h[22:24], uint16(w.format.Channels))
	binary.LittleEndian.PutUint32(h[24:28], uint32(w.format.SampleRate))
	byteRate := w.format.SampleRate * w.format.Channels * 2
	binary.LittleEndian.PutUint32(h[28:32], uint32(byteRate))
	binary.LittleEndian.PutUint16(h[32:34], uint16(w.format.Channels*2))
	binary.LittleEndian.PutUint16(h[34:36], 16)
	copy(h[36:40], "data")
	binary.LittleEndian.PutUint32(h[40:44], uint32(dataLen))
	_, err := w.f.WriteAt(h[:], 0)
	return err
}

// Write принимает PCM-данные.
func (w *WAVWriter) Write(p []byte) (int, error) {
	n, err := w.f.Write(p)
	w.written += int64(n)
	return n, err
}

// Close дописывает заголовок с итоговым размером и закрывает файл.
func (w *WAVWriter) Close() error {
	if err := w.writeHeader(w.written); err != nil {
		w.f.Close()
		return err
	}
	return w.f.Close()
}

// PlayResult — итог воспроизведения.
type PlayResult struct {
	Format      Format
	Samples     int
	DurationSec float64
	Output      string
	Device      bool
}

// Play декодирует поток и воспроизводит его.
//
// Если outFile задан — PCM пишется в WAV-файл (используется в тестах и при
// отсутствии звуковой карты). Иначе — вывод через системное устройство.
func Play(r io.Reader, format string, outFile string) (*PlayResult, error) {
	dec, err := OpenFormat(r, format)
	if err != nil {
		return nil, err
	}
	defer dec.Close()
	f := dec.Format()

	var pcm []byte
	buf := make([]byte, 64*1024)
	for {
		n, err := dec.Read(buf)
		if n > 0 {
			pcm = append(pcm, buf[:n]...)
		}
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, err
		}
	}
	bytesPerFrame := f.Channels * 2
	samples := len(pcm) / bytesPerFrame
	res := &PlayResult{Format: f, Samples: samples}
	if f.SampleRate > 0 {
		res.DurationSec = float64(samples) / float64(f.SampleRate)
	}

	if outFile != "" {
		w, err := NewWAVWriter(outFile, f)
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(pcm); err != nil {
			w.Close()
			return nil, err
		}
		if err := w.Close(); err != nil {
			return nil, err
		}
		res.Output = outFile
		return res, nil
	}

	if err := playToDevice(pcm, f); err != nil {
		return res, fmt.Errorf("звуковая карта недоступна: %w", err)
	}
	res.Device = true
	return res, nil
}

// FormatName возвращает человекочитаемое описание формата.
func FormatName(f Format) string {
	switch {
	case f.Channels == 1:
		return fmt.Sprintf("%d Гц, моно", f.SampleRate)
	case f.Channels == 2:
		return fmt.Sprintf("%d Гц, стерео", f.SampleRate)
	default:
		return fmt.Sprintf("%d Гц, %d кан.", f.SampleRate, f.Channels)
	}
}

// playToDevice воспроизводит PCM через системное устройство.
func playToDevice(pcm []byte, f Format) error {
	if f.SampleRate <= 0 || f.Channels <= 0 {
		return fmt.Errorf("некорректный формат потока")
	}
	ctx, err := oto.NewContext(f.SampleRate, f.Channels, 2, 4096)
	if err != nil {
		return err
	}
	defer ctx.Close()
	player := ctx.NewPlayer()
	defer player.Close()
	if _, err := player.Write(pcm); err != nil {
		return err
	}
	return nil
}
