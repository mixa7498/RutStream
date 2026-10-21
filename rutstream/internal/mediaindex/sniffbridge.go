package mediaindex

import (
	"errors"
	"io"

	"github.com/mixa7/rutstream/internal/mediaindex/sniff"
)

// sniffContainer пытается разобрать контейнер аудиофайла (FLAC/MP3/...) и вернуть
// карту треков. Ошибка не является критичной: вызывающий код просто оставит файл
// как один трек без границ.
func sniffContainer(readerFor func(int) (ReaderAt, error), fileIdx int, size int64) (*sniff.Result, error) {
	if readerFor == nil {
		return nil, errors.New("mediaindex: нет доступа к содержимому")
	}
	r, err := readerFor(fileIdx)
	if err != nil {
		return nil, err
	}
	ra, ok := r.(io.ReaderAt)
	if !ok {
		return nil, errors.New("mediaindex: reader не реализует io.ReaderAt")
	}
	return sniff.Probe(ra, size)
}
