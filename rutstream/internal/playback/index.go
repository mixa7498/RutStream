package playback

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/mixa7/rutstream/internal/mediaindex"
	"github.com/mixa7/rutstream/internal/torrent"
)

// IndexBuilder строит индекс треков раздачи, при необходимости «зондируя»
// содержимое файлов (заголовок контейнера, cue-файл).
//
// Это нужно для однофайловых раздач («FLAC (image+.cue)», «альбом одним .zip»):
// список треков внутри файла известен только из его содержимого.
type IndexBuilder struct {
	Session *torrent.Session
	Info    *metainfo.Info
}

// Build строит индекс раздачи.
//
// Если раздача содержит один аудиофайл, чтение его заголовка инициирует
// докачку нужных piece'ов — специально ничего «скачивать заранее» не нужно.
func (b *IndexBuilder) Build(ctx context.Context) (*mediaindex.Index, error) {
	if b.Session == nil || b.Info == nil {
		return nil, fmt.Errorf("index: нет сессии или info")
	}
	hints := mediaindex.Hints{
		ReaderFor: func(fileIdx int) (mediaindex.ReaderAt, error) {
			r, err := b.Session.OpenFile(fileIdx)
			if err != nil {
				return nil, err
			}
			ra, ok := r.(mediaindex.ReaderAt)
			if !ok {
				return nil, fmt.Errorf("index: поток не поддерживает ReadAt")
			}
			return ra, nil
		},
		CueReader: func() ([]byte, error) {
			return b.readCueFile(ctx)
		},
	}
	return mediaindex.Build(b.Info, hints)
}

// readCueFile читает cue-файл раздачи (если он есть).
func (b *IndexBuilder) readCueFile(ctx context.Context) ([]byte, error) {
	files := b.Info.UpvertedFiles()
	for i, f := range files {
		name := strings.Join(f.BestPath(), "/")
		if !strings.HasSuffix(strings.ToLower(name), ".cue") {
			continue
		}
		r, err := b.Session.OpenFile(i)
		if err != nil {
			return nil, err
		}
		defer r.Close()
		buf := make([]byte, f.Length)
		if _, err := readFull(r, buf); err != nil {
			return nil, err
		}
		return buf, nil
	}
	return nil, nil
}

// readFull читает ровно len(p) байт.
//
// Ридер может вернуть (n, io.EOF) на последнем блоке — данные при этом
// получены, поэтому EOF в этом месте не считается ошибкой.
func readFull(r interface{ Read([]byte) (int, error) }, p []byte) (int, error) {
	total := 0
	for total < len(p) {
		n, err := r.Read(p[total:])
		total += n
		if err != nil {
			if err == io.EOF && total == len(p) {
				return total, nil
			}
			return total, err
		}
		if n == 0 {
			return total, fmt.Errorf("нет данных")
		}
	}
	return total, nil
}
