package testutil

import (
	"bytes"
	"math"
	"os"
	"path/filepath"

	"github.com/mewkiz/flac"
	"github.com/mewkiz/flac/frame"
	"github.com/mewkiz/flac/meta"
)

// MakeFLAC генерирует корректный FLAC-файл с синусоидой.
//
// Нужен как тестовая фикстура: скачанные из сети образцы часто оказываются
// обрезанными (заголовок обещает больше сэмплов, чем есть в файле), и такой
// файл не годится ни для проверки декодера, ни для cue-разметки.
func MakeFLAC(sampleRate, channels, bits, seconds int) []byte {
	total := sampleRate * seconds
	samples := make([]int32, total*channels)
	for i := 0; i < total; i++ {
		// Синусоида 440 Гц + тихий второй тон, чтобы данные не были однообразными.
		v := math.Sin(2*math.Pi*440*float64(i)/float64(sampleRate))*0.6 +
			math.Sin(2*math.Pi*880*float64(i)/float64(sampleRate))*0.2
		s := int32(v * 32767)
		for ch := 0; ch < channels; ch++ {
			samples[i*channels+ch] = s
		}
	}

	info := &meta.StreamInfo{
		BlockSizeMin:  16,
		BlockSizeMax:  4096,
		SampleRate:    uint32(sampleRate),
		NChannels:     uint8(channels),
		BitsPerSample: uint8(bits),
		NSamples:      uint64(total),
	}
	var buf bytes.Buffer
	enc, err := flac.NewEncoder(&buf, info)
	if err != nil {
		return nil
	}
	const blockSize = 4096
	for offset := 0; offset < total; offset += blockSize {
		n := blockSize
		if offset+n > total {
			n = total - offset
		}
		f := &frame.Frame{
			Header: frame.Header{
				HasFixedBlockSize: true,
				BlockSize:         uint16(n),
				SampleRate:        uint32(sampleRate),
				Channels:          frame.ChannelsLR,
				BitsPerSample:     uint8(bits),
			},
		}
		f.Subframes = make([]*frame.Subframe, channels)
		for ch := 0; ch < channels; ch++ {
			cs := make([]int32, n)
			for i := 0; i < n; i++ {
				cs[i] = samples[(offset+i)*channels+ch]
			}
			f.Subframes[ch] = &frame.Subframe{
				SubHeader: frame.SubHeader{Pred: frame.PredVerbatim},
				Samples:   cs,
				NSamples:  n,
			}
		}
		if err := enc.WriteFrame(f); err != nil {
			return nil
		}
	}
	if err := enc.Close(); err != nil {
		return nil
	}
	return buf.Bytes()
}

// WriteTestFLAC генерирует FLAC и кладёт его в файл, возвращая путь.
func WriteTestFLAC(dir, name string, seconds int) (string, []byte) {
	data := MakeFLAC(44100, 2, 16, seconds)
	path := dir + string(os.PathSeparator) + name
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", nil
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", nil
	}
	return path, data
}
