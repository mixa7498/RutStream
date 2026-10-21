package rutracker

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

var topicIDRe = regexp.MustCompile(`t=(\d+)`)
var searchForumIDRe = regexp.MustCompile(`[?&]f=(\d+)`)
var sizeRe = regexp.MustCompile(`(?i)^([\d.,]+)\s*(bytes|kb|mb|gb|tb)?$`)
var sizeTextRe = regexp.MustCompile(`(?i)([\d.,]+)\s*(bytes|kb|mb|gb|tb|b)\b`)

// Search ищет раздачи по названию.
//
// Важная особенность форума: если передать одновременно nm и f, выдача
// оказывается пустой («Не найдено»). Поэтому поиск по названию идёт без f
// (по всему сайту, а раздел указан в каждой строке), а фильтр по разделу
// применяется у нас. Листинг раздела (без nm) идёт с f.
func (c *Client) Search(ctx context.Context, query string, forumID int, start int) ([]Topic, error) {
	var path string
	if strings.TrimSpace(query) != "" {
		path = fmt.Sprintf("/forum/tracker.php?nm=%s&start=%d", url_QueryEscape(query), start)
	} else {
		path = fmt.Sprintf("/forum/tracker.php?f=%d&start=%d", forumID, start)
	}
	page, err := c.Get(ctx, path)
	if err != nil {
		return nil, err
	}
	topics, err := ParseSearchResults(page)
	if err != nil {
		return nil, err
	}
	// Фильтруем по разделу на нашей стороне.
	if forumID > 0 && strings.TrimSpace(query) != "" {
		var out []Topic
		for _, t := range topics {
			if t.ForumID == forumID {
				out = append(out, t)
			}
		}
		return out, nil
	}
	return topics, nil
}

// ParseSearchResults разбирает таблицу результатов трекера.
//
// Актуальная вёрстка: <table id="tor-tbl"> со столбцами
// «иконка | проверено | форум | тема | автор | размер | сиды | личеры | загрузки | дата».
// Поддерживается и старая вёрстка (row4/med/wbr).
func ParseSearchResults(page []byte) ([]Topic, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(page)))
	if err != nil {
		return nil, err
	}
	var out []Topic
	rows := doc.Find("table#tor-tbl tbody tr")
	if rows.Length() == 0 {
		rows = doc.Find("table.forumline tbody tr")
	}
	rows.Each(func(_ int, tr *goquery.Selection) {
		t, ok := parseTopicRow(tr)
		if !ok {
			return
		}
		out = append(out, t)
	})
	return out, nil
}

// parseTopicRow разбирает одну строку результатов.
func parseTopicRow(tr *goquery.Selection) (Topic, bool) {
	var t Topic

	// Заголовок: ссылка на тему.
	link := tr.Find("a.med.tLink").First()
	if link.Length() == 0 {
		link = tr.Find(".row4 .wbr .med").First()
	}
	if link.Length() == 0 {
		link = tr.Find(".row4 .med").First()
	}
	if link.Length() == 0 {
		return t, false
	}
	href, _ := link.Attr("href")
	m := topicIDRe.FindStringSubmatch(href)
	if m == nil {
		return t, false
	}
	id, _ := strconv.ParseInt(m[1], 10, 64)
	t.ID = id
	if v, ok := tr.Attr("data-topic_id"); ok {
		if parsed, err := strconv.ParseInt(v, 10, 64); err == nil {
			t.ID = parsed
		}
	}
	// Заголовок без тегов в скобках (жанровые метки).
	t.Title = cleanSearchTitle(link.Text())

	// Раздел: ссылка вида tracker.php?f=NNN&nm=...
	if fl := tr.Find("td.f-name-col a.gen, a.gen.f").First(); fl.Length() > 0 {
		t.Category = strings.TrimSpace(fl.Text())
		if h, ok := fl.Attr("href"); ok {
			if fm := searchForumIDRe.FindStringSubmatch(h); fm != nil {
				t.ForumID, _ = strconv.Atoi(fm[1])
			}
		}
	} else if fl := tr.Find(`a[href*="viewforum.php?f="]`).First(); fl.Length() > 0 {
		t.Category = strings.TrimSpace(fl.Text())
		if h, ok := fl.Attr("href"); ok {
			if fm := searchForumIDRe.FindStringSubmatch(h); fm != nil {
				t.ForumID, _ = strconv.Atoi(fm[1])
			}
		}
	}

	// Размер: точные байты в data-ts_text, иначе текст вида «16.14 GB ↓».
	if sizeCell := tr.Find("td.tor-size").First(); sizeCell.Length() > 0 {
		if v, ok := sizeCell.Attr("data-ts_text"); ok {
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				t.Size = n
			}
		}
		t.SizeText = strings.TrimSpace(sizeCell.Find("a").First().Text())
	} else if sz := tr.Find("a.small.tr-dl.dl-stub").First(); sz.Length() > 0 {
		t.SizeText = strings.TrimSpace(sz.Text())
	}
	if t.Size == 0 && t.SizeText != "" {
		t.Size = ParseSize(t.SizeText)
	}

	// Сиды и личеры.
	if s := tr.Find("b.seedmed").First(); s.Length() > 0 {
		t.Seeds, _ = strconv.Atoi(strings.TrimSpace(s.Text()))
	}
	if p := tr.Find("td.leechmed").First(); p.Length() > 0 {
		t.Peers, _ = strconv.Atoi(strings.TrimSpace(p.Text()))
	}
	// Загрузки.
	if d := tr.Find("td.number-format").First(); d.Length() > 0 {
		t.Downloads, _ = strconv.Atoi(strings.TrimSpace(d.Text()))
	}
	// Дата.
	if d := tr.Find("td.row4 p").First(); d.Length() > 0 {
		t.Date = strings.TrimSpace(d.Text())
	}
	// Проверенная раздача.
	t.Checked = tr.Find("span.tor-approved").Length() > 0 ||
		strings.TrimSpace(tr.Find("td.row1.t-ico").First().Text()) == "√"
	// Автор.
	if a := tr.Find("td.u-name-col a.med").First(); a.Length() > 0 {
		t.Author = strings.TrimSpace(a.Text())
	}
	return t, true
}

// cleanSearchTitle убирает жанровые метки в скобках и лишние пробелы.
func cleanSearchTitle(s string) string {
	s = strings.TrimSpace(s)
	var b strings.Builder
	for _, r := range s {
		if r == ' ' {
			b.WriteRune(' ')
			continue
		}
		b.WriteRune(r)
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// ParseSize переводит «484.4 MB» в байты.
func ParseSize(s string) int64 {
	s = strings.TrimSpace(strings.ToLower(s))
	// Убираем хвост вроде «↓» и неразрывные пробелы.
	s = strings.ReplaceAll(s, " ", " ")
	if m := sizeTextRe.FindStringSubmatch(s); m != nil {
		v, err := strconv.ParseFloat(strings.Replace(m[1], ",", ".", 1), 64)
		if err != nil {
			return 0
		}
		switch strings.ToLower(m[2]) {
		case "kb":
			v *= 1 << 10
		case "mb":
			v *= 1 << 20
		case "gb":
			v *= 1 << 30
		case "tb":
			v *= 1 << 40
		}
		return int64(v)
	}
	m := sizeRe.FindStringSubmatch(s)
	if m == nil {
		return 0
	}
	num := strings.Replace(m[1], ",", ".", 1)
	v, err := strconv.ParseFloat(num, 64)
	if err != nil {
		return 0
	}
	switch m[2] {
	case "kb":
		v *= 1 << 10
	case "mb":
		v *= 1 << 20
	case "gb":
		v *= 1 << 30
	case "tb":
		v *= 1 << 40
	}
	return int64(v)
}

func url_QueryEscape(s string) string {
	return strings.NewReplacer(
		" ", "+",
		"&", "%26",
		"?", "%3F",
		"#", "%23",
		"=", "%3D",
		"+", "%2B",
		"%", "%25",
	).Replace(s)
}
