package download

import (
	"context"
	"fmt"
	"time"

	"github.com/anacrolix/torrent/metainfo"
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
//
// Прогресс считается по реальной готовности файлов (сколько байт piece'ов
// лежит на диске), а не по сетевому счётчику: тот учитывает служебный трафик
// и выравнивание по piece'ам.
func (f *TorrentFetcher) Fetch(ctx context.Context, task *Task, progress func(done, total int64)) error {
	if f.MetaInfo == nil {
		return fmt.Errorf("не задан источник metainfo")
	}
	mi, err := f.MetaInfo(ctx, task)
	if err != nil {
		return err
	}
	if _, err := mi.UnmarshalInfo(); err != nil {
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
	// Убираем «дырявые» файлы от прошлых запусков: anacrolix создаёт их
	// при записи граничных piece'ов (полный размер, но с пропусками).
	// Сейчас их можно удалить — файлы ещё не открыты.
	if removed := session.CleanupIncompleteFiles(); len(removed) > 0 {
		fmt.Printf("удалено частичных файлов: %d\n", len(removed))
	}
	// Снимок файлов до загрузки: после неё anacrolix может создать
	// «дырявых» соседей по границам piece'ов.
	before := session.ListFiles()
	_, total := session.FilesProgress(task.FileIdxs)
	if total == 0 {
		return fmt.Errorf("нечего скачивать: файлы не определены")
	}
	progress(0, total)

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		done, total := session.FilesProgress(task.FileIdxs)
		progress(done, total)
		if done >= total {
			progress(total, total)
			// Собираем «дырявые» файлы-соседи: anacrolix создаёт их при
			// записи граничных piece'ов (полный размер, но с пропусками).
			// Удалить их можно только после закрытия клиента — пока файл
			// открыт, Windows его не удалит.
			keep := make(map[int]bool, len(task.FileIdxs))
			for _, i := range task.FileIdxs {
				keep[i] = true
			}
			task.PartialFiles = session.NewFilesSince(before, keep)
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
