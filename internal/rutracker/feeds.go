package rutracker

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// FeedEntry — запись Atom-фида раздела.
type FeedEntry struct {
	Title   string
	Author  string
	URL     string
	TopicID int64
	Magnet  string
	InfoHash string
	Trackers []string
	Category string
	Updated time.Time
}

// atomFeed — разбор Atom-фида.
type atomFeed struct {
	XMLName xml.Name `xml:"feed"`
	Title   string    `xml:"title"`
	Entries []struct {
		Title   string `xml:"title"`
		Author  struct {
			Name string `xml:"name"`
		} `xml:"author"`
		Link []struct {
			Href string `xml:"href,attr"`
			Rel  string `xml:"rel,attr"`
			Type string `xml:"type,attr"`
		} `xml:"link"`
		Category struct {
			Term  string `xml:"term,attr"`
			Label string `xml:"label,attr"`
		} `xml:"category"`
		Updated string `xml:"updated"`
	} `xml:"entry"`
}

// Feed возвращает свежие раздачи раздела.
//
// Эндпоинт: https://feed.rutracker.cc/atom/f/<forum_id>.atom (доступен без авторизации).
func (c *Client) Feed(ctx context.Context, forumID int) ([]FeedEntry, error) {
	url := fmt.Sprintf("https://feed.rutracker.cc/atom/f/%d.atom", forumID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.opts.UserAgent)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("rutracker: фид раздела %d вернул HTTP %d", forumID, resp.StatusCode)
	}
	var feed atomFeed
	if err := xml.NewDecoder(resp.Body).Decode(&feed); err != nil {
		return nil, fmt.Errorf("разбор фида: %w", err)
	}
	out := make([]FeedEntry, 0, len(feed.Entries))
	for _, e := range feed.Entries {
		entry := FeedEntry{
			Title:    strings.TrimSpace(e.Title),
			Author:   strings.TrimSpace(e.Author.Name),
			Category: e.Category.Label,
		}
		for _, l := range e.Link {
			switch l.Rel {
			case "enclosure":
				entry.Magnet = l.Href
			default:
				if entry.URL == "" {
					entry.URL = l.Href
				}
			}
		}
		if m := magnetRe.FindStringSubmatch(entry.Magnet); m != nil {
			entry.InfoHash = strings.ToUpper(m[1])
		}
		for _, tr := range trackerRe.FindAllStringSubmatch(entry.Magnet, -1) {
			if dec, err := decodeTracker(tr[1]); err == nil {
				entry.Trackers = append(entry.Trackers, dec)
			}
		}
		if id, err := strconv.ParseInt(topicIDFromURL(entry.URL), 10, 64); err == nil {
			entry.TopicID = id
		}
		if t, err := time.Parse(time.RFC3339, e.Updated); err == nil {
			entry.Updated = t
		}
		out = append(out, entry)
	}
	return out, nil
}

func topicIDFromURL(s string) string {
	if m := topicIDRe.FindStringSubmatch(s); m != nil {
		return m[1]
	}
	return ""
}
