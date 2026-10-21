// Command rutstream — десктопное приложение RutStream: каталог rutracker,
// потоковое воспроизведение треков из раздач и загрузка.
//
// Использование:
//
//	rutstream forums                      — дерево разделов музыки
//	rutstream search <запрос> [раздел]     — поиск раздач
//	rutstream show <topicID>              — метаданные и треклист раздачи
//	rutstream plan <topicID>              — что и сколько будет скачано
//	rutstream play <topicID> <трек>       — потоковое воспроизведение трека
//	rutstream download <topicID> <трек>   — скачать трек целиком
//	rutstream history                     — история прослушиваний
package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/mixa7/rutstream/internal/audio"
	"github.com/mixa7/rutstream/internal/catalog"
	"github.com/mixa7/rutstream/internal/mediaindex"
	"github.com/mixa7/rutstream/internal/playback"
	"github.com/mixa7/rutstream/internal/rutracker"
	"github.com/mixa7/rutstream/internal/torrent"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	ctx := context.Background()
	var err error
	switch os.Args[1] {
	case "forums":
		err = cmdForums(ctx)
	case "search":
		err = cmdSearch(ctx, os.Args[2:])
	case "show":
		err = cmdShow(ctx, os.Args[2:])
	case "plan":
		err = cmdPlan(ctx, os.Args[2:])
	case "play":
		err = cmdPlay(ctx, os.Args[2:])
	case "download":
		err = cmdDownload(ctx, os.Args[2:])
	case "history":
		err = cmdHistory(ctx)
	case "-h", "--help", "help":
		usage()
		return
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "ОШИБКА:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `RutStream — музыка с rutracker.org через BitTorrent

  rutstream forums                      дерево разделов музыки
  rutstream search <запрос> [раздел]     поиск раздач (раздел — id, например 1698)
  rutstream show <topicID>              метаданные и треклист раздачи
  rutstream plan <topicID>              расчёт: что скачается при выборе трека
  rutstream play <topicID> <трек>       потоковое воспроизведение (1 — первый трек)
  rutstream download <topicID> <трек>   скачать трек целиком
  rutstream history                     история прослушиваний

Переменные окружения:
  RUTSTREAM_USER / RUTSTREAM_PASS  логин и пароль rutracker
  RUTSTREAM_COOKIE                готовая cookie bb_session
  RUTSTREAM_PROXY                 socks5://host:port для сайта и пиров
  RUTSTREAM_DATA                  каталог данных (по умолчанию %APPDATA%/RutStream)`)
}

// ---------------------------------------------------------------- каркас --

type app struct {
	client   *rutracker.Client
	store    *catalog.Store
	engine   *torrent.Engine
	dataDir  string
	loggedIn bool
}

func newApp() (*app, error) {
	dataDir := os.Getenv("RUTSTREAM_DATA")
	if dataDir == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			base = os.TempDir()
		}
		dataDir = filepath.Join(base, "RutStream")
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, err
	}
	opts := rutracker.Options{
		ProxyURL: os.Getenv("RUTSTREAM_PROXY"),
		Timeout:  30 * time.Second,
	}
	c, err := rutracker.New(opts)
	if err != nil {
		return nil, err
	}
	store, err := catalog.Open(filepath.Join(dataDir, "catalog.db"))
	if err != nil {
		return nil, err
	}
	engine, err := torrent.New(torrent.Options{
		DataDir:       filepath.Join(dataDir, "data"),
		ProxyURL:      os.Getenv("RUTSTREAM_PROXY"),
		DisableDHT:    true,
		DisablePEX:    true,
		DisableWebseeds: true,
		Seed:          true,
	})
	if err != nil {
		store.Close()
		return nil, err
	}
	a := &app{client: c, store: store, engine: engine, dataDir: dataDir}
	a.applySession()
	return a, nil
}

func (a *app) close() {
	a.engine.Close()
	a.store.Close()
}

// applySession переносит учётные данные из окружения.
func (a *app) applySession() {
	if ck := strings.TrimSpace(os.Getenv("RUTSTREAM_COOKIE")); ck != "" {
		a.client.SetSessionCookie(ck)
		a.loggedIn = true
		return
	}
	user := os.Getenv("RUTSTREAM_USER")
	pass := os.Getenv("RUTSTREAM_PASS")
	if user == "" || pass == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := a.client.Login(ctx, user, pass); err != nil {
		fmt.Fprintln(os.Stderr, "предупреждение: не удалось войти:", err)
		return
	}
	a.loggedIn = true
}

// ---------------------------------------------------------------- команды --

func cmdForums(ctx context.Context) error {
	a, err := newApp()
	if err != nil {
		return err
	}
	defer a.close()
	forums, err := a.loadForums(ctx)
	if err != nil {
		return err
	}
	curCat := ""
	for _, f := range forums {
		if f.CatTitle != curCat {
			curCat = f.CatTitle
			fmt.Printf("\n== %s ==\n", curCat)
		}
		fmt.Printf("  %-6d %s\n", f.ID, f.Title)
	}
	return nil
}

// loadForums читает дерево разделов из кэша или с сайта.
func (a *app) loadForums(ctx context.Context) ([]catalog.Forum, error) {
	if forums, err := a.store.Forums(ctx); err == nil && len(forums) > 0 {
		return forums, nil
	}
	raw, err := a.client.Forums(ctx)
	if err != nil {
		return nil, err
	}
	forums := make([]catalog.Forum, 0, len(raw))
	for _, f := range raw {
		forums = append(forums, catalog.Forum{
			ID: f.ID, CatID: f.CatID, CatTitle: f.CatTitle, Title: f.Title, URL: f.URL,
		})
	}
	if err := a.store.SaveForums(ctx, forums); err != nil {
		return nil, err
	}
	return forums, nil
}

func cmdSearch(ctx context.Context, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("нужен поисковый запрос")
	}
	query := args[0]
	var forumID int64
	if len(args) > 1 {
		v, _ := strconv.ParseInt(args[1], 10, 64)
		forumID = v
	}
	a, err := newApp()
	if err != nil {
		return err
	}
	defer a.close()

	if forumID == 0 {
		forums, err := a.loadForums(ctx)
		if err != nil {
			return err
		}
		// По умолчанию ищем по первому музыкальному разделу.
		for _, f := range forums {
			switch f.CatID {
			case 8, 22, 23, 31, 35, 37:
				forumID = f.ID
			}
			if forumID != 0 {
				break
			}
		}
		if forumID == 0 && len(forums) > 0 {
			forumID = forums[0].ID
		}
	}
	topics, err := a.client.Search(ctx, query, int(forumID), 0)
	if err != nil {
		return fmt.Errorf("поиск: %w (если видите ошибку Cloudflare — укажите RUTSTREAM_COOKIE)", err)
	}
	fmt.Printf("Найдено %d раздач в разделе %d\n\n", len(topics), forumID)
	for _, t := range topics {
		lossless := ""
		if isLosslessTitle(t.Title) {
			lossless = "lossless"
		}
		fmt.Printf("[%d] %s\n", t.ID, t.Title)
		fmt.Printf("     %s | сиды %d | личеры %d | %s | %s\n", t.SizeText, t.Seeds, t.Peers, t.Date, lossless)
		cp := catalog.Topic{
			ID: t.ID, ForumID: forumID, Title: t.Title, InfoHash: t.InfoHash, Size: t.Size,
			Seeds: t.Seeds, Peers: t.Peers, Downloads: t.Downloads, Lossless: isLosslessTitle(t.Title),
		}
		if err := a.store.SaveTopic(ctx, cp); err != nil {
			return err
		}
	}
	return nil
}

func cmdShow(ctx context.Context, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("нужен topicID")
	}
	id, _ := strconv.ParseInt(args[0], 10, 64)
	a, err := newApp()
	if err != nil {
		return err
	}
	defer a.close()

	topic, err := a.client.Topic(ctx, id)
	if err != nil {
		return fmt.Errorf("чтение темы: %w (нужна авторизация: RUTSTREAM_COOKIE)", err)
	}
	fmt.Printf("Тема #%d: %s\n", topic.ID, topic.Title)
	fmt.Printf("hash: %s\n", topic.InfoHash)
	fmt.Printf("трекеры: %s\n", strings.Join(topic.Trackers, ", "))
	fmt.Printf("исполнитель: %s | альбом: %s | год: %s\n", topic.Artist, topic.Album, topic.Year)
	fmt.Printf("жанр: %s | формат: %s | битрейт: %s\n", topic.Genre, topic.Format, topic.Bitrate)
	fmt.Printf("треков в треклисте: %d\n", len(topic.Tracks))
	for _, t := range topic.Tracks {
		fmt.Printf("  %2d. %-55s %s\n", t.Pos, t.Title, dur(t.Duration))
	}

	// Список внутренних файлов (если доступен).
	files, err := a.client.TorrentFiles(ctx, id)
	if err == nil && len(files) > 0 {
		fmt.Printf("\nФайлов в раздаче: %d\n", len(files))
		for i, f := range files {
			if i >= 20 {
				fmt.Printf("  … ещё %d\n", len(files)-i)
				break
			}
			fmt.Printf("  %-60s %s\n", f.Path, f.SizeText)
		}
	}
	return nil
}

func cmdPlan(ctx context.Context, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("нужен topicID")
	}
	id, _ := strconv.ParseInt(args[0], 10, 64)
	a, err := newApp()
	if err != nil {
		return err
	}
	defer a.close()

	mi, info, err := a.fetchTorrent(ctx, id)
	if err != nil {
		return err
	}
	ix, err := mediaindex.Build(info, mediaindex.Hints{})
	if err != nil {
		return err
	}
	total := info.TotalLength()
	fmt.Printf("Раздача: %s | piece %s | piece'ов %d | файлов %d\n\n",
		human(total), human(info.PieceLength), info.NumPieces(), len(ix.Files))
	fmt.Printf("%-4s %-10s %-11s %-9s %-8s %s\n", "№", "размер", "скачаем", "лишние", "кодек", "файл")
	for i, t := range ix.Tracks {
		bytes, _, extra, _ := mediaindex.EstimateDownload(info, t.FileIdx)
		fmt.Printf("%-4d %-10s %-11s %-9s %-8s %s\n",
			i+1, human(t.ByteEnd-t.ByteStart), human(bytes), human(extra), t.Codec, t.Path)
		if i >= 30 {
			fmt.Printf("… ещё %d треков\n", len(ix.Tracks)-i-1)
			break
		}
	}
	fmt.Printf("\nИтого раздача: %s. Скачивается только нужный трек.\n", human(total))
	_ = mi
	return nil
}

func cmdPlay(ctx context.Context, args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("нужны topicID и номер трека")
	}
	id, _ := strconv.ParseInt(args[0], 10, 64)
	trackNo, _ := strconv.Atoi(args[1])
	a, err := newApp()
	if err != nil {
		return err
	}
	defer a.close()

	mi, info, err := a.fetchTorrent(ctx, id)
	if err != nil {
		return err
	}
	ix, err := mediaindex.Build(info, mediaindex.Hints{})
	if err != nil {
		return err
	}
	if trackNo < 1 || trackNo > len(ix.Tracks) {
		return fmt.Errorf("трек №%d вне диапазона (1..%d)", trackNo, len(ix.Tracks))
	}
	track := ix.Tracks[trackNo-1]
	bytes, _, extra, _ := mediaindex.EstimateDownload(info, track.FileIdx)
	fmt.Printf("Трек: %s\n", track.Path)
	fmt.Printf("Скачаем %s (из раздачи %s), лишних байт: %s\n",
		human(bytes), human(info.TotalLength()), human(extra))

	session, err := a.engine.Start(ctx, mi, torrent.StartOptions{
		Trackers: trackersOf(mi),
	})
	if err != nil {
		return err
	}
	defer session.Close()

	player := playback.New(session)
	wav := filepath.Join(a.dataDir, "preview.wav")
	res, err := player.PlayTrack(ctx, playback.Track{
		FileIdx: track.FileIdx,
		Path:    track.Path,
		Codec:   track.Codec,
		Title:   track.Title,
	}, wav)
	if err != nil {
		return err
	}
	fmt.Printf("\nВоспроизведено: %d сэмплов (%.1f с), формат %s\n",
		res.Samples, res.DurationSec, formatName(res.Format))
	fmt.Printf("Скачано по сети: %s | время до конца: %s\n", human(res.BytesDownloaded), res.Elapsed.Round(time.Millisecond))
	fmt.Printf("WAV: %s\n", wav)
	return nil
}

func cmdDownload(ctx context.Context, args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("нужны topicID и номер трека")
	}
	id, _ := strconv.ParseInt(args[0], 10, 64)
	trackNo, _ := strconv.Atoi(args[1])
	a, err := newApp()
	if err != nil {
		return err
	}
	defer a.close()

	mi, info, err := a.fetchTorrent(ctx, id)
	if err != nil {
		return err
	}
	ix, err := mediaindex.Build(info, mediaindex.Hints{})
	if err != nil {
		return err
	}
	if trackNo < 1 || trackNo > len(ix.Tracks) {
		return fmt.Errorf("трек №%d вне диапазона (1..%d)", trackNo, len(ix.Tracks))
	}
	track := ix.Tracks[trackNo-1]
	session, err := a.engine.Start(ctx, mi, torrent.StartOptions{Trackers: trackersOf(mi)})
	if err != nil {
		return err
	}
	defer session.Close()
	if err := session.SetWantedFiles([]int{track.FileIdx}); err != nil {
		return err
	}
	fmt.Printf("Скачиваю %s…\n", track.Path)
	deadline := time.Now().Add(30 * time.Minute)
	for time.Now().Before(deadline) {
		st := session.Stats()
		fmt.Printf("\r  %s / %s (piece'ов %d/%d)   ", human(st.BytesDownloaded),
			human(track.ByteEnd-track.ByteStart), st.PiecesComplete, st.PiecesTotal)
		if st.BytesDownloaded >= track.ByteEnd-track.ByteStart {
			break
		}
		time.Sleep(time.Second)
	}
	fmt.Println()
	return nil
}

func cmdHistory(ctx context.Context) error {
	a, err := newApp()
	if err != nil {
		return err
	}
	defer a.close()
	h, err := a.store.History(ctx, 50)
	if err != nil {
		return err
	}
	if len(h) == 0 {
		fmt.Println("История пуста.")
		return nil
	}
	for _, e := range h {
		fmt.Printf("%s  %-40s %s\n", time.Unix(e.PlayedAt, 0).Format("2006-01-02 15:04"), e.Artist, e.Title)
	}
	return nil
}

// -------------------------------------------------------------- помощники --

// fetchTorrent скачивает .torrent раздачи и разбирает его.
func (a *app) fetchTorrent(ctx context.Context, topicID int64) (*metainfo.MetaInfo, *metainfo.Info, error) {
	data, err := a.client.DownloadTorrent(ctx, topicID)
	if err != nil {
		return nil, nil, fmt.Errorf("скачивание .torrent: %w (нужна авторизация: RUTSTREAM_COOKIE)", err)
	}
	mi, err := metainfo.Load(bytesReader(data))
	if err != nil {
		return nil, nil, fmt.Errorf("разбор .torrent: %w", err)
	}
	info, err := mi.UnmarshalInfo()
	if err != nil {
		return nil, nil, err
	}
	return mi, &info, nil
}

func trackersOf(mi *metainfo.MetaInfo) [][]string {
	list := mi.UpvertedAnnounceList().DistinctValues()
	if len(list) == 0 {
		return nil
	}
	return [][]string{list}
}

func isLosslessTitle(title string) bool {
	t := strings.ToLower(title)
	for _, m := range []string{"flac", "alac", "ape", "wav", "wv]", "lossless", "24bit", "24-bit", "flac 24"} {
		if strings.Contains(t, m) {
			return true
		}
	}
	return false
}

func dur(sec float64) string {
	if sec <= 0 {
		return ""
	}
	return fmt.Sprintf("%d:%02d", int(sec)/60, int(sec)%60)
}

func human(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.2f ГБ", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.2f МБ", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f КБ", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d Б", n)
}

func formatName(f audio.Format) string {
	return fmt.Sprintf("%d Гц, %d кан.", f.SampleRate, f.Channels)
}

// bytesReader оборачивает срез байт в io.Reader.
func bytesReader(b []byte) io.Reader { return bytes.NewReader(b) }
