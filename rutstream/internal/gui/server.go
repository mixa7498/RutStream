// Package gui — локальный веб-сервер и окно приложения RutStream.
//
// Архитектура: приложение поднимает HTTP-сервер на 127.0.0.1 и открывает
// окно WebView2 с этим адресом. Фронтенд — обычные HTML/CSS/JS, встроенные
// в бинарник (go:embed), без сборщика и Node.js.
package gui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/mixa7/rutstream/internal/audio"
	"github.com/mixa7/rutstream/internal/catalog"
	"github.com/mixa7/rutstream/internal/download"
	"github.com/mixa7/rutstream/internal/mediaindex"
	"github.com/mixa7/rutstream/internal/playback"
	"github.com/mixa7/rutstream/internal/rutracker"
	"github.com/mixa7/rutstream/internal/torrent"
)

// Options — параметры сервера.
type Options struct {
	Client  *rutracker.Client
	Engine  *torrent.Engine
	Store   *catalog.Store
	DataDir string
	Logger  *slog.Logger
	// FetchTorrent — загрузка .torrent по id темы (по умолчанию — клиент).
	FetchTorrent func(ctx context.Context, topicID int64) (*metainfo.MetaInfo, error)
}

// Server — локальный сервер приложения.
type Server struct {
	opts Options
	log  *slog.Logger

	mu       sync.Mutex
	session  *torrent.Session
	player   *playback.Player
	play     PlayStatus
	cancel   context.CancelFunc
	mgr      *download.Manager
	fetcher  *download.TorrentFetcher
	listener net.Listener
	srv      *http.Server
}

// PlayStatus — состояние воспроизведения.
type PlayStatus struct {
	TopicID  int64   `json:"topicId"`
	Track    int     `json:"track"`
	Title    string  `json:"title"`
	State    string  `json:"state"` // idle | preparing | playing | done | error
	Error    string  `json:"error,omitempty"`
	Samples  int     `json:"samples"`
	Duration float64 `json:"durationSec"`
	Elapsed  float64 `json:"elapsedSec"`
	Download int64   `json:"bytesDownloaded"`
	// TotalBytes — ожидаемый объём данных трека (для полосы прогресса).
	TotalBytes int64 `json:"totalBytes"`
}

// New создаёт сервер.
func New(opts Options) *Server {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &Server{
		opts: opts,
		log:  opts.Logger,
		play: PlayStatus{State: "idle"},
	}
}

// Start поднимает сервер на свободном локальном порту.
func (s *Server) Start(ctx context.Context) (string, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("запуск сервера: %w", err)
	}
	s.listener = ln
	mux := http.NewServeMux()
	mux.HandleFunc("/api/", s.handleAPI)
	mux.Handle("/", s.handleAssets())
	s.srv = &http.Server{Handler: mux}
	go func() {
		if err := s.srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			s.log.Error("сервер остановился", "err", err)
		}
	}()
	return "http://" + ln.Addr().String(), nil
}

// Stop останавливает сервер и активные задания.
func (s *Server) Stop() {
	s.StopPlayback()
	if s.srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = s.srv.Shutdown(ctx)
	}
}

// ---------------------------------------------------------------------------
// API
// ---------------------------------------------------------------------------

func (s *Server) handleAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	switch strings.TrimPrefix(r.URL.Path, "/api/") {
	case "state":
		s.apiState(w, r)
	case "sections":
		s.apiSections(w, r)
	case "search":
		s.apiSearch(w, r)
	case "topic":
		s.apiTopic(w, r)
	case "plan":
		s.apiPlan(w, r)
	case "play":
		s.apiPlay(w, r)
	case "stop":
		s.apiStop(w, r)
	case "download":
		s.apiDownload(w, r)
	default:
		http.NotFound(w, r)
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, err error) {
	writeJSON(w, map[string]string{"error": err.Error()})
}

// apiState — общее состояние приложения.
func (s *Server) apiState(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	play := s.play
	s.mu.Unlock()
	downloads := []download.Task{}
	if s.mgr != nil {
		downloads = s.mgr.Tasks()
	}
	writeJSON(w, map[string]any{
		"loggedIn": s.opts.Client != nil && s.opts.Client.SessionCookie() != "",
		"playing":  play,
		"downloads": downloads,
		"hasDevice": audio.DeviceAvailable(),
	})
}

// apiSections — дерево музыкальных разделов.
func (s *Server) apiSections(w http.ResponseWriter, r *http.Request) {
	if s.opts.Client == nil {
		writeErr(w, fmt.Errorf("нет подключения к сайту"))
		return
	}
	forums, err := s.opts.Client.Forums(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, rutracker.MusicForums(forums))
}

// apiSearch — поиск по сайту.
func (s *Server) apiSearch(w http.ResponseWriter, r *http.Request) {
	if s.opts.Client == nil {
		writeErr(w, fmt.Errorf("нет подключения к сайту"))
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeJSON(w, []rutracker.Topic{})
		return
	}
	forumID, _ := strconv.Atoi(r.URL.Query().Get("forum"))
	topics, err := s.opts.Client.Search(r.Context(), q, forumID, 0)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, topics)
}

// apiTopic — тема раздачи: описание, треклист, файлы.
func (s *Server) apiTopic(w http.ResponseWriter, r *http.Request) {
	if s.opts.Client == nil {
		writeErr(w, fmt.Errorf("нет подключения к сайту"))
		return
	}
	id, _ := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	topic, err := s.opts.Client.Topic(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, topic)
}

// apiPlan — треки раздачи и оценка объёма скачивания.
func (s *Server) apiPlan(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, _ := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	mi, info, err := s.fetchTorrent(ctx, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	session, err := s.openSession(ctx, mi)
	if err != nil {
		writeErr(w, err)
		return
	}
	ix, err := (&playback.IndexBuilder{Session: session, Info: info}).Build(ctx)
	if err != nil {
		writeErr(w, err)
		return
	}
	type Track struct {
		No        int     `json:"no"`
		Title     string  `json:"title"`
		Duration  float64 `json:"duration"`
		Size      int64   `json:"size"`
		Download  int64   `json:"download"`
		Extra     int64   `json:"extra"`
		Codec     string  `json:"codec"`
		Lossless  bool    `json:"lossless"`
		Source    string  `json:"source"`
		Position  string  `json:"position"`
	}
	tracks := make([]Track, 0, len(ix.Tracks))
	for i, t := range ix.Tracks {
		bytes, _, extra := mediaindex.EstimateTrack(info, t)
		tracks = append(tracks, Track{
			No:       i + 1,
			Title:    t.Title,
			Duration: t.DurationSec,
			Size:     t.ByteEnd - t.ByteStart,
			Download: bytes,
			Extra:    extra,
			Codec:    t.Codec,
			Lossless: t.Lossless,
			Source:   t.Source,
			Position: t.Path,
		})
	}
	writeJSON(w, map[string]any{
		"topicId": id,
		"total":   info.TotalLength(),
		"tracks":  tracks,
	})
}

// apiPlay — запустить воспроизведение трека.
func (s *Server) apiPlay(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID    int64 `json:"id"`
		Track int   `json:"track"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, err)
		return
	}
	if err := s.startPlayback(r.Context(), req.ID, req.Track); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

// apiStop — остановить воспроизведение.
func (s *Server) apiStop(w http.ResponseWriter, r *http.Request) {
	s.StopPlayback()
	writeJSON(w, map[string]any{"ok": true})
}

// apiDownload — поставить задание в очередь загрузок.
func (s *Server) apiDownload(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID   int64  `json:"id"`
		What string `json:"what"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, err)
		return
	}
	if err := s.enqueueDownload(r.Context(), req.ID, req.What); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

// ---------------------------------------------------------------------------
// Воспроизведение
// ---------------------------------------------------------------------------

// startPlayback запускает воспроизведение трека в фоне.
func (s *Server) startPlayback(ctx context.Context, topicID int64, trackNo int) error {
	s.StopPlayback()
	if trackNo < 1 {
		trackNo = 1
	}
	mi, info, err := s.fetchTorrent(ctx, topicID)
	if err != nil {
		return err
	}
	session, err := s.openSession(ctx, mi)
	if err != nil {
		return err
	}
	ix, err := (&playback.IndexBuilder{Session: session, Info: info}).Build(ctx)
	if err != nil {
		return err
	}
	if len(ix.Tracks) == 0 {
		return fmt.Errorf("в раздаче не найдено ни одного трека")
	}
	if trackNo > len(ix.Tracks) {
		return fmt.Errorf("трек №%d вне диапазона (1..%d)", trackNo, len(ix.Tracks))
	}
	track := ix.Tracks[trackNo-1]
	estBytes, _, _ := mediaindex.EstimateTrack(info, track)

	playCtx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.cancel = cancel
	s.play = PlayStatus{
		TopicID:    topicID,
		Track:      trackNo,
		Title:      track.Title,
		State:      "preparing",
		TotalBytes: estBytes,
	}
	s.mu.Unlock()

	player := playback.New(session)
	s.mu.Lock()
	s.player = player
	s.mu.Unlock()

	go func() {
		err := s.playTrack(playCtx, player, session, track)
		s.mu.Lock()
		switch {
		case err != nil:
			s.play.State = "error"
			s.play.Error = err.Error()
		case playCtx.Err() != nil:
			s.play.State = "idle"
		default:
			s.play.State = "done"
		}
		cancel()
		s.mu.Unlock()
	}()
	return nil
}

// playTrack декодирует трек в звуковую карту.
func (s *Server) playTrack(ctx context.Context, player *playback.Player, session *torrent.Session, track mediaindex.TrackPlan) error {
	// Формат аудио известен после чтения заголовка; для карты нужен заранее.
	format := audio.Format{SampleRate: 44100, Channels: 2}
	if track.SampleRate > 0 {
		format.SampleRate = track.SampleRate
	}
	if track.BitDepth > 0 {
		format.BitsPerSample = track.BitDepth
	}
	w, err := audio.NewDeviceWriter(format)
	if err != nil {
		// Карты нет — пишем в WAV, чтобы пользователь хотя бы получил файл.
		wav := filepath.Join(s.opts.DataDir, "preview.wav")
		f, ferr := os.Create(wav)
		if ferr != nil {
			return fmt.Errorf("звуковая карта недоступна: %w", err)
		}
		defer f.Close()
		_, err = player.PlayTrackTo(ctx, playback.Track{
			FileIdx:      track.FileIdx,
			Path:         track.Path,
			Codec:        track.Codec,
			Title:        track.Title,
			SkipSamples:  track.SkipSamples,
			LimitSamples: track.LimitSamples,
		}, f)
		return err
	}
	defer w.Close()

	counter := &progressWriter{w: w, srv: s, session: session, started: time.Now()}
	_, err = player.PlayTrackTo(ctx, playback.Track{
		FileIdx:      track.FileIdx,
		Path:         track.Path,
		Codec:        track.Codec,
		Title:        track.Title,
		SkipSamples:  track.SkipSamples,
		LimitSamples: track.LimitSamples,
	}, counter)
	return err
}

// progressWriter считает объём выданного PCM и обновляет статус.
type progressWriter struct {
	w       io.Writer
	srv     *Server
	session *torrent.Session
	started time.Time
	bytes   int64
}

func (p *progressWriter) Write(b []byte) (int, error) {
	n, err := p.w.Write(b)
	p.bytes += int64(n)
	p.srv.mu.Lock()
	if p.srv.play.State == "preparing" {
		p.srv.play.State = "playing"
	}
	p.srv.play.Samples = int(p.bytes / 4)
	p.srv.play.Elapsed = time.Since(p.started).Seconds()
	if p.session != nil {
		p.srv.play.Download = p.session.Stats().BytesDownloaded
	}
	p.srv.mu.Unlock()
	return n, err
}

// StopPlayback останавливает текущее воспроизведение.
func (s *Server) StopPlayback() {
	s.mu.Lock()
	cancel := s.cancel
	s.cancel = nil
	s.play = PlayStatus{State: "idle"}
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// ---------------------------------------------------------------------------
// Загрузки
// ---------------------------------------------------------------------------

// enqueueDownload ставит задание на загрузку в очередь.
func (s *Server) enqueueDownload(ctx context.Context, topicID int64, what string) error {
	if what == "" {
		what = "1"
	}
	mi, info, err := s.fetchTorrent(ctx, topicID)
	if err != nil {
		return err
	}
	session, err := s.openSession(ctx, mi)
	if err != nil {
		return err
	}
	ix, err := (&playback.IndexBuilder{Session: session, Info: info}).Build(ctx)
	if err != nil {
		return err
	}
	if len(ix.Tracks) == 0 {
		return fmt.Errorf("в раздаче не найдено ни одного трека")
	}
	var fileIdxs []int
	label := ""
	kind := download.KindTrack
	switch what {
	case "album":
		album := ix.Tracks[0].Album
		fileIdxs = download.AlbumFiles(info, album, allFileIndexes(info))
		label = "альбом " + album
		kind = download.KindAlbum
	case "discography", "all":
		fileIdxs = allFileIndexes(info)
		label = "дискография"
		kind = download.KindDiscography
	default:
		n, err := strconv.Atoi(what)
		if err != nil {
			return fmt.Errorf("неверный параметр: %q", what)
		}
		if n < 1 || n > len(ix.Tracks) {
			return fmt.Errorf("трек №%d вне диапазона (1..%d)", n, len(ix.Tracks))
		}
		track := ix.Tracks[n-1]
		fileIdxs = []int{track.FileIdx}
		label = fmt.Sprintf("трек №%d (%s)", n, track.Title)
	}

	if s.mgr == nil {
		fetcher := &download.TorrentFetcher{
			Engine: s.opts.Engine,
			MetaInfo: func(ctx context.Context, task *download.Task) (*metainfo.MetaInfo, error) {
				return mi, nil
			},
		}
		s.mgr = download.NewManager(fetcher, s.log)
		go func() {
			_ = s.mgr.Run(context.Background())
		}()
	}
	hash, _ := torrent.InfoHashOf(mi)
	s.mgr.Add(download.Task{
		Kind:     kind,
		TopicID:  topicID,
		Title:    label,
		InfoHash: hash,
		FileIdxs: fileIdxs,
		Priority: 10,
	})
	return nil
}

// ---------------------------------------------------------------------------
// Служебное
// ---------------------------------------------------------------------------

// fetchTorrent скачивает .torrent по id темы.
func (s *Server) fetchTorrent(ctx context.Context, topicID int64) (*metainfo.MetaInfo, *metainfo.Info, error) {
	if s.opts.FetchTorrent != nil {
		mi, err := s.opts.FetchTorrent(ctx, topicID)
		if err != nil {
			return nil, nil, err
		}
		info, err := mi.UnmarshalInfo()
		if err != nil {
			return nil, nil, err
		}
		return mi, &info, nil
	}
	if s.opts.Client == nil {
		return nil, nil, fmt.Errorf("нет подключения к сайту")
	}
	data, err := s.opts.Client.DownloadTorrent(ctx, topicID)
	if err != nil {
		return nil, nil, err
	}
	mi, err := metainfo.Load(bytes.NewReader(data))
	if err != nil {
		return nil, nil, fmt.Errorf("разбор .torrent: %w", err)
	}
	info, err := mi.UnmarshalInfo()
	if err != nil {
		return nil, nil, err
	}
	return mi, &info, nil
}

// openSession открывает (или переиспользует) сессию для раздачи.
func (s *Server) openSession(ctx context.Context, mi *metainfo.MetaInfo) (*torrent.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.session != nil {
		return s.session, nil
	}
	session, err := s.opts.Engine.Start(ctx, mi, torrent.StartOptions{Trackers: trackersOf(mi)})
	if err != nil {
		return nil, err
	}
	s.session = session
	s.player = playback.New(session)
	return session, nil
}

// trackersOf возвращает список трекеров из .torrent.
func trackersOf(mi *metainfo.MetaInfo) [][]string {
	if mi == nil {
		return nil
	}
	var out [][]string
	for _, tier := range mi.AnnounceList {
		if len(tier) > 0 {
			out = append(out, tier)
		}
	}
	if len(out) == 0 && mi.Announce != "" {
		out = append(out, []string{mi.Announce})
	}
	return out
}

// allFileIndexes возвращает индексы всех файлов раздачи.
func allFileIndexes(info *metainfo.Info) []int {
	files := mediaindex.FilesInfo(info)
	out := make([]int, 0, len(files))
	for i := range files {
		out = append(out, i)
	}
	return out
}
