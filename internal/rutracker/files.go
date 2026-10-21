package rutracker

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

// decodePercent декодирует percent-encoding.
func decodePercent(s string) (string, error) {
	return url.QueryUnescape(s)
}

// TorrentFiles запрашивает список внутренних файлов раздачи.
//
// Эндпоинт: POST /forum/viewtorrent.php с телом t=<topic_id>.
func (c *Client) TorrentFiles(ctx context.Context, topicID int64) ([]TopicFile, error) {
	form := url.Values{}
	form.Set("t", fmt.Sprintf("%d", topicID))
	page, err := c.Post(ctx, "/forum/viewtorrent.php", form)
	if err != nil {
		return nil, err
	}
	return ParseTorrentFiles(page)
}

// ParseTorrentFiles разбирает дерево файлов раздачи.
func ParseTorrentFiles(page []byte) ([]TopicFile, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(page)))
	if err != nil {
		return nil, err
	}
	var out []TopicFile
	doc.Find("ul.ftree > li.dir > ul > li").Each(func(_ int, li *goquery.Selection) {
		name := strings.TrimSpace(li.Find("b").First().Text())
		if name == "" {
			return
		}
		sizeText := strings.TrimSpace(li.Find("i").First().Text())
		out = append(out, TopicFile{Path: name, SizeText: sizeText, Size: ParseSize(sizeText)})
	})
	return out, nil
}

// DownloadTorrent скачивает .torrent-файл раздачи.
//
// Эндпоинт: GET /forum/dl.php?t=<topic_id> (требует авторизации).
func (c *Client) DownloadTorrent(ctx context.Context, topicID int64) ([]byte, error) {
	body, err := c.Get(ctx, fmt.Sprintf("/forum/dl.php?t=%d", topicID))
	if err != nil {
		return nil, err
	}
	if len(body) == 0 {
		return nil, fmt.Errorf("rutracker: пустой .torrent")
	}
	if body[0] != 'd' && body[0] != 'l' {
		return nil, fmt.Errorf("rutracker: ответ не является bencode (возможно, HTML-страница)")
	}
	return body, nil
}
