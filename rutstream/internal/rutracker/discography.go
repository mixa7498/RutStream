package rutracker

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// AlbumRelease — агрегированный релиз (альбом) по нескольким раздачам.
type AlbumRelease struct {
	Artist  string `json:"artist"`
	Album   string `json:"album"`
	Year    int    `json:"year"`
	// Topics — раздачи этого релиза, лучшая первой.
	Topics []Topic `json:"topics"`
	// IsCollection — раздача содержит всю дискографию.
	IsCollection bool `json:"isCollection"`
	// Lossless — есть ли lossless-версия.
	Lossless bool `json:"lossless"`
	// Best — лучшая раздача (lossless + сиды).
	Best Topic `json:"best"`
}

// collectionRe — маркеры сборников дискографии.
//
// Внимание: \b в Go — ASCII-граница слова, поэтому для кириллицы используем
// явные варианты без \b.
var collectionRe = regexp.MustCompile(`(?i)(discograph[a-z]*|collection|complete|полная дискография|дискография|полное собрание|studio albums|anthology|box ?set)`)

// yearRe вытаскивает год из названия раздачи.
var yearRe = regexp.MustCompile(`\b(19[5-9]\d|20[0-4]\d)\b`)

// losslessRe — маркеры lossless-форматов.
var losslessRe = regexp.MustCompile(`(?i)\b(flac|alac|ape|wav|dsd|24bit|24-bit|lossless|hi-?res)\b`)

// mp3Re — маркеры lossy-форматов.
var mp3Re = regexp.MustCompile(`(?i)\b(mp3|aac|m4a|ogg|opus|320|v0|v2|128)\b`)

// IsCollectionTitle определяет, что раздача — сборник дискографии.
func IsCollectionTitle(title string) bool { return collectionRe.MatchString(title) }

// IsLosslessTitle определяет, что раздача в lossless-формате.
func IsLosslessTitle(title string) bool { return losslessRe.MatchString(title) }

// YearOfTitle извлекает год из названия раздачи.
func YearOfTitle(title string) int {
	if m := yearRe.FindStringSubmatch(title); m != nil {
		y, _ := strconv.Atoi(m[1])
		return y
	}
	return 0
}

// SearchArtist ищет все раздачи исполнителя по музыкальным разделам
// и группирует их в релизы (альбомы).
//
// Поиск идёт одним запросом без параметра «f»: комбинация nm+f на форуме
// ломается («Не найдено»), а фильтр по разделу применяем у себя — так заодно
// быстрее, чем обходить разделы по очереди.
func (c *Client) SearchArtist(ctx context.Context, artist string, forums []Forum, perForum int) ([]AlbumRelease, error) {
	if len(forums) == 0 {
		return nil, fmt.Errorf("список разделов пуст")
	}
	needle := strings.ToLower(artist)

	allowed := make(map[int]bool, len(forums))
	for _, f := range forums {
		allowed[int(f.ID)] = true
	}
	// Готовый набор разделов с подразделами (задаётся снаружи) — экономим
	// обход форума.
	if ids := c.storedMusicForumIDs(); len(ids) > 0 {
		for id := range ids {
			allowed[id] = true
		}
	} else {
		// Раздачи лежат в подразделах («Rock (Hi-Res)»), а не в самих
		// разделах — собираем id подразделов (с кэшем в клиенте).
		for id := range c.MusicForumIDs(ctx, forums) {
			allowed[id] = true
		}
	}
	topics, err := c.Search(ctx, artist, 0, 0)
	if err != nil {
		return nil, err
	}
	var all []Topic
	for _, t := range topics {
		if !allowed[t.ForumID] {
			continue
		}
		if !strings.Contains(strings.ToLower(t.Title), needle) {
			continue
		}
		all = append(all, t)
	}
	if len(all) == 0 {
		return nil, fmt.Errorf("по запросу %q в музыкальных разделах ничего не найдено", artist)
	}
	return GroupReleases(artist, all), nil
}

// GroupReleases группирует раздачи в релизы по альбому и году.
func GroupReleases(artist string, topics []Topic) []AlbumRelease {
	type key struct {
		album string
		year  int
	}
	groups := map[key][]Topic{}
	var order []key
	for _, t := range topics {
		album, year := AlbumOfTitle(t.Title)
		k := key{album: album, year: year}
		if _, ok := groups[k]; !ok {
			order = append(order, k)
		}
		groups[k] = append(groups[k], t)
	}
	var out []AlbumRelease
	for _, k := range order {
		topics := groups[k]
		sort.SliceStable(topics, func(i, j int) bool {
			li, lj := IsLosslessTitle(topics[i].Title), IsLosslessTitle(topics[j].Title)
			if li != lj {
				return li
			}
			return topics[i].Seeds > topics[j].Seeds
		})
		rel := AlbumRelease{Artist: artist, Album: k.album, Year: k.year, Topics: topics, Best: topics[0]}
		for _, t := range topics {
			if IsCollectionTitle(t.Title) {
				rel.IsCollection = true
			}
			if IsLosslessTitle(t.Title) {
				rel.Lossless = true
			}
		}
		out = append(out, rel)
	}
	// Коллекции — первыми, затем по году.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].IsCollection != out[j].IsCollection {
			return out[i].IsCollection
		}
		if out[i].Year != out[j].Year {
			return out[i].Year < out[j].Year
		}
		return out[i].Album < out[j].Album
	})
	return out
}

// AlbumOfTitle пытается выделить название альбома из заголовка раздачи.
//
// Формат rutracker: «[ИСТОЧНИК] Исполнитель - Альбом - Год, ФОРМАТ, …».
func AlbumOfTitle(title string) (string, int) {
	t := title
	// Убираем ведущий блок в квадратных скобках.
	if i := strings.Index(t, "]"); i >= 0 && strings.HasPrefix(strings.TrimSpace(t), "[") {
		t = t[i+1:]
	}
	t = strings.TrimSpace(t)
	year := YearOfTitle(t)
	// Убираем год и хвост с форматом.
	if loc := yearRe.FindStringIndex(t); loc != nil {
		t = t[:loc[0]] + " " + t[loc[1]:]
	}
	// Отрезаем «, FLAC (tracks), lossless [484.4 MB]».
	if i := strings.Index(t, ","); i >= 0 {
		t = t[:i]
	}
	// Отрезаем хвост в квадратных скобках («[MP3 320]», «[FLAC]»).
	if i := strings.Index(t, "["); i >= 0 {
		t = t[:i]
	}
	// Хвост в круглых скобках с годом («(2000)»).
	if i := strings.Index(t, "("); i >= 0 {
		t = t[:i]
	}
	// Исполнитель - Альбом: берём вторую часть по разделителю " - ".
	parts := strings.SplitN(t, " - ", 2)
	if len(parts) == 2 {
		album := strings.TrimSpace(parts[1])
		// Убираем год-обрывки и лишние пробелы.
		album = strings.TrimSpace(strings.ReplaceAll(album, "-", " "))
		return album, year
	}
	return strings.TrimSpace(t), year
}
