// Package mediaindex строит карту «трек → файл → диапазон байт → диапазон piece'ов»
// для торрента, чтобы качать только нужные куски раздачи, а не её целиком.
package mediaindex

import (
	"fmt"
	"sort"
	"strings"

	"github.com/anacrolix/torrent/metainfo"
)

// Plan описывает, какие куски раздачи нужны для получения одного файла (или группы файлов).
//
// В BitTorrent единица передачи — piece. Файл не обязан быть выровнен по границам piece'ов,
// поэтому для чтения байтов [ByteStart, ByteEnd) нужны piece'ы [PieceStart, PieceEnd],
// а крайние piece'ы скачиваются целиком — лишние байты учитываются в ExtraBytes.
type Plan struct {
	FileIdx    int
	Path       string
	ByteStart  int64
	ByteEnd    int64
	PieceStart int
	PieceEnd   int
	ExtraBytes int64
}

// PieceRange — полуинтервал piece'ов [Start, End).
type PieceRange struct {
	Start int
	End   int
}

// Len возвращает количество piece'ов в диапазоне.
func (p PieceRange) Len() int {
	if p.End <= p.Start {
		return 0
	}
	return p.End - p.Start
}

// Contains сообщает, принадлежит ли piece диапазону.
func (p PieceRange) Contains(i int) bool { return i >= p.Start && i < p.End }

// PieceRangeFor возвращает диапазон piece'ов, покрывающий байты [start, end).
// end — исключительно.
func PieceRangeFor(start, end, pieceLen int64) PieceRange {
	if end <= start {
		return PieceRange{}
	}
	if pieceLen <= 0 {
		return PieceRange{}
	}
	s := int(start / pieceLen)
	e := int((end - 1) / pieceLen) + 1
	return PieceRange{Start: s, End: e}
}

// ExtraBytes возвращает количество байт, которые будут скачаны «впустую»
// из-за выравнивания по границам piece'ов.
//
// Крайний левый piece скачивается целиком, хотя нужен только его хвост;
// крайний правый — только его начало, хотя скачивается целиком.
func ExtraBytes(start, end, pieceLen int64) int64 {
	if end <= start || pieceLen <= 0 {
		return 0
	}
	s := start / pieceLen
	e := (end - 1) / pieceLen
	wasteLeft := start - s*pieceLen
	wasteRight := (e+1)*pieceLen - end
	return wasteLeft + wasteRight
}

// FilesInfo возвращает список файлов раздачи с вычисленными смещениями
// в «склеенном» потоке (BEP 3: файлы идут подряд, в порядке info.files).
func FilesInfo(info *metainfo.Info) []metainfo.FileInfo {
	if info == nil {
		return nil
	}
	return info.UpvertedFiles()
}

// FilePath возвращает человекочитаемый путь файла в раздаче ("CD1/01 - Song.flac").
func FilePath(f metainfo.FileInfo) string {
	p := f.BestPath()
	if len(p) == 0 {
		return ""
	}
	return strings.Join(p, "/")
}

// PlanForFile строит план загрузки для одного файла раздачи.
func PlanForFile(info *metainfo.Info, idx int) (Plan, error) {
	if info == nil {
		return Plan{}, fmt.Errorf("metainfo is nil")
	}
	files := FilesInfo(info)
	if idx < 0 || idx >= len(files) {
		return Plan{}, fmt.Errorf("file index %d out of range (%d files)", idx, len(files))
	}
	f := files[idx]
	start := f.TorrentOffset
	end := start + f.Length
	pr := PieceRangeFor(start, end, info.PieceLength)
	return Plan{
		FileIdx:    idx,
		Path:       FilePath(f),
		ByteStart:  start,
		ByteEnd:    end,
		PieceStart: pr.Start,
		PieceEnd:   pr.End,
		ExtraBytes: ExtraBytes(start, end, info.PieceLength),
	}, nil
}

// PlansForFiles строит планы для набора файлов (например, альбом).
func PlansForFiles(info *metainfo.Info, idxs []int) ([]Plan, error) {
	out := make([]Plan, 0, len(idxs))
	for _, i := range idxs {
		p, err := PlanForFile(info, i)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

// MergeRanges объединяет пересекающиеся piece-диапазоны.
// Важно: нельзя просто взять диапазон от первого до последнего файла —
// тогда скачаются «чужие» треки между ними.
func MergeRanges(ranges []PieceRange) []PieceRange {
	if len(ranges) == 0 {
		return nil
	}
	cp := make([]PieceRange, len(ranges))
	copy(cp, ranges)
	sort.Slice(cp, func(i, j int) bool { return cp[i].Start < cp[j].Start })
	out := []PieceRange{cp[0]}
	for _, r := range cp[1:] {
		last := &out[len(out)-1]
		if r.Start <= last.End {
			if r.End > last.End {
				last.End = r.End
			}
			continue
		}
		out = append(out, r)
	}
	return out
}

// RangesForPlans переводит планы в объединённые piece-диапазоны.
func RangesForPlans(plans []Plan) []PieceRange {
	ranges := make([]PieceRange, 0, len(plans))
	for _, p := range plans {
		if p.PieceEnd > p.PieceStart {
			ranges = append(ranges, PieceRange{Start: p.PieceStart, End: p.PieceEnd})
		}
	}
	return MergeRanges(ranges)
}

// EstimateDownload оценивает, сколько байт будет скачано для получения файла,
// и сколько из них — «лишние» из-за выравнивания по piece'ам.
func EstimateDownload(info *metainfo.Info, idx int) (bytes, pieces int64, extra int64, err error) {
	p, err := PlanForFile(info, idx)
	if err != nil {
		return 0, 0, 0, err
	}
	pr := PieceRangeFor(p.ByteStart, p.ByteEnd, info.PieceLength)
	bytes = int64(pr.Len()) * info.PieceLength
	pieces = int64(pr.Len())
	extra = p.ExtraBytes
	return bytes, pieces, extra, nil
}

// EstimateTrack оценивает объём загрузки конкретного трека.
//
// Для трека внутри одного файла (альбом с cue-разметкой) размер в байтах
// неизвестен заранее: кадры FLAC сжаты, поэтому позиция сэмпла в файле
// зависит от сжатия. Оценка строится пропорционально длительности трека
// (см. TrackPlan.EstimateBytes), а фактический объём ограничивается
// длительностью при декодировании.
func EstimateTrack(info *metainfo.Info, t TrackPlan) (bytes, pieces int64, extra int64) {
	if info == nil {
		return 0, 0, 0
	}
	if t.EstimateBytes > 0 {
		bytes = t.EstimateBytes
		pieces = (bytes + info.PieceLength - 1) / info.PieceLength
		// Лишние байты: округление до границы piece'а плюс хвост файла,
		// который тоже придётся скачать (декодирование идёт с начала).
		if t.ByteEnd > t.ByteStart {
			extra = ExtraBytes(t.ByteStart, t.ByteEnd, info.PieceLength)
		}
		return bytes, pieces, extra
	}
	if t.ByteEnd <= t.ByteStart {
		return 0, 0, 0
	}
	pr := PieceRangeFor(t.ByteStart, t.ByteEnd, info.PieceLength)
	bytes = int64(pr.Len()) * info.PieceLength
	pieces = int64(pr.Len())
	extra = ExtraBytes(t.ByteStart, t.ByteEnd, info.PieceLength)
	return bytes, pieces, extra
}

// PieceLength возвращает длину piece'а раздачи.
func PieceLength(info *metainfo.Info) int64 {
	if info == nil {
		return 0
	}
	return info.PieceLength
}

// NumPieces возвращает количество piece'ов в раздаче.
func NumPieces(info *metainfo.Info) int {
	if info == nil {
		return 0
	}
	return info.NumPieces()
}
