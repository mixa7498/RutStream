package torrent

import (
	"context"
	"fmt"
	"sync"

	"github.com/anacrolix/generics"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
)

// MemoryStorage — хранилище в оперативной памяти.
//
// Зачем: при потоковом воспроизведении мы скачиваем только нужные piece'ы
// трека. Если держать их в RAM, на SSD не пишется ни байта (главная
// аргументация проекта) и заодно снимается проблема Windows с переименованием
// открытого файла.
//
// Piece'ы хранятся в памяти до явного освобождения (Release) либо пока не
// превышен лимит MemLimit, после чего вытесняются самые старые.
type MemoryStorage struct {
	mu       sync.Mutex
	torrents map[metainfo.Hash]*memTorrent
	limit    int64
	used     int64
}

// NewMemoryStorage создаёт хранилище с лимитом памяти (0 — без лимита).
func NewMemoryStorage(limitBytes int64) *MemoryStorage {
	return &MemoryStorage{
		torrents: map[metainfo.Hash]*memTorrent{},
		limit:    limitBytes,
	}
}

// OpenTorrent открывает раздачу в памяти.
func (s *MemoryStorage) OpenTorrent(ctx context.Context, info *metainfo.Info, infoHash metainfo.Hash) (storage.TorrentImpl, error) {
	if info == nil {
		return storage.TorrentImpl{}, fmt.Errorf("torrent: нет info")
	}
	s.mu.Lock()
	mt, ok := s.torrents[infoHash]
	if !ok {
		mt = &memTorrent{
			info:     info,
			pieceLen: info.PieceLength,
			pieces:   map[int][]byte{},
			done:     map[int]bool{},
			order:    []int{},
		}
		s.torrents[infoHash] = mt
	}
	limit := s.limit
	s.mu.Unlock()

	mt.setLimit(limit)

	return storage.TorrentImpl{
		Piece: func(p metainfo.Piece) storage.PieceImpl {
			return &memPiece{t: mt, idx: int(p.Index())}
		},
		PieceWithHash: func(p metainfo.Piece, _ generics.Option[[]byte]) storage.PieceImpl {
			return &memPiece{t: mt, idx: int(p.Index())}
		},
		Close: func() error { return nil },
	}, nil
}

// Used возвращает занятую память в байтах.
func (s *MemoryStorage) Used() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.used
}

// Release освобождает piece'ы указанной раздачи.
func (s *MemoryStorage) Release(infoHash metainfo.Hash, indices []int) {
	s.mu.Lock()
	mt, ok := s.torrents[infoHash]
	s.mu.Unlock()
	if !ok {
		return
	}
	mt.Release(indices)
}

// memTorrent — данные одной раздачи в памяти.
type memTorrent struct {
	mu       sync.Mutex
	info     *metainfo.Info
	pieceLen int64
	pieces   map[int][]byte
	done     map[int]bool
	order    []int
	limit    int64
	used     int64
}

func (t *memTorrent) setLimit(limit int64) {
	t.mu.Lock()
	t.limit = limit
	t.mu.Unlock()
}

// pieceSize возвращает размер piece'а по его номеру.
func (t *memTorrent) pieceSize(idx int) int64 {
	total := t.info.TotalLength()
	off := int64(idx) * t.pieceLen
	if off >= total {
		return 0
	}
	size := t.pieceLen
	if off+size > total {
		size = total - off
	}
	return size
}

// memPiece — один piece'а в памяти.
type memPiece struct {
	t   *memTorrent
	idx int
}

// ReadAt читает данные piece'а.
func (p *memPiece) ReadAt(b []byte, off int64) (int, error) {
	p.t.mu.Lock()
	defer p.t.mu.Unlock()
	data, ok := p.t.pieces[p.idx]
	if !ok {
		return 0, fmt.Errorf("torrent: piece %d ещё не загружен", p.idx)
	}
	if off < 0 || off >= int64(len(data)) {
		return 0, fmt.Errorf("torrent: смещение %d вне piece %d", off, p.idx)
	}
	n := copy(b, data[off:])
	return n, nil
}

// WriteAt записывает данные piece'а.
func (p *memPiece) WriteAt(b []byte, off int64) (int, error) {
	p.t.mu.Lock()
	defer p.t.mu.Unlock()
	size := int(p.t.pieceSize(p.idx))
	if size == 0 {
		return 0, fmt.Errorf("torrent: некорректный номер piece %d", p.idx)
	}
	data, ok := p.t.pieces[p.idx]
	if !ok {
		data = make([]byte, size)
		p.t.pieces[p.idx] = data
		p.t.used += int64(size)
	}
	if off < 0 || off >= int64(len(data)) {
		return 0, fmt.Errorf("torrent: смещение %d вне piece %d", off, p.idx)
	}
	n := copy(data[off:], b)
	return n, nil
}

// MarkComplete помечает piece как готовый.
func (p *memPiece) MarkComplete() error {
	p.t.mu.Lock()
	defer p.t.mu.Unlock()
	if !p.t.done[p.idx] {
		p.t.done[p.idx] = true
		p.t.order = append(p.t.order, p.idx)
	}
	p.t.evict()
	return nil
}

// MarkNotComplete удаляет данные piece'а.
func (p *memPiece) MarkNotComplete() error {
	p.t.mu.Lock()
	defer p.t.mu.Unlock()
	if data, ok := p.t.pieces[p.idx]; ok {
		p.t.used -= int64(len(data))
		delete(p.t.pieces, p.idx)
	}
	delete(p.t.done, p.idx)
	return nil
}

// Completion сообщает состояние piece'а.
func (p *memPiece) Completion() storage.Completion {
	p.t.mu.Lock()
	defer p.t.mu.Unlock()
	return storage.Completion{Ok: true, Complete: p.t.done[p.idx]}
}

// Release удаляет данные указанных piece'ов (вызывается после проигрывания трека).
func (t *memTorrent) Release(indices []int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, idx := range indices {
		if data, ok := t.pieces[idx]; ok {
			t.used -= int64(len(data))
			delete(t.pieces, idx)
		}
		delete(t.done, idx)
	}
}

// evict вытесняет самые старые готовые piece'ы, если превышен лимит памяти.
func (t *memTorrent) evict() {
	if t.limit <= 0 {
		return
	}
	for t.used > t.limit && len(t.order) > 0 {
		oldest := t.order[0]
		t.order = t.order[1:]
		if data, ok := t.pieces[oldest]; ok {
			t.used -= int64(len(data))
			delete(t.pieces, oldest)
		}
		delete(t.done, oldest)
	}
}
