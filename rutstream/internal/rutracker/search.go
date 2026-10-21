package rutracker

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"regexp"
)

// Topic — раздача (тема форума).
type Topic struct {
	ID       int64  `json:"id"`
	Title    string `json:"title"`
	ForumID  int    `json:"forumId"`
	Category string `json:"category"`
	SizeText string `json:"sizeText"`
	Size     int64  `json:"size"`
	Seeds    int    `json:"seeds"`
	Peers    int    `json:"peers"`
	Rating   int    `json:"rating"`
	Downloads int   `json:"downloads"`
	Date     string `json:"date"`
	Checked  bool   `json:"checked"`
	Magnet   string `json:"magnet"`
	InfoHash string `json:"infoHash"`
	Trackers []string `json:"trackers"`
	// Метаданные из тела темы.
	Artist   string `json:"artist"`
	Album    string `json:"album"`
	Year     string `json:"year"`
	Genre    string `json:"genre"`
	Format   string `json:"format"`
	Bitrate  string `json:"bitrate"`
	Duration string `json:"duration"`
	Quality  string `json:"quality"`
	Tracks   []SiteTrack `json:"tracks"`
	Files    []TopicFile  `json:"files"`
}

// SiteTrack — трек из треклиста темы.
type SiteTrack struct {
	Pos      int     `json:"pos"`
	Disc     int     `json:"disc"`
	Title    string  `json:"title"`
	Duration float64 `json:"duration"`
}

// TopicFile — файл внутри торрента (по списку rutracker).
type TopicFile struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
	SizeText string `json:"sizeText"`
}

var topicIDRe = regexp.MustCompile(`t=(\d+)`)
var sizeRe = regexp.MustCompile(`(?i)^([\d.,]+)\s*(bytes|kb|mb|gb|tb|b)?$`)

// Search ищет раздачи в разделе форума.
//
// Используется эндпоинт tracker.php: /forum/tracker.php?nm=<запрос>&f=<forum_id>&start=<смещение>
func (c *Client) Search(ctx context.Context, query string, forumID int, start int) ([]Topic, error) {
	path := fmt.Sprintf("/forum/tracker.php?nm=%s&f=%d&start=%d",
		url_QueryEscape(query), forumID, start)
	page, err := c.Get(ctx, path)
	if err != nil {
		return nil, err
	}
	return ParseSearchResults(page)
}

// ParseSearchResults разбирает HTML-таблицу результатов поиска.
func ParseSearchResults(page []byte) ([]Topic, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(page)))
	if err != nil {
		return nil, err
	}
	var out []Topic
	doc.Find("table.forumline tbody tr, .forumline tbody tr, tbody tr").Each(func(_ int, tr *goquery.Selection) {
		link := tr.Find(".row4 .med").First()
		if link.Length() == 0 {
			link = tr.Find("a.med").First()
		}
		href, _ := link.Attr("href")
		if href == "" {
			return
		}
		m := topicIDRe.FindStringSubmatch(href)
		if m == nil {
			return
		}
		id, _ := strconv.ParseInt(m[1], 10, 64)
		t := Topic{
			ID:       id,
			Title:    strings.TrimSpace(link.Text()),
			Category: strings.TrimSpace(tr.Find(".row1 .gen").First().Text()),
			Date:     strings.TrimSpace(tr.Find("td.row4 p").First().Text()),
			Checked:  strings.TrimSpace(tr.Find("td.row1.t-ico").First().Text()) == "√",
		}
		if s := strings.TrimSpace(tr.Find("a.small.tr-dl.dl-stub").First().Text()); s != "" {
			t.SizeText = strings.Fields(s)[0]
			t.Size = ParseSize(t.SizeText)
		}
		if s := strings.TrimSpace(tr.Find("b.seedmed").First().Text()); s != "" {
			t.Seeds, _ = strconv.Atoi(strings.Fields(s)[0])
		}
		if s := strings.TrimSpace(tr.Find("td.row4.leechmed.bold").First().Text()); s != "" {
			t.Peers, _ = strconv.Atoi(strings.Fields(s)[0])
		}
		if s := strings.TrimSpace(tr.Find("td.row4.small.number-format").First().Text()); s != "" {
			t.Downloads, _ = strconv.Atoi(strings.Fields(s)[0])
		}
		out = append(out, t)
	})
	return out, nil
}

// ParseSize переводит «484.4 MB» в байты.
func ParseSize(s string) int64 {
	s = strings.TrimSpace(strings.ToLower(s))
	m := sizeRe.FindStringSubmatch(s)
	if m == nil {
		return 0
	}
	num := strings.Replace(m[1], ",", ".", 1)
	v, err := strconv.ParseFloat(num, 64)
	if err != nil {
		return 0
	}
	switch strings.ToLower(m[2]) {
	case "b", "bytes":
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
