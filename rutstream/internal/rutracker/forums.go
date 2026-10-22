package rutracker

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"

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

// SetMusicForumIDs задаёт готовый набор id музыкальных разделов с подразделами.
//
// Позволяет не обходить форум повторно: приложение один раз собирает список и
// кэширует его (на диске), дальше список просто передаётся клиенту.
func (c *Client) SetMusicForumIDs(ids map[int]bool) {
	c.musicMu.Lock()
	c.musicIDs = ids
	c.musicMu.Unlock()
}

// storedMusicForumIDs возвращает заданный набор id (nil — если не задан).
func (c *Client) storedMusicForumIDs() map[int]bool {
	c.musicMu.Lock()
	defer c.musicMu.Unlock()
	return c.musicIDs
}

// ForumSubIDs возвращает id подразделов форума.
//
// Раздачи лежат не в самих разделах («Rock»), а в их подразделах
// («Rock (Hi-Res)», «Rock (lossless)»). Результаты кэшируются: обход
// разделов — это десятки запросов к сайту.
func (c *Client) ForumSubIDs(ctx context.Context, forumID int64) ([]int64, error) {
	c.subMu.Lock()
	if ids, ok := c.subIDs[forumID]; ok {
		c.subMu.Unlock()
		return ids, nil
	}
	c.subMu.Unlock()

	page, err := c.Get(ctx, fmt.Sprintf("/forum/viewforum.php?f=%d", forumID))
	if err != nil {
		return nil, err
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(page)))
	if err != nil {
		return nil, err
	}
	var ids []int64
	seen := map[int64]bool{}
	doc.Find("a[href*='viewforum.php?f=']").Each(func(_ int, a *goquery.Selection) {
		href, _ := a.Attr("href")
		m := forumIDRe.FindStringSubmatch(href)
		if m == nil {
			return
		}
		id, _ := strconv.ParseInt(m[1], 10, 64)
		if id == 0 || id == forumID || seen[id] {
			return
		}
		seen[id] = true
		ids = append(ids, id)
	})
	c.subMu.Lock()
	if c.subIDs == nil {
		c.subIDs = map[int64][]int64{}
	}
	c.subIDs[forumID] = ids
	c.subMu.Unlock()
	return ids, nil
}

// MusicForumIDs возвращает id всех музыкальных разделов вместе с их
// подразделами. Результат кэшируется в клиенте.
func (c *Client) MusicForumIDs(ctx context.Context, forums []Forum) map[int]bool {
	out := map[int]bool{}
	music := MusicForums(forums)
	for _, f := range music {
		out[int(f.ID)] = true
	}
	// Подразделы собираем параллельно: разделов десятки, запрос быстрый.
	sem := make(chan struct{}, 6)
	var wg sync.WaitGroup
	var mu sync.Mutex
	for _, f := range music {
		wg.Add(1)
		go func(id int64) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			ids, err := c.ForumSubIDs(ctx, id)
			if err != nil {
				return
			}
			mu.Lock()
			for _, sid := range ids {
				out[int(sid)] = true
			}
			mu.Unlock()
		}(f.ID)
	}
	wg.Wait()
	return out
}
