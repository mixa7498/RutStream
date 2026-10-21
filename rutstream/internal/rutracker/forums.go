package rutracker

import (
	"context"
	"regexp"
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

var catIDRe = regexp.MustCompile(`c=(\d+)`)
var forumIDRe = regexp.MustCompile(`f=(\d+)`)

// Forum — раздел форума (с категорией).
type Forum struct {
	ID       int64  `json:"id"`
	CatID    int64  `json:"catId"`
	CatTitle string `json:"catTitle"`
	Title    string `json:"title"`
	URL      string `json:"url"`
}

// Forums читает дерево разделов форума (страница index.php).
func (c *Client) Forums(ctx context.Context) ([]Forum, error) {
	page, err := c.Get(ctx, "/forum/index.php")
	if err != nil {
		return nil, err
	}
	return ParseForums(page, c.mirror)
}

// ParseForums разбирает дерево разделов со страницы index.php.
func ParseForums(page []byte, mirror string) ([]Forum, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(page)))
	if err != nil {
		return nil, err
	}
	var out []Forum
	doc.Find("div.category").Each(func(_ int, cat *goquery.Selection) {
		catTitle := ""
		var catID int64
		if a := cat.Find("h3.cat_title a").First(); a.Length() > 0 {
			catTitle = strings.TrimSpace(a.Text())
			if href, ok := a.Attr("href"); ok {
				if m := catIDRe.FindStringSubmatch(href); m != nil {
					catID, _ = strconv.ParseInt(m[1], 10, 64)
				}
			}
		}
		cat.Find("tr[id^=f-]").Each(func(_ int, tr *goquery.Selection) {
			a := tr.Find("h4.forumlink a").First()
			if a.Length() == 0 {
				return
			}
			title := strings.TrimSpace(a.Text())
			href, _ := a.Attr("href")
			var id int64
			if m := forumIDRe.FindStringSubmatch(href); m != nil {
				id, _ = strconv.ParseInt(m[1], 10, 64)
			}
			if id == 0 {
				return
			}
			out = append(out, Forum{
				ID:       id,
				CatID:    catID,
				CatTitle: catTitle,
				Title:    title,
				URL:      mirror + "/forum/" + href,
			})
		})
	})
	return out, nil
}

// MusicForums возвращает только музыкальные разделы (категории 8, 22, 23, 31, 35, 37).
func MusicForums(forums []Forum) []Forum {
	musicCats := map[int64]bool{8: true, 22: true, 23: true, 31: true, 35: true, 37: true}
	var out []Forum
	for _, f := range forums {
		if musicCats[f.CatID] {
			out = append(out, f)
		}
	}
	return out
}
