// Package playback — оркестратор воспроизведения: от выбранного трека
// (файл внутри торрента) до звука.
package playback

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"time"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/mixa7/rutstream/internal/audio"
	"github.com/mixa7/rutstream/internal/mediaindex"
	"github.com/mixa7/rutstream/internal/torrent"
)

// Track — описание играемого трека.
type Track struct {
	// FileIdx — индекс файла внутри раздачи.
	FileIdx int
	// Path — путь файла в раздаче.
	Path string
	// Codec — кодек для декодера ("flac", "mp3", …).
	Codec string
	// ByteOffset — смещение внутри файла (для треков внутри одного контейнера).
	ByteOffset int64
	// Title — название трека.
	Title string
}

// Result — итог воспроизведения.
type Result struct {
	Format      audio.Format
	Samples     int
	DurationSec float64
	Output      string
	Device      bool
	// Сколько байт реально скачано за сессию (трафик).
	BytesDownloaded int64
	// Время до первого звука.
	TimeToFirstAudio time.Duration
	// Время от старта до конца трека.
	Elapsed time.Duration
}

// Player воспроизводит треки из активной torrent-сессии.
type Player struct {
	session *torrent.Session
}

// New создаёт плеер поверх сессии.
func New(s *torrent.Session) *Player { return &Player{session: s} }

// Session возвращает сессию.
func (p *Player) Session() *torrent.Session { return p.session }

// PlayTrack воспроизводит трек из раздачи.
//
// outFile — если задано, PCM пишется в WAV-файл вместо звуковой карты.
func (p *Player) PlayTrack(ctx context.Context, track Track, outFile string) (*Result, error) {
	start := time.Now()

	// 1. Ограничиваем загрузку только нужным файлом.
	if err := p.session.SetWantedFiles([]int{track.FileIdx}); err != nil {
		return nil, err
	}
	// 2. Открываем поток файла. Чтение «подтягивает» нужные piece'ы.
	r, err := p.session.OpenFile(track.FileIdx)
	if err != nil {
		return nil, err
	}
	defer r.Close()

	// 3. Если трек начинается не с начала файла — проматываем.
	if track.ByteOffset > 0 {
		if _, err := r.Seek(track.ByteOffset, io.SeekStart); err != nil {
			return nil, fmt.Errorf("перемотка к треку: %w", err)
		}
	}

	// 4. Определяем формат по сигнатуре (первые 4 байта).
	//
	// Важно: после чтения заголовка НЕ делаем Seek(0) — поток торрента
	// не перематывается надёжно. Вместо этого собираем MultiReader.
	head := make([]byte, 4)
	if _, err := io.ReadFull(r, head); err != nil {
		return nil, fmt.Errorf("чтение заголовка: %w", err)
	}
	codec := track.Codec
	switch {
	case string(head) == "fLaC":
		codec = "flac"
	case head[0] == 0xFF && head[1]&0xE0 == 0xE0:
		codec = "mp3"
	case string(head) == "ID3":
		codec = "mp3"
	}
	src := io.MultiReader(bytes.NewReader(head), r)

	// 5. Декодируем и выводим.
	res, err := audio.Play(src, codec, outFile)
	if err != nil {
		return nil, err
	}
	st := p.session.Stats()
	return &Result{
		Format:          res.Format,
		Samples:         res.Samples,
		DurationSec:     res.DurationSec,
		Output:          res.Output,
		Device:          res.Device,
		BytesDownloaded: st.BytesDownloaded,
		TimeToFirstAudio: time.Since(start),
		Elapsed:         time.Since(start),
	}, nil
}

// PlanForTrack готовит piece-план для трека (для оценки трафика).
func PlanForTrack(info *metainfo.Info, track Track) (mediaindex.Plan, error) {
	return mediaindex.PlanForFile(info, track.FileIdx)
}
