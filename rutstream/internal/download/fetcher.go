package download

import (
	"context"
	"fmt"
	"time"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/mixa7/rutstream/internal/mediaindex"
	"github.com/mixa7/rutstream/internal/torrent"
)

// MetaInfoFunc достаёт metainfo раздачи для задания (обычно — скачивание .torrent с сайта).
type MetaInfoFunc func(ctx context.Context, task *Task) (*metainfo.MetaInfo, error)

// TorrentFetcher выполняет задания через BitTorrent-движок.
type TorrentFetcher struct {
	Engine *torrent.Engine
	// MetaInfo — источник описания раздачи.
	MetaInfo MetaInfoFunc
	// AnnounceOverride — если задано, использовать эти трекеры вместо тех, что в .torrent.
	AnnounceOverride []string
}

// Fetch скачивает файлы задания.
func (f *TorrentFetcher) Fetch(ctx context.Context, task *Task, progress func(done, total int64)) error {
	if f.MetaInfo == nil {
		return fmt.Errorf("не задан источник metainfo")
	}
	mi, err := f.MetaInfo(ctx, task)
	if err != nil {
		return err
	}
	info, err := mi.UnmarshalInfo()
	if err != nil {
		return err
	}
	session, err := f.Engine.Start(ctx, mi, torrent.StartOptions{
		AnnounceURL: firstOr(f.AnnounceOverride),
	})
	if err != nil {
		return err
	}
	defer session.Close()

	if err := session.SetWantedFiles(task.FileIdxs); err != nil {
		return err
	}
	var total int64
	for _, i := range task.FileIdxs {
		if p, err := mediaindex.PlanForFile(&info, i); err == nil {
			total += p.ByteEnd - p.ByteStart
		}
	}
	if total == 0 {
		return fmt.Errorf("нечего скачивать: файлы не определены")
	}

	start := session.Stats().BytesDownloaded
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		st := session.Stats()
		done := st.BytesDownloaded - start
		progress(done, total)
		if done >= total {
			progress(total, total)
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func firstOr(s []string) string {
	if len(s) == 0 {
		return ""
	}
	return s[0]
}
