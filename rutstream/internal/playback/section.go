package playback

import (
	"fmt"
	"io"
)

// seekSection — окно внутри потока с произвольным доступом (аналог io.SectionReader,
// но поверх io.ReadSeeker, а не io.ReaderAt).
type seekSection struct {
	r      io.ReadSeeker
	off    int64
	length int64
	pos    int64
}

// Read читает данные окна.
func (s *seekSection) Read(p []byte) (int, error) {
	if s.pos >= s.length {
		return 0, io.EOF
	}
	if int64(len(p)) > s.length-s.pos {
		p = p[:s.length-s.pos]
	}
	if _, err := s.r.Seek(s.off+s.pos, io.SeekStart); err != nil {
		return 0, err
	}
	n, err := s.r.Read(p)
	s.pos += int64(n)
	if err == nil && s.pos >= s.length {
		err = io.EOF
	}
	return n, err
}

// Seek перематывает окно.
func (s *seekSection) Seek(offset int64, whence int) (int64, error) {
	var abs int64
	switch whence {
	case io.SeekStart:
		abs = offset
	case io.SeekCurrent:
		abs = s.pos + offset
	case io.SeekEnd:
		abs = s.length + offset
	default:
		return 0, fmt.Errorf("неверный whence: %d", whence)
	}
	if abs < 0 {
		return 0, fmt.Errorf("отрицательная позиция: %d", abs)
	}
	if abs > s.length {
		abs = s.length
	}
	s.pos = abs
	return abs, nil
}
