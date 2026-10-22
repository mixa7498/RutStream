// Package download — очередь загрузок: трек, альбом или целиком дискография
// исполнителя, с контролем трафика и рейтинга (Ratio Guard).
package download

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/mixa7/rutstream/internal/mediaindex"
)

// Kind — что именно качаем.
type Kind string

const (
	KindTrack       Kind = "track"
	KindAlbum       Kind = "album"
	KindDiscography Kind = "discography"
)

// State — состояние задания.
type State string

const (
	StateQueued     State = "queued"
	StateRunning    State = "running"
	StateDone       State = "done"
	StateFailed     State = "failed"
	StateCancelled  State = "cancelled"
)

// Task — задание на загрузку.
type Task struct {
	ID       string `json:"id"`
	Kind     Kind   `json:"kind"`
	TopicID  int64  `json:"topicId"`
	Title    string `json:"title"`
	InfoHash string `json:"infoHash"`
	// FileIdxs — индексы файлов раздачи, которые нужно скачать.
	FileIdxs []int `json:"fileIdxs"`
	// TrackNo — номер трека (для KindTrack).
	TrackNo int `json:"trackNo"`
	// Priority — меньше значит раньше.
	Priority int `json:"priority"`

	State      State  `json:"state"`
	BytesDone  int64  `json:"bytesDone"`
	BytesTotal int64  `json:"bytesTotal"`
	Error      string `json:"error"`
	CreatedAt  time.Time `json:"createdAt"`
	StartedAt  time.Time `json:"startedAt"`
	FinishedAt time.Time `json:"finishedAt"`

	// PartialFiles — файлы, созданные частично (соседи по границам piece'ов).
	// Их нужно удалить после закрытия клиента.
	PartialFiles []string `json:"-"`

	// lastDone — предыдущее значение счётчика (для учёта трафика).
	lastDone int64
}

// Progress возвращает процент выполнения (0..100).
func (t *Task) Progress() float64 {
	if t.BytesTotal <= 0 {
		return 0
	}
	p := 100 * float64(t.BytesDone) / float64(t.BytesTotal)
	if p > 100 {
		p = 100
	}
	return p
}

// Fetcher — функция, выполняющая загрузку задания.
// Реализуется слоем Torrent Engine.
type Fetcher interface {
	Fetch(ctx context.Context, task *Task, progress func(done, total int64)) error
}

// Manager — очередь загрузок.
type Manager struct {
	mu      sync.Mutex
	tasks   []*Task
	seq     int
	fetcher Fetcher
	log     *slog.Logger
	// maxConcurrent — сколько заданий качать одновременно.
	maxConcurrent int
	// Budget — лимит трафика на сессию в байтах (0 = без лимита).
	budget int64
	spent  int64
	// RatioGuard — минимально допустимый рейтинг (0 = выключен).
	RatioGuard float64
}

// NewManager создаёт очередь.
func NewManager(f Fetcher, log *slog.Logger) *Manager {
	if log == nil {
		log = slog.Default()
	}
	return &Manager{
		fetcher:       f,
		log:           log,
		maxConcurrent: 1,
	}
}

// Add ставит задание в очередь.
func (m *Manager) Add(task Task) *Task {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seq++
	task.ID = fmt.Sprintf("dl-%d", m.seq)
	task.State = StateQueued
	task.CreatedAt = time.Now()
	if task.Priority == 0 {
		task.Priority = 100
	}
	m.tasks = append(m.tasks, &task)
	// Копия, чтобы вызывающий не менял состояние задания извне.
	out := task
	return &out
}

// Tasks возвращает снимок очереди (по приоритету, затем по времени добавления).
func (m *Manager) Tasks() []Task {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Task, 0, len(m.tasks))
	for _, t := range m.tasks {
		out = append(out, *t)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Priority != out[j].Priority {
			return out[i].Priority < out[j].Priority
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out
}

// Cancel отменяет задание.
func (m *Manager) Cancel(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, t := range m.tasks {
		if t.ID == id {
			if t.State == StateDone {
				return fmt.Errorf("задание уже завершено")
			}
			t.State = StateCancelled
			t.FinishedAt = time.Now()
			return nil
		}
	}
	return fmt.Errorf("задание %s не найдено", id)
}

// SetBudget задаёт лимит трафика на сессию (байт; 0 = без лимита).
func (m *Manager) SetBudget(bytes int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.budget = bytes
}

// Spent возвращает израсходованный трафик.
func (m *Manager) Spent() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.spent
}

// BudgetLeft возвращает остаток лимита трафика (-1 — лимита нет).
func (m *Manager) BudgetLeft() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.budget <= 0 {
		return -1
	}
	left := m.budget - m.spent
	if left < 0 {
		return 0
	}
	return left
}

// Run обрабатывает очередь до завершения (или отмены контекста).
//
// Возвращает управление, когда все задания завершены (успешно, с ошибкой
// или отменой) и новых не добавлено.
func (m *Manager) Run(ctx context.Context) error {
	for {
		task := m.next()
		if task == nil {
			// Очередь пуста или всё выполняется — проверяем, есть ли незавершённые.
			if m.finished() {
				return nil
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(500 * time.Millisecond):
				continue
			}
		}
		m.mu.Lock()
		if left := m.BudgetLeftLocked(); left == 0 {
			m.mu.Unlock()
			m.log.Info("достигнут лимит трафика, очередь приостановлена")
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(5 * time.Second):
			}
			continue
		}
		task.State = StateRunning
		task.StartedAt = time.Now()
		m.mu.Unlock()

		err := m.fetcher.Fetch(ctx, task, func(done, total int64) {
			m.mu.Lock()
			task.BytesDone = done
			task.BytesTotal = total
			m.spent += done - task.lastDone
			task.lastDone = done
			m.mu.Unlock()
		})

		m.mu.Lock()
		task.FinishedAt = time.Now()
		if err != nil {
			task.State = StateFailed
			task.Error = err.Error()
			m.log.Error("задание не выполнено", "id", task.ID, "err", err)
		} else {
			task.State = StateDone
			m.log.Info("задание выполнено", "id", task.ID, "bytes", task.BytesDone)
		}
		m.mu.Unlock()
	}
}

// BudgetLeftLocked возвращает остаток лимита (вызывается под мьютексом).
func (m *Manager) BudgetLeftLocked() int64 {
	if m.budget <= 0 {
		return -1
	}
	left := m.budget - m.spent
	if left < 0 {
		return 0
	}
	return left
}

// next выбирает следующее задание к выполнению.
func (m *Manager) next() *Task {
	m.mu.Lock()
	defer m.mu.Unlock()
	running := 0
	for _, t := range m.tasks {
		if t.State == StateRunning {
			running++
		}
	}
	if running >= m.maxConcurrent {
		return nil
	}
	ordered := make([]*Task, 0, len(m.tasks))
	for _, t := range m.tasks {
		if t.State == StateQueued {
			ordered = append(ordered, t)
		}
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].Priority != ordered[j].Priority {
			return ordered[i].Priority < ordered[j].Priority
		}
		return ordered[i].CreatedAt.Before(ordered[j].CreatedAt)
	})
	if len(ordered) == 0 {
		return nil
	}
	return ordered[0]
}

// finished сообщает, что все задания в очереди завершены.
func (m *Manager) finished() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.tasks) == 0 {
		return false
	}
	for _, t := range m.tasks {
		switch t.State {
		case StateQueued, StateRunning:
			return false
		}
	}
	return true
}

// AlbumFiles возвращает индексы файлов альбома внутри раздачи.
//
// Альбом определяется по общему префиксу пути (папка «Artist - Album (Year)/»).
func AlbumFiles(info *metainfo.Info, album string, all []int) []int {
	if album == "" {
		return all
	}
	needle := normalizePath(album)
	files := mediaindex.FilesInfo(info)
	var out []int
	for _, i := range all {
		if i < 0 || i >= len(files) {
			continue
		}
		path := normalizePath(mediaindex.FilePath(files[i]))
		if strings.Contains(path, needle) {
			out = append(out, i)
		}
	}
	if len(out) == 0 {
		return all
	}
	return out
}

func lastSlash(s string) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '/' {
			return i
		}
	}
	return -1
}

func normalizePath(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "\\", "/")
	return s
}
