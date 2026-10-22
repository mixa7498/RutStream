package rutracker

import (
	"context"
	"sort"
	"strings"
)

// ArtistStat — исполнитель в разделе: число раздач и признак lossless.
type ArtistStat struct {
	Artist   string `json:"artist"`
	Count    int    `json:"count"`
	Lossless bool   `json:"lossless"`
	Seeds    int    `json:"seeds"`
}

// SimilarArtists возвращает самых популярных исполнителей раздела.
//
// Источник — листинг раздела (tracker.php?f=NNN): группируем раздачи по
// исполнителю из названия и отдаём тех, у кого больше всего релизов.
// Это и есть рекомендация «похожие исполнители» для текущего раздела.
func (c *Client) SimilarArtists(ctx context.Context, forumID int, limit int) ([]ArtistStat, error) {
	if forumID <= 0 {
		return nil, nil
	}
	if limit <= 0 {
		limit = 12
	}
	topics, err := c.Search(ctx, "", forumID, 0)
	if err != nil {
		return nil, err
	}
	counts := map[string]*ArtistStat{}
	for _, t := range topics {
		artist, _, _ := SplitArtistAlbum(t.Title)
		artist = strings.TrimSpace(artist)
		if artist == "" {
			continue
		}
		st, ok := counts[artist]
		if !ok {
			st = &ArtistStat{Artist: artist}
			counts[artist] = st
		}
		st.Count++
		st.Seeds += t.Seeds
		if IsLosslessTitle(t.Title) {
			st.Lossless = true
		}
	}
	out := make([]ArtistStat, 0, len(counts))
	for _, st := range counts {
		out = append(out, *st)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		if out[i].Lossless != out[j].Lossless {
			return out[i].Lossless
		}
		return out[i].Seeds > out[j].Seeds
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
