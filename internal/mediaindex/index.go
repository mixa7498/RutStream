package mediaindex

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/anacrolix/torrent/metainfo"
)

// Source — откуда взялась информация о треке.
const (
	SourceFileList    = "file-list"    // имя файла в раздаче
	SourceSiteTrack   = "site-tracklist" // треклист со страницы темы
	SourceCue         = "cue"            // файл .cue в раздаче
	SourceSniff       = "sniff"          // разбор контейнера
	SourceSingleFile  = "single-file"    // весь файл целиком
)

// TrackPlan — полное описание трека и того, что нужно скачать, чтобы его получить.
type TrackPlan struct {
	ID          string  `json:"id"`
	Title       string  `json:"title"`
	Artist      string  `json:"artist"`
	Album       string  `json:"album"`
	Disc        int     `json:"disc"`
	Pos         int     `json:"pos"`
	FileIdx     int     `json:"fileIdx"`
	Path        string  `json:"path"`
	ByteStart   int64   `json:"byteStart"`
	ByteEnd     int64   `json:"byteEnd"`
	PieceStart  int     `json:"pieceStart"`
	PieceEnd    int     `json:"pieceEnd"`
	ExtraBytes  int64   `json:"extraBytes"`
	Codec       string  `json:"codec"`
	Lossless    bool    `json:"lossless"`
	BitrateKbps int     `json:"bitrateKbps"`
	SampleRate  int     `json:"sampleRate"`
	BitDepth    int     `json:"bitDepth"`
	DurationSec float64 `json:"durationSec"`
	Confidence  float64 `json:"confidence"`
	Source      string  `json:"source"`
}

// SiteTrack — трек, прочитанный со страницы раздачи на rutracker.
type SiteTrack struct {
	Title       string
	Artist      string
	Album       string
	DurationSec float64
	Pos         int
	Disc        int
}

// Hints — внешние подсказки для построения индекса.
type Hints struct {
	SiteTracks []SiteTrack
	Artist     string
	Album      string
	Year       int
	// ReaderFor даёт доступ к содержимому файла раздачи (для sniff-а).
	// Возвращает nil, если содержимое недоступно.
	ReaderFor func(fileIdx int) (ReaderAt, error)
}

// ReaderAt — минимальный интерфейс чтения (совместим с io.ReaderAt).
type ReaderAt interface {
	ReadAt(p []byte, off int64) (int, error)
}

// Index — индекс раздачи: список треков и карта файлов.
type Index struct {
	Info   *metainfo.Info
	Files  []metainfo.FileInfo
	Tracks []TrackPlan
	// SingleFileHasAudio означает, что в раздаче ровно один аудиофайл
	// (типичный случай «дискография одним файлом»).
	SingleFileHasAudio bool
}

// AudioFileIndexes возвращает индексы аудиофайлов раздачи.
func (ix *Index) AudioFileIndexes() []int {
	var out []int
	for i, f := range ix.Files {
		if IsAudioFile(FilePath(f)) {
			out = append(out, i)
		}
	}
	return out
}

// TrackByFileIdx находит трек по индексу файла.
func (ix *Index) TrackByFileIdx(idx int) (TrackPlan, bool) {
	for _, t := range ix.Tracks {
		if t.FileIdx == idx {
			return t, true
		}
	}
	return TrackPlan{}, false
}

// Build строит индекс раздачи.
//
// Логика:
//   - если аудиофайлов несколько — каждый файл = трек (с уточнением по треклисту сайта);
//   - если аудиофайл один — пытаемся разобрать контейнер (FLAC/MP3/...),
//     чтобы получить границы треков внутри файла; если не вышло — файл = один трек.
func Build(info *metainfo.Info, hints Hints) (*Index, error) {
	if info == nil {
		return nil, fmt.Errorf("metainfo is nil")
	}
	files := FilesInfo(info)
	ix := &Index{Info: info, Files: files}

	audio := make([]int, 0, len(files))
	for i, f := range files {
		if IsAudioFile(FilePath(f)) {
			audio = append(audio, i)
		}
	}

	switch {
	case len(audio) == 0:
		return ix, nil
	case len(audio) == 1:
		ix.SingleFileHasAudio = true
		return ix, buildSingleFile(ix, audio[0], hints)
	default:
		return ix, buildMultiFile(ix, audio, hints)
	}
}

func buildMultiFile(ix *Index, audio []int, hints Hints) error {
	// Сортируем по пути, чтобы порядок треков совпадал с порядком дисков.
	sort.Slice(audio, func(i, j int) bool {
		return strings.ToLower(FilePath(ix.Files[audio[i]])) < strings.ToLower(FilePath(ix.Files[audio[j]]))
	})

	paths := make([]string, len(audio))
	for i, idx := range audio {
		paths[i] = FilePath(ix.Files[idx])
	}

	for i, idx := range audio {
		full := paths[i]
		base := path.Base(full)
		plan, err := PlanForFile(ix.Info, idx)
		if err != nil {
			return err
		}

		tp := TrackPlan{
			ID:         fmt.Sprintf("%s:%d", full, idx),
			Title:      titleFromFileName(base),
			Disc:       discFromPath(full),
			Pos:        trackNumFromPath(full),
			FileIdx:    idx,
			Path:       full,
			ByteStart:  plan.ByteStart,
			ByteEnd:    plan.ByteEnd,
			PieceStart: plan.PieceStart,
			PieceEnd:   plan.PieceEnd,
			ExtraBytes: plan.ExtraBytes,
			Codec:      CodecOf(full),
			Lossless:   IsLosslessExt(full),
			Source:     SourceFileList,
			Confidence: 0.6,
		}

		// Уточняем по треклисту сайта: сначала по номеру трека, затем по совпадению названия.
		if st, ok := matchSiteTrack(tp, hints.SiteTracks); ok {
			if st.Title != "" {
				tp.Title = st.Title
			}
			if st.Artist != "" {
				tp.Artist = st.Artist
			}
			if st.Album != "" {
				tp.Album = st.Album
			}
			if st.DurationSec > 0 {
				tp.DurationSec = st.DurationSec
			}
			if st.Disc > 0 {
				tp.Disc = st.Disc
			}
			if st.Pos > 0 {
				tp.Pos = st.Pos
			}
			tp.Source = SourceSiteTrack
			tp.Confidence = 0.95
		}
		if tp.Artist == "" {
			tp.Artist = hints.Artist
		}
		if tp.Album == "" {
			tp.Album = hints.Album
		}
		ix.Tracks = append(ix.Tracks, tp)
	}
	return nil
}

// matchSiteTrack ищет соответствующий трек в треклисте сайта.
func matchSiteTrack(tp TrackPlan, site []SiteTrack) (SiteTrack, bool) {
	if len(site) == 0 {
		return SiteTrack{}, false
	}
	// 1) Точное совпадение по номеру трека (и диску, если он известен).
	for _, st := range site {
		if st.Pos > 0 && st.Pos == tp.Pos {
			if st.Disc == 0 || tp.Disc == 0 || st.Disc == tp.Disc {
				if s := Score(tp.Title, st.Title); s >= 0.5 {
					return st, true
				}
			}
		}
	}
	// 2) Совпадение по названию.
	titles := make([]string, len(site))
	for i, st := range site {
		titles[i] = st.Title
	}
	idx, score := BestMatch(tp.Title, titles)
	if idx >= 0 && score >= 0.75 {
		return site[idx], true
	}
	return SiteTrack{}, false
}

func buildSingleFile(ix *Index, fileIdx int, hints Hints) error {
	plan, err := PlanForFile(ix.Info, fileIdx)
	if err != nil {
		return err
	}
	full := FilePath(ix.Files[fileIdx])
	tp := TrackPlan{
		ID:         fmt.Sprintf("%s:0", full),
		Title:      titleFromFileName(path.Base(full)),
		FileIdx:    fileIdx,
		Path:       full,
		ByteStart:  plan.ByteStart,
		ByteEnd:    plan.ByteEnd,
		PieceStart: plan.PieceStart,
		PieceEnd:   plan.PieceEnd,
		ExtraBytes: plan.ExtraBytes,
		Codec:      CodecOf(full),
		Lossless:   IsLosslessExt(full),
		Source:     SourceSingleFile,
		Confidence: 0.5,
		Artist:     hints.Artist,
		Album:      hints.Album,
	}

	// Пробуем разобрать контейнер — получим границы треков внутри файла.
	if hints.ReaderFor != nil {
		if res, err := sniffContainer(hints.ReaderFor, fileIdx, ix.Files[fileIdx].Length); err == nil && res != nil {
			tp.Codec = res.Codec
			tp.Lossless = res.Lossless
			tp.SampleRate = res.SampleRate
			tp.BitDepth = res.BitDepth
			tp.BitrateKbps = res.BitrateKbps
			if res.DurationSec > 0 {
				tp.DurationSec = res.DurationSec
			}
			if len(res.Tracks) > 1 {
				// Внутри одного файла несколько треков — размечаем их по смещениям.
				ix.Tracks = nil
				bytesPerSec := float64(res.SampleRate) * float64(res.Channels) * float64(bitDepthOr(res.BitDepth)) / 8
				if bytesPerSec <= 0 {
					bytesPerSec = 44100 * 2 * 2 / 8
				}
				for _, ct := range res.Tracks {
					sub := tp
					sub.ID = fmt.Sprintf("%s:%d", full, ct.Index)
					sub.Title = ct.Title
					sub.ByteStart = plan.ByteStart + ct.Offset
					sub.ByteEnd = plan.ByteStart + ct.Offset + int64(ct.DurationSec*bytesPerSec)
					sub.DurationSec = ct.DurationSec
					sub.Source = SourceSniff
					sub.Confidence = 0.85
					sub.Pos = ct.Index
					ix.Tracks = append(ix.Tracks, sub)
				}
				return nil
			}
		}
	}

	ix.Tracks = append(ix.Tracks, tp)
	return nil
}

// CodecOf определяет кодек по расширению файла.
func CodecOf(path string) string {
	switch strings.ToLower(extensionOf(path)) {
	case ".flac":
		return "flac"
	case ".mp3":
		return "mp3"
	case ".m4a", ".mp4", ".aac", ".alac", ".m4b":
		return "aac"
	case ".ogg", ".oga":
		return "ogg"
	case ".opus":
		return "opus"
	case ".wav":
		return "wav"
	case ".ape":
		return "ape"
	case ".wv":
		return "wavpack"
	case ".dsf":
		return "dsd"
	case ".dff":
		return "dsd"
	case ".tak":
		return "tak"
	case ".tta":
		return "tta"
	case ".aiff", ".aif":
		return "aiff"
	}
	return "unknown"
}

// IsLosslessExt сообщает, является ли расширение форматом без потерь.
func IsLosslessExt(path string) bool {
	switch strings.ToLower(extensionOf(path)) {
	case ".flac", ".wav", ".ape", ".wv", ".alac", ".aiff", ".aif", ".tak", ".dsf", ".dff", ".tta":
		return true
	}
	return false
}

// bitDepthOr возвращает глубину бита, либо 16 по умолчанию.
func bitDepthOr(bd int) int {
	if bd <= 0 {
		return 16
	}
	return bd
}

// titleFromFileName извлекает название трека из имени файла, убирая номер.
func titleFromFileName(base string) string {
	t := extRe.ReplaceAllString(base, "")
	t = strings.TrimSpace(t)
	t = leadNumRe.ReplaceAllString(t, "")
	t = leadNum2Re.ReplaceAllString(t, "")
	t = strings.TrimSpace(t)
	t = strings.Trim(t, "-–—. ")
	return t
}

// trackNumFromPath извлекает номер трека из пути.
func trackNumFromPath(full string) int {
	if n, ok := TrackNumber(path.Base(full)); ok {
		return n
	}
	return 0
}

// discFromPath определяет номер диска по имени каталога ("CD2/..." → 2).
func discFromPath(full string) int {
	dir := path.Dir(full)
	if dir == "." || dir == "" {
		return 0
	}
	for _, seg := range strings.Split(dir, "/") {
		up := strings.ToUpper(seg)
		if strings.HasPrefix(up, "CD") {
			var n int
			if _, err := fmt.Sscanf(strings.TrimPrefix(up, "CD"), "%d", &n); err == nil && n > 0 {
				return n
			}
		}
		if strings.HasPrefix(up, "DISC") {
			var n int
			if _, err := fmt.Sscanf(strings.TrimPrefix(up, "DISC"), "%d", &n); err == nil && n > 0 {
				return n
			}
		}
	}
	return 0
}
