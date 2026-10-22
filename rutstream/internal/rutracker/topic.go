package rutracker

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

var magnetRe = regexp.MustCompile(`magnet:\?xt=urn:btih:([A-Za-z0-9]+)`)
var trackerRe = regexp.MustCompile(`tr=([A-Za-z0-9%:\/\.\+\-]+)`)
var trackLineRe = regexp.MustCompile(`^\s*(\d{1,3})[\.\)\-–—\s]+(.+?)\s*(?:[—\-–|]\s*(\d{1,2}:\d{2}(?::\d{2})?))?\s*$`)

// brSplitRe делит HTML по тегам переноса строки.
var brSplitRe = regexp.MustCompile(`(?i)<br\s*/?>`)
var durationRe = regexp.MustCompile(`^(\d{1,2}):(\d{2})(?::(\d{2}))?$`)

// seedsRe и peersRe ищут «Сиды: 12» и «Личеры: 3» в тексте темы.
var seedsRe = regexp.MustCompile(`(?i)сиды\s*[:：]?\s*(\d+)`)
var peersRe = regexp.MustCompile(`(?i)личеры\s*[:：]?\s*(\d+)`)

// topicSizeRe ищет «Размер: 960.81 MB» в тексте темы.
var topicSizeRe = regexp.MustCompile(`(?i)размер\s*[:：]?\s*([\d.,]+\s*(?:bytes|kb|mb|gb|tb)?)`)

// parseTopicForumIDs собирает id разделов из хлебных крошек страницы темы.
func parseTopicForumIDs(doc *goquery.Document) []int {
	var out []int
	seen := map[int]bool{}
	doc.Find("a[href*='viewforum.php?f=']").Each(func(_ int, a *goquery.Selection) {
		href, _ := a.Attr("href")
		m := forumIDRe.FindStringSubmatch(href)
		if m == nil {
			return
		}
		id, _ := strconv.Atoi(m[1])
		if id == 0 || seen[id] {
			return
		}
		seen[id] = true
		out = append(out, id)
	})
	return out
}

// parseTopicSize достаёт размер раздачи.
//
// На странице темы есть скрытый счётчик с точным числом байтов
// (<span id="tor-size-humn" title="1007486418">), а в блоке «Статистика
// раздачи» — текст «Размер: 960.8 MB».
func parseTopicSize(doc *goquery.Document) (int64, string) {
	if sel := doc.Find("span#tor-size-humn").First(); sel.Length() > 0 {
		if v, ok := sel.Attr("title"); ok {
			if n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil {
				return n, strings.TrimSpace(sel.Text())
			}
		}
	}
	text := doc.Text()
	m := topicSizeRe.FindStringSubmatch(text)
	if m == nil {
		return 0, ""
	}
	value := strings.TrimSpace(m[1])
	return ParseSize(value), value
}

// parseTopicPeers достаёт число сидов и личеров из блока раздачи
// (<span class="seed">Сиды: 90</span>, <span class="leech">Личи: 34</span>).
func parseTopicPeers(doc *goquery.Document) (seeds, peers int) {
	if sel := doc.Find("span.seed b").First(); sel.Length() > 0 {
		seeds, _ = strconv.Atoi(strings.TrimSpace(sel.Text()))
	}
	if sel := doc.Find("span.leech b").First(); sel.Length() > 0 {
		peers, _ = strconv.Atoi(strings.TrimSpace(sel.Text()))
	}
	if seeds == 0 && peers == 0 {
		text := doc.Text()
		if m := seedsRe.FindStringSubmatch(text); m != nil {
			seeds, _ = strconv.Atoi(m[1])
		}
		if m := peersRe.FindStringSubmatch(text); m != nil {
			peers, _ = strconv.Atoi(m[1])
		}
	}
	return seeds, peers
}

// Topic загружает и разбирает страницу темы.
func (c *Client) Topic(ctx context.Context, topicID int64) (*Topic, error) {
	page, err := c.Get(ctx, fmt.Sprintf("/forum/viewtopic.php?t=%d", topicID))
	if err != nil {
		return nil, err
	}
	topic, err := ParseTopic(page)
	if err != nil {
		return nil, err
	}
	topic.ID = topicID
	return topic, nil
}

// ParseTopic разбирает страницу темы.
func ParseTopic(page []byte) (*Topic, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(page)))
	if err != nil {
		return nil, err
	}
	t := &Topic{}

	if sel := doc.Find("a#topic-title").First(); sel.Length() > 0 {
		t.Title = strings.TrimSpace(sel.Text())
	} else if sel := doc.Find("h1").First(); sel.Length() > 0 {
		t.Title = strings.TrimSpace(sel.Text())
	}

	// Магнит-ссылка: содержит info_hash и announce-серверы.
	if href, ok := doc.Find(`a[href*="magnet:?xt=urn:btih:"]`).First().Attr("href"); ok {
		if m := magnetRe.FindStringSubmatch(href); m != nil {
			t.InfoHash = strings.ToUpper(m[1])
		}
		for _, tr := range trackerRe.FindAllStringSubmatch(href, -1) {
			if dec, err := decodeTracker(tr[1]); err == nil {
				t.Trackers = append(t.Trackers, dec)
			}
		}
		t.Magnet = href
	}

	// Метаданные: пары «Метка: значение» в span.post-b.
	// Набор подписей различается для разных разделов, поэтому ищем по списку.
	metaLabels := []struct {
		labels []string
		field  *string
	}{
		{[]string{"Исполнитель (группы)", "Исполнитель", "Автор", "Художник"}, &t.Artist},
		{[]string{"Альбом", "Название диска"}, &t.Album},
		{[]string{"Год издания", "Год"}, &t.Year},
		{[]string{"Жанр", "Стиль"}, &t.Genre},
		{[]string{"Аудиокодек", "Формат"}, &t.Format},
		{[]string{"Битрейт"}, &t.Bitrate},
		{[]string{"Продолжительность", "Длительность"}, &t.Duration},
		{[]string{"Тип рипа", "Качество"}, &t.Quality},
	}
	for _, m := range metaLabels {
		for _, label := range m.labels {
			if v := postMeta(doc, label); v != "" {
				*m.field = v
				break
			}
		}
	}

	t.Tracks = parseTracklist(doc)

	// Размер, сиды и личеры — из информационного блока темы
	// («Размер: 960.81 MB», «Сиды: 12», «Личеры: 3»).
	t.Size, t.SizeText = parseTopicSize(doc)
	t.Seeds, t.Peers = parseTopicPeers(doc)

	// Разделы темы — из хлебных крошек (ссылки «viewforum.php?f=NNN»).
	t.ForumIDs = parseTopicForumIDs(doc)

	// Исполнитель и альбом часто указаны только в заголовке темы
	// («(Жанр) [ИСТОЧНИК] Исполнитель - Альбом - Год, ФОРМАТ, …»).
	if t.Artist == "" || t.Album == "" {
		artist, album, year := SplitArtistAlbum(t.Title)
		if t.Artist == "" {
			t.Artist = artist
		}
		if t.Album == "" {
			t.Album = album
		}
		if t.Year == "" && year != "" {
			t.Year = year
		}
	}
	return t, nil
}

// SplitArtistAlbum разбирает заголовок раздачи на исполнителя, альбом и год.
//
// Формат rutracker: «(Жанр) [ИСТОЧНИК] Исполнитель - Альбом - Год, ФОРМАТ, …».
func SplitArtistAlbum(title string) (artist, album, year string) {
	t := strings.TrimSpace(title)
	// Отбрасываем ведущий блок в квадратных скобках (источник) и в круглых (жанр).
	for {
		trimmed := false
		if strings.HasPrefix(t, "[") {
			if i := strings.Index(t, "]"); i > 0 {
				t = strings.TrimSpace(t[i+1:])
				trimmed = true
			}
		}
		if strings.HasPrefix(t, "(") {
			if i := strings.Index(t, ")"); i > 0 {
				t = strings.TrimSpace(t[i+1:])
				trimmed = true
			}
		}
		if !trimmed {
			break
		}
	}
	// Отрезаем хвост с форматом и годом.
	if i := strings.Index(t, ","); i > 0 {
		t = t[:i]
	}
	if y := YearOfTitle(t); y > 0 {
		year = strconv.Itoa(y)
	}
	parts := strings.SplitN(t, " - ", 2)
	if len(parts) == 2 {
		return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]), year
	}
	return "", strings.TrimSpace(t), year
}

// postMeta извлекает значение метки «Метка: значение» из блока темы.
func postMeta(doc *goquery.Document, label string) string {
	sel := doc.Find("span.post-b").FilterFunction(func(_ int, s *goquery.Selection) bool {
		return strings.Contains(s.Text(), label)
	}).First()
	if sel.Length() == 0 {
		return ""
	}
	// Значение обычно лежит в текстовом узле сразу после span.
	if v := nextTextNode(sel); v != "" {
		return strings.Trim(strings.TrimSpace(v), ":")
	}
	// Иначе — берём текст после двоеточия внутри самого узла.
	txt := sel.Text()
	if i := strings.Index(txt, ":"); i >= 0 {
		return strings.TrimSpace(txt[i+1:])
	}
	return ""
}

// nextTextNode возвращает текст следующего текстового узла после элемента.
func nextTextNode(sel *goquery.Selection) string {
	parent := sel.Parent()
	if parent.Length() == 0 {
		return ""
	}
	contents := parent.Contents()
	found := false
	for i := 0; i < contents.Length(); i++ {
		node := contents.Eq(i)
		if found {
			if node.Is("br") {
				continue
			}
			return node.Text()
		}
		if node.Is("span") && node.Text() == sel.Text() {
			found = true
		}
	}
	return ""
}

// parseTracklist извлекает список треков из тела темы.
//
// Актуальная вёрстка форума: блок «Треклист» (div.sp-body), где треки идут
// отдельными строками, разделёнными <br>, а заголовок диска — строка без номера.
// Поддерживается и старая табличная вёрстка.
func parseTracklist(doc *goquery.Document) []SiteTrack {
	var out []SiteTrack
	seen := map[string]bool{}

	// 1) Блок «Треклист» с построчным разбором.
	if body := findTracklistBlock(doc); body != nil {
		html, err := body.Html()
		if err == nil {
			disc := 0
			for _, chunk := range brSplitRe.Split(html, -1) {
				line := stripTags(chunk)
				line = strings.TrimSpace(strings.Join(strings.Fields(line), " "))
				line = strings.TrimSpace(strings.TrimLeft(line, ">/"))
				if line == "" {
					continue
				}
				if m := trackLineRe.FindStringSubmatch(line); m != nil {
					num, _ := strconv.Atoi(m[1])
					title := cleanTitle(m[2])
					title = strings.TrimSuffix(title, ".flac")
					title = strings.TrimSuffix(title, ".mp3")
					if title == "" {
						continue
					}
					key := strconv.Itoa(num) + "|" + title
					if seen[key] {
						continue
					}
					seen[key] = true
					out = append(out, SiteTrack{Pos: num, Title: title, Disc: disc,
						Duration: parseDuration(m[3])})
					continue
				}
				// Строка без номера — заголовок диска.
				if !strings.Contains(line, ".") || strings.Contains(line, "CD") ||
					strings.Contains(line, "Диск") || strings.Contains(line, "Disc") {
					disc++
				}
			}
		}
	}
	if len(out) > 0 {
		return out
	}

	// 2) Старая табличная вёрстка.
	doc.Find("div.post_body table, div.postBody table, div.content table, table.torrent").Each(func(_ int, tbl *goquery.Selection) {
		tbl.Find("tr").Each(func(_ int, tr *goquery.Selection) {
			cells := tr.Find("td, th")
			if cells.Length() < 2 {
				return
			}
			var numCell, titleCell, durCell *goquery.Selection
			switch cells.Length() {
			case 2:
				numCell = cells.Eq(0)
				titleCell = cells.Eq(1)
			case 3:
				numCell = cells.Eq(0)
				titleCell = cells.Eq(1)
				durCell = cells.Eq(2)
			default:
				numCell = cells.Eq(0)
				titleCell = cells.Eq(cells.Length() - 2)
				durCell = cells.Eq(cells.Length() - 1)
			}
			num, err := strconv.Atoi(strings.TrimSpace(numCell.Text()))
			if err != nil || num <= 0 {
				return
			}
			title := cleanTitle(titleCell.Text())
			if title == "" {
				return
			}
			key := strconv.Itoa(num) + "|" + title
			if seen[key] {
				return
			}
			seen[key] = true
			st := SiteTrack{Pos: num, Title: title}
			if durCell != nil {
				st.Duration = parseDuration(durCell.Text())
			}
			out = append(out, st)
		})
	})
	return out
}

// findTracklistBlock находит блок с треклистом.
func findTracklistBlock(doc *goquery.Document) *goquery.Selection {
	// Ищем заголовок «Треклист» и берём следующий спойлер.
	var body *goquery.Selection
	doc.Find("div.sp-wrap, div.sp-head, span.post-b").EachWithBreak(func(_ int, s *goquery.Selection) bool {
		if strings.TrimSpace(s.Text()) == "Треклист" {
			if wrap := s.Closest("div.sp-wrap"); wrap.Length() > 0 {
				if b := wrap.Find("div.sp-body").First(); b.Length() > 0 {
					body = b
					return false
				}
			}
			if b := s.Next(); b.Length() > 0 {
				body = b
				return false
			}
		}
		return true
	})
	return body
}

// stripTags убирает HTML-теги и декодирует сущности.
func stripTags(s string) string {
	s = regexp.MustCompile(`<[^>]*>`).ReplaceAllString(s, " ")
	s = strings.ReplaceAll(s, "&nbsp;", " ")
	s = strings.ReplaceAll(s, "&amp;", "&")
	s = strings.ReplaceAll(s, "&quot;", "\"")
	s = strings.ReplaceAll(s, "&#39;", "'")
	return s
}

func cleanTitle(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, "«»\"' \t")
	s = strings.Join(strings.Fields(s), " ")
	return s
}

// parseDuration разбирает «3:45» или «1:02:33» в секунды.
func parseDuration(s string) float64 {
	s = strings.TrimSpace(s)
	m := durationRe.FindStringSubmatch(s)
	if m == nil {
		return 0
	}
	if m[3] != "" {
		h, _ := strconv.Atoi(m[1])
		mi, _ := strconv.Atoi(m[2])
		se, _ := strconv.Atoi(m[3])
		return float64(h*3600 + mi*60 + se)
	}
	mi, _ := strconv.Atoi(m[1])
	se, _ := strconv.Atoi(m[2])
	return float64(mi*60 + se)
}

// decodeTracker декодирует URL трекера из магнита.
func decodeTracker(s string) (string, error) {
	s = strings.ReplaceAll(s, "+", " ")
	out, err := decodePercent(s)
	if err != nil {
		return "", err
	}
	return out, nil
}
