package download

import (
	"fmt"
	"sync"
	"time"
)

// Ratio — состояние рейтинга по раздаче.
type Ratio struct {
	TopicID   int64   `json:"topicId"`
	InfoHash  string  `json:"infoHash"`
	Downloaded int64  `json:"downloaded"`
	Uploaded   int64  `json:"uploaded"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

// Value возвращает отношение отданного к скачанному (0 — нет данных).
func (r Ratio) Value() float64 {
	if r.Downloaded <= 0 {
		return 0
	}
	return float64(r.Uploaded) / float64(r.Downloaded)
}

// RatioGuard следит за рейтингом раздач и предупреждает о падении.
//
// Для rutracker это важно: частичное скачивание (наш основной сценарий —
// скачать один трек из раздачи) ухудшает соотношение «скачано/раздано».
type RatioGuard struct {
	mu       sync.Mutex
	ratios   map[string]Ratio
	minRatio float64
	// onWarn вызывается при падении рейтинга ниже порога.
	onWarn func(Ratio)
}

// NewRatioGuard создаёт наблюдатель рейтинга.
func NewRatioGuard(minRatio float64, onWarn func(Ratio)) *RatioGuard {
	return &RatioGuard{
		ratios:   map[string]Ratio{},
		minRatio: minRatio,
		onWarn:   onWarn,
	}
}

// Update обновляет счётчики раздачи и возвращает актуальный рейтинг.
func (g *RatioGuard) Update(topicID int64, infoHash string, downloaded, uploaded int64) Ratio {
	g.mu.Lock()
	defer g.mu.Unlock()
	key := infoHash
	if key == "" {
		key = fmt.Sprintf("t%d", topicID)
	}
	r := g.ratios[key]
	r.TopicID = topicID
	r.InfoHash = infoHash
	r.Downloaded = downloaded
	r.Uploaded = uploaded
	r.UpdatedAt = time.Now()
	g.ratios[key] = r
	if g.onWarn != nil && r.Value() < g.minRatio && r.Downloaded > 0 {
		g.onWarn(r)
	}
	return r
}

// Ratios возвращает снимок всех раздач.
func (g *RatioGuard) Ratios() []Ratio {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]Ratio, 0, len(g.ratios))
	for _, r := range g.ratios {
		out = append(out, r)
	}
	return out
}

// Warning возвращает текст предупреждения, если рейтинг ниже порога.
func (g *RatioGuard) Warning() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	var worst Ratio
	found := false
	for _, r := range g.ratios {
		if r.Downloaded == 0 {
			continue
		}
		if !found || r.Value() < worst.Value() {
			worst = r
			found = true
		}
	}
	if !found || worst.Value() >= g.minRatio {
		return ""
	}
	return fmt.Sprintf("рейтинг раздачи #%d упал до %.2f (нужно ≥ %.2f) — включите раздачу или докачайте раздачу",
		worst.TopicID, worst.Value(), g.minRatio)
}
