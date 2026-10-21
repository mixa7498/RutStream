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
var durationRe = regexp.MustCompile(`^(\d{1,2}):(\d{2})(?::(\d{2}))?$`)

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
	for _, label := range []string{"Исполнитель", "Автор", "Художник", "Альбом", "Год", "Жанр", "Формат", "Битрейт", "Длительность", "Качество", "Метка", "Релиз", "Лейбл", "Страна"} {
		v := postMeta(doc, label)
		if v == "" {
			continue
		}
		switch label {
		case "Исполнитель", "Автор", "Художник":
			t.Artist = v
		case "Альбом":
			t.Album = v
		case "Год":
			t.Year = v
		case "Жанр":
			t.Genre = v
		case "Формат":
			t.Format = v
		case "Битрейт":
			t.Bitrate = v
		case "Длительность":
			t.Duration = v
		case "Качество":
			t.Quality = v
		}
	}

	t.Tracks = parseTracklist(doc)
	return t, nil
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
func parseTracklist(doc *goquery.Document) []SiteTrack {
	var out []SiteTrack
	seen := map[string]bool{}

	// 1) Таблицы в теле темы (phpBB3: div.post_body).
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

	if len(out) > 0 {
		return out
	}

	// 2) Строки вида «01. Название — 3:45» в тексте.
	doc.Find("div.post_body, div.postBody, .post_body").Each(func(_ int, body *goquery.Selection) {
		lines := strings.Split(body.Text(), "\n")
		for _, line := range lines {
			m := trackLineRe.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			num, _ := strconv.Atoi(m[1])
			title := cleanTitle(m[2])
			if title == "" {
				continue
			}
			key := strconv.Itoa(num) + "|" + title
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, SiteTrack{Pos: num, Title: title, Duration: parseDuration(m[3])})
		}
	})
	return out
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
