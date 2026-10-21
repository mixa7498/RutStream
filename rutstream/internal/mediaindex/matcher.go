package mediaindex

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

var (
	extRe      = regexp.MustCompile(`(?i)\.(flac|mp3|m4a|mp4|aac|ogg|oga|opus|wav|ape|wv|dsf|dff|tak|tta|alac|aiff|aif|flac\.log)$`)
	leadNumRe  = regexp.MustCompile(`^[\[\(]?\d{1,3}[\]\)]?\s*[-._–—)]\s*`)
	leadNum2Re = regexp.MustCompile(`^[\[\(]?\d{1,3}[\]\)]?\s+`)
	nonAlnumRe = regexp.MustCompile(`[«»"'()\[\]{}<>.,:;!?_\-–—/\\|~@#$%^&*+=]+`)
	spacesOnly = regexp.MustCompile(`\s+`)
)

// audioExts — расширения, которые считаем аудиофайлами.
var audioExts = map[string]bool{
	".flac": true, ".mp3": true, ".m4a": true, ".mp4": true, ".aac": true,
	".ogg": true, ".oga": true, ".opus": true, ".wav": true, ".ape": true,
	".wv": true, ".dsf": true, ".dff": true, ".tak": true, ".tta": true,
	".alac": true, ".aiff": true, ".aif": true, ".mka": true, ".m4b": true,
}

// cueExts — файлы с метаданными раскладки.
var cueExts = map[string]bool{".cue": true, ".m3u": true, ".m3u8": true}

// Normalize приводит название трека/файла к каноническому виду для сопоставления:
// нижний регистр, ё→е, без расширения, без ведущего номера трека, без пунктуации.
func Normalize(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, "ё", "е")
	s = extRe.ReplaceAllString(s, "")
	s = leadNumRe.ReplaceAllString(s, "")
	s = leadNum2Re.ReplaceAllString(s, "")
	s = nonAlnumRe.ReplaceAllString(s, " ")
	s = spacesOnly.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

// TrackNumber извлекает ведущий номер трека из имени файла ("03 - Song.flac" → 3).
func TrackNumber(name string) (int, bool) {
	base := name
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	base = extRe.ReplaceAllString(base, "")
	m := regexp.MustCompile(`^[\[\(]?(\d{1,3})[\]\)]?`).FindStringSubmatch(base)
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// Score оценивает совпадение двух названий от 0 до 1.
func Score(a, b string) float64 {
	na, nb := Normalize(a), Normalize(b)
	if na == "" || nb == "" {
		return 0
	}
	if na == nb {
		return 1
	}
	if len(na) >= 4 && len(nb) >= 4 && (strings.Contains(na, nb) || strings.Contains(nb, na)) {
		// Одно название — подстрока другого: сильное, но не идеальное совпадение.
		return 0.9
	}
	if r := tokenSetRatio(na, nb); r >= 0.9 {
		return 0.95
	}
	if r := levenshteinRatio(na, nb); r >= 0.85 {
		return 0.9
	}
	if r := jaroWinkler(na, nb); r >= 0.88 {
		return 0.8
	}
	return 0
}

// BestMatch ищет в candidates элемент с максимальным Score.
func BestMatch(needle string, candidates []string) (idx int, score float64) {
	idx = -1
	for i, c := range candidates {
		if s := Score(needle, c); s > score {
			idx, score = i, s
		}
	}
	return idx, score
}

func tokens(s string) []string {
	return strings.Fields(s)
}

// tokenSetRatio — доля общих токенов с весом по длинам (по сути F1 по мультимножествам).
func tokenSetRatio(a, b string) float64 {
	ta, tb := tokens(a), tokens(b)
	if len(ta) == 0 || len(tb) == 0 {
		return 0
	}
	ca := map[string]int{}
	for _, t := range ta {
		ca[t]++
	}
	cb := map[string]int{}
	for _, t := range tb {
		cb[t]++
	}
	common := 0
	for t, n := range ca {
		if m, ok := cb[t]; ok {
			if m < n {
				common += m
			} else {
				common += n
			}
		}
	}
	total := len(ta) + len(tb)
	if total == 0 {
		return 0
	}
	return 2 * float64(common) / float64(total)
}

// levenshteinRatio — 1 - расстояние/макс-длина.
func levenshteinRatio(a, b string) float64 {
	if a == b {
		return 1
	}
	ra, rb := []rune(a), []rune(b)
	if len(ra) == 0 || len(rb) == 0 {
		return 0
	}
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min3(cur[j-1]+1, prev[j]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	maxLen := len(ra)
	if len(rb) > maxLen {
		maxLen = len(rb)
	}
	return 1 - float64(prev[len(rb)])/float64(maxLen)
}

// jaroWinkler — метрика с префиксным бонусом.
func jaroWinkler(a, b string) float64 {
	j := jaro(a, b)
	if j < 0.7 {
		return j
	}
	prefix := 0
	for i := 0; i < len(a) && i < len(b) && i < 4; i++ {
		if a[i] != b[i] {
			break
		}
		prefix++
	}
	return j + float64(prefix)*0.1*(1-j)
}

func jaro(a, b string) float64 {
	ra, rb := []rune(a), []rune(b)
	if len(ra) == 0 || len(rb) == 0 {
		return 0
	}
	matchDist := len(ra)
	if len(rb) < matchDist {
		matchDist = len(rb)
	}
	matchDist = matchDist/2 - 1
	if matchDist < 0 {
		matchDist = 0
	}
	ma := make([]bool, len(ra))
	mb := make([]bool, len(rb))
	matches := 0
	for i := range ra {
		start := i - matchDist
		if start < 0 {
			start = 0
		}
		end := i + matchDist + 1
		if end > len(rb) {
			end = len(rb)
		}
		for j := start; j < end; j++ {
			if mb[j] || ra[i] != rb[j] {
				continue
			}
			ma[i] = true
			mb[j] = true
			matches++
			break
		}
	}
	if matches == 0 {
		return 0
	}
	transpositions := 0
	k := 0
	for i := range ra {
		if !ma[i] {
			continue
		}
		for !mb[k] {
			k++
		}
		if ra[i] != rb[k] {
			transpositions++
		}
		k++
	}
	m := float64(matches)
	return (m/float64(len(ra)) + m/float64(len(rb)) + (m-float64(transpositions)/2)/m) / 3
}

func min3(a, b, c int) int {
	if b < a {
		a = b
	}
	if c < a {
		a = c
	}
	return a
}

// IsAudioFile сообщает, является ли путь аудиофайлом.
func IsAudioFile(path string) bool {
	ext := strings.ToLower(extensionOf(path))
	return audioExts[ext]
}

// IsCueFile сообщает, является ли путь файлом раскладки (.cue/.m3u).
func IsCueFile(path string) bool {
	ext := strings.ToLower(extensionOf(path))
	return cueExts[ext]
}

func extensionOf(path string) string {
	i := strings.LastIndex(path, ".")
	if i < 0 {
		return ""
	}
	return path[i:]
}

// sanitize убирает управляющие символы (частая проблема в именах файлов раздач).
func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
}
