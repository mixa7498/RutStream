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
	Artist  string
	Album   string
	Year    int
	// Topics — раздачи этого релиза, лучшая первой.
	Topics []Topic
	// IsCollection — раздача содержит всю дискографию.
	IsCollection bool
	// Lossless — есть ли lossless-версия.
	Lossless bool
	// Best — лучшая раздача (lossless + сиды).
	Best Topic
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
func (c *Client) SearchArtist(ctx context.Context, artist string, forums []Forum, perForum int) ([]AlbumRelease, error) {
	if len(forums) == 0 {
		return nil, fmt.Errorf("список разделов пуст")
	}
	needle := strings.ToLower(artist)

	// Собираем раздачи по всем разделам (последовательно, чтобы не долбить сайт).
	var all []Topic
	for _, f := range forums {
		topics, err := c.Search(ctx, artist, int(f.ID), 0)
		if err != nil {
			// Раздел мог быть недоступен — пропускаем, но не рвём общий поиск.
			continue
		}
		for _, t := range topics {
			if !strings.Contains(strings.ToLower(t.Title), needle) {
				continue
			}
			t.ForumID = int(f.ID)
			all = append(all, t)
			if len(all) >= perForum*len(forums) {
				break
			}
		}
	}
	if len(all) == 0 {
		return nil, fmt.Errorf("по запросу %q ничего не найдено", artist)
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
