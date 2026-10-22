// Package audio — декодирование аудиопотока (FLAC/MP3/WAV) в PCM и вывод
// в устройство или файл.
package audio

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/hajimehoshi/go-mp3"
	"github.com/mewkiz/flac"
	"github.com/mewkiz/flac/frame"
)

// Format — параметры аудиопотока.
type Format struct {
	SampleRate    int
	Channels      int
	BitsPerSample int
}

// ErrUnsupported — формат не поддерживается.
var ErrUnsupported = errors.New("audio: формат не поддерживается")

// Decoder — потоковый декодер, выдающий PCM в формате s16le (интерливинг).
type Decoder struct {
	src    io.Reader
	format Format
	kind   string

	flacStream *flac.Stream
	mp3Dec     *mp3.Decoder

	// Буфер для накопления сэмплов FLAC.
	pcm []byte
	pos int
	eof bool

	// skipSamples — сколько сэмплов пропустить в начале (для треков внутри
	// альбома с cue-разметкой: до нужного трека надо декодировать предыдущие).
	skipSamples int64
	// skipped — сколько уже пропущено.
	skipped int64
	// limitSamples — сколько сэмплов выдать (0 — без ограничения).
	// Нужно, чтобы остановить декодирование на границе трека: тогда поток
	// перестанет читать, и лишние piece'ы не будут скачиваться.
	limitSamples int64
	// produced — сколько сэмплов выдано.
	produced int64
	// bytesPerSample — размер одного сэмпла в байтах (каналы × 2).
	bytesPerSample int
}

// Open создаёт декодер, определяя формат по сигнатуре.
func Open(r io.Reader) (*Decoder, error) {
	head := make([]byte, 12)
	n, err := io.ReadFull(r, head)
	if err != nil && n == 0 {
		return nil, err
	}
	head = head[:n]
	prefixed := io.MultiReader(strings.NewReader(string(head)), r)

	switch {
	case len(head) >= 4 && string(head[0:4]) == "fLaC":
		return OpenFormat(prefixed, "flac")
	case len(head) >= 3 && string(head[0:3]) == "ID3":
		return OpenFormat(prefixed, "mp3")
	case len(head) >= 2 && head[0] == 0xFF && head[1]&0xE0 == 0xE0:
		return OpenFormat(prefixed, "mp3")
	case len(head) >= 4 && string(head[0:4]) == "RIFF":
		return OpenFormat(prefixed, "wav")
	case len(head) >= 4 && string(head[0:4]) == "OggS":
		return nil, fmt.Errorf("%w: ogg (требуется ffmpeg)", ErrUnsupported)
	}
	return nil, fmt.Errorf("%w: сигнатура не распознана", ErrUnsupported)
}

// OpenFormat создаёт декодер по имени формата ("flac", "mp3", "wav").
func OpenFormat(r io.Reader, format string) (*Decoder, error) {
	switch strings.ToLower(strings.TrimPrefix(format, ".")) {
	case "flac":
		st, err := flac.New(r)
		if err != nil {
			return nil, fmt.Errorf("flac: %w", err)
		}
		info := st.Info
		return &Decoder{
			src:        r,
			kind:       "flac",
			flacStream: st,
			format: Format{
				SampleRate:    int(info.SampleRate),
				Channels:      int(info.NChannels),
				BitsPerSample: int(info.BitsPerSample),
			},
			bytesPerSample: int(info.NChannels) * 2,
		}, nil
	case "mp3":
		dec, err := mp3.NewDecoder(r)
		if err != nil {
			return nil, fmt.Errorf("mp3: %w", err)
		}
		return &Decoder{
			src:    r,
			kind:   "mp3",
			mp3Dec: dec,
			format: Format{
				SampleRate:    dec.SampleRate(),
				Channels:      2,
				BitsPerSample: 16,
			},
		}, nil
	case "wav":
		return nil, fmt.Errorf("%w: wav (требуется ffmpeg)", ErrUnsupported)
	}
	return nil, fmt.Errorf("%w: %s", ErrUnsupported, format)
}

// Format возвращает параметры потока.
func (d *Decoder) Format() Format { return d.format }

// SkipSamples задаёт количество сэмплов, которые нужно пропустить в начале.
//
// Нужно для треков внутри альбома: FLAC — потоковый формат, чтобы дойти до
// третьего трека, надо декодировать (и скачать) предыдущие кадры.
func (d *Decoder) SkipSamples(n int64) {
	if n > 0 {
		d.skipSamples = n
	}
}

// LimitSamples ограничивает количество выдаваемых сэмплов (0 — без ограничения).
//
// Это ключ к выборочной загрузке трека внутри альбома: декодер останавливается
// на границе трека, поток больше не читает файл, и торрент-клиент не качает
// следующие piece'ы.
func (d *Decoder) LimitSamples(n int64) {
	if n > 0 {
		d.limitSamples = n
	}
}

// Kind возвращает тип исходного формата.
func (d *Decoder) Kind() string { return d.kind }

// DurationSec возвращает длительность в секундах (0 — если неизвестно).
func (d *Decoder) DurationSec() float64 {
	if d.kind == "mp3" && d.format.SampleRate > 0 {
		frames := d.mp3Dec.Length()
		if frames > 0 {
			return float64(frames) * 1152 / float64(d.format.SampleRate)
		}
	}
	return 0
}

// Read отдаёт PCM (s16le, интерливинг).
func (d *Decoder) Read(p []byte) (int, error) {
	switch d.kind {
	case "mp3":
		return d.mp3Dec.Read(p)
	case "flac":
		return d.readFLAC(p)
	}
	return 0, ErrUnsupported
}

func (d *Decoder) readFLAC(p []byte) (int, error) {
	for d.pos >= len(d.pcm) {
		if d.eof {
			return 0, io.EOF
		}
		f, err := d.flacStream.ParseNext()
		if err != nil {
			if errors.Is(err, io.EOF) {
				d.eof = true
				return 0, io.EOF
			}
			return 0, err
		}
		nch := len(f.Subframes)
		if nch == 0 {
			continue
		}
		// Число сэмплов берём из NSamples: у CONSTANT-субфрейма в Samples лежит
		// одно значение, а не весь кадр.
		nsamples := f.Subframes[0].NSamples
		if nsamples <= 0 {
			nsamples = len(f.Subframes[0].Samples)
		}
		// Пропуск сэмплов в начале (трек внутри альбома с cue-разметкой):
		// до нужного трека кадры надо декодировать, но в PCM они не попадают.
		from := 0
		if d.skipped < d.skipSamples {
			need := d.skipSamples - d.skipped
			if int64(nsamples) <= need {
				d.skipped += int64(nsamples)
				continue
			}
			from = int(need)
			d.skipped = d.skipSamples
		}
		// Ограничение объёма: трек внутри альбома заканчивается раньше конца
		// файла. Обрезаем кадр, чтобы декодер остановился на границе трека.
		if d.limitSamples > 0 {
			room := d.limitSamples - d.produced
			if room <= 0 {
				d.eof = true
				return 0, io.EOF
			}
			if int64(nsamples-from) > room {
				nsamples = from + int(room)
			}
		}
		d.pcm = frameToPCM(f, from, nsamples)
		d.pos = 0
	}
	n := copy(p, d.pcm[d.pos:])
	d.pos += n
	if d.bytesPerSample > 0 {
		d.produced += int64(n / d.bytesPerSample)
	}
	return n, nil
}

// frameToPCM конвертирует сэмплы кадра в PCM s16le, начиная с индекса from.
func frameToPCM(f *frame.Frame, from, nsamples int) []byte {
	nch := len(f.Subframes)
	out := make([]byte, 0, (nsamples-from)*nch*2)
	var b [2]byte
	for i := from; i < nsamples; i++ {
		for ch := 0; ch < nch; ch++ {
			sf := f.Subframes[ch]
			var v int32
			switch {
			case len(sf.Samples) >= nsamples:
				v = sf.Samples[i]
			case len(sf.Samples) == 1:
				// CONSTANT-субфрейм: одно значение на весь кадр.
				v = sf.Samples[0]
			case len(sf.Samples) > 0:
				v = sf.Samples[i%len(sf.Samples)]
			}
			binary.LittleEndian.PutUint16(b[:], uint16(int16(v)))
			out = append(out, b[0], b[1])
		}
	}
	return out
}
// Close освобождает ресурсы.
func (d *Decoder) Close() error {
	if d.flacStream != nil {
		return d.flacStream.Close()
	}
	return nil
}

// DecodeAll декодирует весь поток в PCM.
func DecodeAll(r io.Reader, format string) ([]byte, Format, error) {
	dec, err := OpenFormat(r, format)
	if err != nil {
		return nil, Format{}, err
	}
	defer dec.Close()
	var out []byte
	buf := make([]byte, 32*1024)
	for {
		n, err := dec.Read(buf)
		if n > 0 {
			out = append(out, buf[:n]...)
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return out, dec.Format(), err
		}
	}
	return out, dec.Format(), nil
}
