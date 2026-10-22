package mediaindex

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// CueSheet — разобранный cue-файл.
type CueSheet struct {
	Title     string
	Performer string
	Year      string
	Files     []CueFile
}

// CueFile — блок FILE одного альбома.
type CueFile struct {
	Name   string
	Tracks []CueTrack
}

// CueTrack — трек внутри cue-файла.
type CueTrack struct {
	Number    int
	Title     string
	Performer string
	// Index — смещение трека в сэмплах от начала аудиоданных файла.
	Index int64
	// EndSamples — конец трека в сэмплах (заполняется TrackSamples).
	//
	// Важно: сэмплы НЕ переводятся в байты — кадры FLAC сжаты, и позиция
	// сэмпла в байтах зависит от сжатия. Поэтому трек внутри альбома можно
	// найти только последовательным декодированием, а объём скачивания
	// ограничивается длительностью трека.
	EndSamples int64
}

// ParseCue разбирает cue-файл.
//
// Поддерживаются команды REM, PERFORMER, TITLE, FILE, TRACK, INDEX.
// Строка INDEX задаёт смещение в формате MM:SS:FF (кадры по 75 кадров/сек).
func ParseCue(r io.Reader) (*CueSheet, error) {
	sheet := &CueSheet{}
	var curFile *CueFile
	var curTrack *CueTrack

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		cmd := strings.ToUpper(strings.Fields(line)[0])
		rest := strings.TrimSpace(line[len(strings.Fields(line)[0]):])

		switch cmd {
		case "REM":
			// REM GENRE / REM DATE / REM COMMENT
			parts := strings.SplitN(rest, " ", 2)
			if len(parts) == 2 {
				switch strings.ToUpper(parts[0]) {
				case "DATE":
					sheet.Year = strings.TrimSpace(parts[1])
				case "TITLE":
					sheet.Title = strings.TrimSpace(parts[1])
				case "PERFORMER":
					sheet.Performer = strings.TrimSpace(parts[1])
				}
			}
		case "PERFORMER":
			if curTrack != nil {
				curTrack.Performer = unquote(rest)
			} else {
				sheet.Performer = unquote(rest)
			}
		case "TITLE":
			if curTrack != nil {
				curTrack.Title = unquote(rest)
			} else if sheet.Title == "" {
				sheet.Title = unquote(rest)
			}
		case "FILE":
			name, typ := parseFileCmd(rest)
			sheet.Files = append(sheet.Files, CueFile{Name: name})
			curFile = &sheet.Files[len(sheet.Files)-1]
			_ = typ
			curTrack = nil
		case "TRACK":
			num, title := parseTrackCmd(rest)
			if curFile == nil {
				curFile = &CueFile{}
				sheet.Files = append(sheet.Files, *curFile)
				curFile = &sheet.Files[len(sheet.Files)-1]
			}
			curFile.Tracks = append(curFile.Tracks, CueTrack{Number: num, Title: title})
			curTrack = &curFile.Tracks[len(curFile.Tracks)-1]
		case "INDEX":
			if curTrack == nil {
				continue
			}
			if off, ok := parseIndex(rest); ok {
				curTrack.Index = off
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return sheet, nil
}

// parseFileCmd разбирает `FILE "name.flac" WAVE`.
func parseFileCmd(s string) (name, typ string) {
	fields := splitCueFields(s)
	if len(fields) == 0 {
		return "", ""
	}
	name = unquote(fields[0])
	if len(fields) > 1 {
		typ = fields[1]
	}
	return name, typ
}

// parseTrackCmd разбирает `TRACK 01 AUDIO`.
func parseTrackCmd(s string) (num int, title string) {
	fields := splitCueFields(s)
	if len(fields) == 0 {
		return 0, ""
	}
	num, _ = strconv.Atoi(fields[0])
	// Ищем название в кавычках (некоторые релизы пишут `TRACK 01 AUDIO "Title"`).
	for _, f := range fields {
		if strings.HasPrefix(f, "\"") {
			title = unquote(f)
			break
		}
	}
	return num, title
}

// parseIndex разбирает `INDEX 01 12:34:56` и возвращает смещение в сэмплах.
//
// Формат времени: MM:SS:FF (кадры по 75/сек) либо MM:SS.
func parseIndex(s string) (int64, bool) {
	fields := splitCueFields(s)
	if len(fields) < 2 {
		return 0, false
	}
	parts := strings.Split(fields[1], ":")
	if len(parts) == 3 {
		mm, _ := strconv.ParseInt(parts[0], 10, 64)
		ss, _ := strconv.ParseInt(parts[1], 10, 64)
		ff, _ := strconv.ParseInt(parts[2], 10, 64)
		return ((mm*60 + ss) * 75 + ff) * 44100 / 75, true
	}
	if len(parts) == 2 {
		mm, _ := strconv.ParseInt(parts[0], 10, 64)
		ss, _ := strconv.ParseInt(parts[1], 10, 64)
		return (mm*60 + ss) * 44100, true
	}
	return 0, false
}

// splitCueFields разбивает строку на поля с учётом кавычек.
func splitCueFields(s string) []string {
	var out []string
	var cur strings.Builder
	inQuotes := false
	for _, r := range s {
		switch {
		case r == '"':
			inQuotes = !inQuotes
			cur.WriteRune(r)
		case (r == ' ' || r == '\t') && !inQuotes:
			if cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

// unquote снимает кавычки.
func unquote(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, "\"")
	return strings.TrimSpace(s)
}

// CueTracksForFile возвращает треки cue-файла для указанного аудиофайла.
//
// Сопоставление идёт по имени файла (без пути); если не найдено — берётся
// первый блок FILE.
func (c *CueSheet) CueTracksForFile(audioName string) []CueTrack {
	base := audioName
	if i := strings.LastIndexAny(base, "/\\"); i >= 0 {
		base = base[i+1:]
	}
	for _, f := range c.Files {
		fb := f.Name
		if i := strings.LastIndexAny(fb, "/\\"); i >= 0 {
			fb = fb[i+1:]
		}
		if strings.EqualFold(fb, base) {
			return f.Tracks
		}
	}
	if len(c.Files) > 0 {
		return c.Files[0].Tracks
	}
	return nil
}

// TrackBytes переводит границы треков из сэмплов в байты аудиоданных.
//
// bytesPerSample = каналы × бит на сэмпл / 8.
// TrackSamples проставляет конец каждого трека (в сэмплах).
//
// totalSamples — полное число сэмплов в аудиофайле (из STREAMINFO).
func (c *CueSheet) TrackSamples(tracks []CueTrack, totalSamples int64) []CueTrack {
	out := make([]CueTrack, len(tracks))
	copy(out, tracks)
	for i := range out {
		end := totalSamples
		if i+1 < len(tracks) {
			end = tracks[i+1].Index
		}
		if end > totalSamples {
			end = totalSamples
		}
		if end < out[i].Index {
			end = out[i].Index
		}
		out[i].EndSamples = end
	}
	return out
}

// String печатает краткое описание.
func (c *CueSheet) String() string {
	n := 0
	for _, f := range c.Files {
		n += len(f.Tracks)
	}
	return fmt.Sprintf("cue: %q, файлов %d, треков %d", c.Title, len(c.Files), n)
}
