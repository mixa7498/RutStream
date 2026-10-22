// Package torrent — обёртка над BitTorrent-движком с ключевой возможностью:
// скачивать только нужные piece'ы раздачи, а не её целиком.
package torrent

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	bt "github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/dialer"
	g "github.com/anacrolix/generics"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
	"github.com/mixa7/rutstream/internal/mediaindex"
	"golang.org/x/net/proxy"
	"golang.org/x/time/rate"
)

// Options — настройки движка.
type Options struct {
	// DataDir — каталог для хранения данных. Пустой = временный каталог.
	DataDir string
	// ProxyURL — socks5://host:port или http://host:port. Пусто = напрямую.
	ProxyURL string
	// ListenPort — порт входящих соединений (0 = случайный).
	ListenPort int
	// ListenHost — адрес прослушивания ("" = все интерфейсы, "127.0.0.1" = только локально).
	ListenHost string
	// DisableDHT — для приватных трекеров DHT не нужен.
	DisableDHT bool
	// DisablePEX — для приватных трекеров PEX не нужен.
	DisablePEX bool
	// DisableWebseeds — rutracker не использует webseed.
	DisableWebseeds bool
	// Seed — разрешить раздачу (важно: без этого клиент отклоняет входящие
	// соединения, если у него нет недостающих данных).
	Seed bool
	// MaxConnsPerTorrent — лимит одновременных пиров.
	MaxConnsPerTorrent int
	// DownloadLimit — лимит скорости скачивания, байт/с (0 = без лимита).
	DownloadLimit int64
	// UploadLimit — лимит скорости раздачи, байт/с (0 = без лимита).
	UploadLimit int64
	// Logger.
	Logger *slog.Logger
	// StorageMode — "disk" (по умолчанию) или "ram".
	//
	// "ram" — piece'ы хранятся в памяти: при потоковом воспроизведении на SSD
	// не пишется ни байта. Ограничено MemLimit.
	StorageMode string
	// MemLimit — лимит памяти для режима "ram" (байт; 0 — без лимита).
	MemLimit int64
	// ReadaheadBytes — окно опережающего чтения (в байтах; 0 — значение по
	// умолчанию движка).
	//
	// Клиент качает куски чуть впереди текущей позиции чтения, чтобы
	// воспроизведение шло без запинок. Слишком большое окно — лишний трафик:
	// для трека внутри альбома скачался бы остаток альбома.
	ReadaheadBytes int64
}

// DefaultReadahead — окно опережающего чтения по умолчанию (256 КБ).
//
// Этого хватает на пару piece'ов вперёд (при piece 128 КБ) — воспроизведение
// идёт ровно, а лишний трафик не растёт.
const DefaultReadahead = 256 * 1024

// Engine — BitTorrent-клиент.
type Engine struct {
	client *bt.Client
	opts   Options
	log    *slog.Logger
	// mem — хранилище в памяти (если StorageMode == "ram").
	mem *MemoryStorage
}

// New создаёт движок.
func New(opts Options) (*Engine, error) {
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	}
	if opts.ReadaheadBytes <= 0 {
		opts.ReadaheadBytes = DefaultReadahead
	}
	cfg := bt.NewDefaultClientConfig()
	cfg.DataDir = opts.DataDir
	cfg.DisableTCP = false
	cfg.DisableUTP = true // µTP приватному трекеру не нужен
	cfg.NoDHT = opts.DisableDHT
	cfg.DisablePEX = opts.DisablePEX
	cfg.DisableWebseeds = opts.DisableWebseeds
	cfg.Seed = opts.Seed
	cfg.ListenPort = opts.ListenPort
	if opts.ListenHost != "" {
		host := opts.ListenHost
		cfg.ListenHost = func(network string) string {
			if strings.HasPrefix(network, "tcp6") {
				return "::1"
			}
			return host
		}
	}
	cfg.Slogger = opts.Logger
	cfg.EstablishedConnsPerTorrent = 30
	if opts.MaxConnsPerTorrent > 0 {
		cfg.EstablishedConnsPerTorrent = opts.MaxConnsPerTorrent
	}
	if opts.DownloadLimit > 0 {
		cfg.DownloadRateLimiter = rate.NewLimiter(rate.Limit(opts.DownloadLimit), int(opts.DownloadLimit))
	}
	if opts.UploadLimit > 0 {
		cfg.UploadRateLimiter = rate.NewLimiter(rate.Limit(opts.UploadLimit), int(opts.UploadLimit))
	}

	var proxyDialer dialer.T
	if opts.ProxyURL != "" {
		d, err := newProxyDialer(opts.ProxyURL)
		if err != nil {
			return nil, fmt.Errorf("прокси %q: %w", opts.ProxyURL, err)
		}
		proxyDialer = d
		pu, err := url.Parse(opts.ProxyURL)
		if err == nil {
			cfg.HTTPProxy = http.ProxyURL(pu)
		}
		if cd, ok := d.(dialer.WithNetwork); ok {
			cfg.HTTPDialContext = cd.Dialer.DialContext
			cfg.TrackerDialContext = cd.Dialer.DialContext
		}
	}

	// Хранилище.
	var mem *MemoryStorage
	switch opts.StorageMode {
	case "ram", "RAM", "memory":
		// Данные только в памяти: 0 байт на SSD при стриминге.
		mem = NewMemoryStorage(opts.MemLimit)
		cfg.DefaultStorage = mem
	default:
		// Пишем сразу в финальные файлы, без промежуточных «.part».
		//
		// Почему: при потоковом чтении мы держим файл открытым, а Windows не
		// позволяет переименовать открытый файл — anacrolix не смог бы перевести
		// «X.flac.part» в «X.flac» и получал «Access is denied».
		if opts.DataDir != "" {
			fs := storage.NewFileOpts(storage.NewFileClientOpts{
				ClientBaseDir: opts.DataDir,
				UsePartFiles:  g.Option[bool]{Value: false, Ok: true},
			})
			cfg.DefaultStorage = fs
		}
	}

	cl, err := bt.NewClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("создание BT-клиента: %w", err)
	}
	if proxyDialer != nil {
		cl.AddDialer(proxyDialer)
	}
	return &Engine{client: cl, opts: opts, log: opts.Logger, mem: mem}, nil
}

// Close останавливает движок.
func (e *Engine) Close() error {
	if e.client == nil {
		return nil
	}
	errs := e.client.Close()
	if len(errs) == 0 {
		return nil
	}
	msgs := make([]string, 0, len(errs))
	for _, err := range errs {
		if err != nil {
			msgs = append(msgs, err.Error())
		}
	}
	if len(msgs) == 0 {
		return nil
	}
	return errors.New("закрытие клиента: " + strings.Join(msgs, "; "))
}

// Client возвращает низкоуровневый клиент (для расширенных сценариев).
func (e *Engine) Client() *bt.Client { return e.client }

// Session — активная сессия работы с одной раздачей.
type Session struct {
	t    *bt.Torrent
	mi   *metainfo.MetaInfo
	info *metainfo.Info
	eng  *Engine

	mu     sync.Mutex
	wanted map[int]bool
	// readahead — размер окна опережающего чтения (в байтах).
	readahead int64
}

// StartOptions — параметры запуска сессии.
type StartOptions struct {
	// Trackers — список announce-URL (tiered).
	Trackers [][]string
	// AnnounceURL — одиночный announce (удобно для теста).
	AnnounceURL string
	// Webseeds.
	Webseeds []string
	// PeerAddrs — прямые адреса пиров (для локальных тестов без трекера).
	PeerAddrs []string
	// DisallowUpload — запретить отдачу (тест/разведка).
	DisallowUpload bool
	// DisallowDownload — запретить скачивание (тест/разведка).
	DisallowDownload bool
}

// Start регистрирует раздачу в клиенте и начинает работу с ней.
func (e *Engine) Start(ctx context.Context, mi *metainfo.MetaInfo, opts StartOptions) (*Session, error) {
	if mi == nil {
		return nil, errors.New("metainfo пуст")
	}
	info, err := mi.UnmarshalInfo()
	if err != nil {
		return nil, fmt.Errorf("разбор info: %w", err)
	}
	infoBytes, err := bencodeInfoBytes(mi)
	if err != nil {
		return nil, err
	}
	trackers := opts.Trackers
	if len(trackers) == 0 && opts.AnnounceURL != "" {
		trackers = [][]string{{opts.AnnounceURL}}
	}
	spec := bt.TorrentSpecFromMetaInfo(mi)
	if len(trackers) > 0 {
		spec.Trackers = trackers
	}
	if len(opts.Webseeds) > 0 {
		spec.Webseeds = opts.Webseeds
	}
	spec.PeerAddrs = opts.PeerAddrs
	spec.AddTorrentOpts.DisallowDataUpload = opts.DisallowUpload
	spec.AddTorrentOpts.DisallowDataDownload = opts.DisallowDownload
	_ = infoBytes
	t, _, err := e.client.AddTorrentSpec(spec)
	if err != nil {
		return nil, fmt.Errorf("добавление раздачи: %w", err)
	}
	return &Session{
		t:         t,
		mi:        mi,
		info:      &info,
		eng:       e,
		wanted:    map[int]bool{},
		readahead: e.opts.ReadaheadBytes,
	}, nil
}

// InfoHash возвращает hex-хеш раздачи.
func (s *Session) InfoHash() string { return s.t.InfoHash().HexString() }

// Torrent возвращает низкоуровневый объект раздачи.
func (s *Session) Torrent() *bt.Torrent { return s.t }

// MetaInfo возвращает metainfo раздачи.
func (s *Session) MetaInfo() *metainfo.MetaInfo { return s.mi }

// Info возвращает разобранный info-словарь раздачи.
func (s *Session) Info() *metainfo.Info { return s.info }

// Files возвращает список файлов раздачи.
func (s *Session) Files() []*bt.File { return s.t.Files() }

// SetWantedFiles оставляет «нужными» только перечисленные файлы:
// у остальных приоритет снимается, поэтому их piece'ы не качаются.
func (s *Session) SetWantedFiles(idxs []int) error {
	want := make(map[int]bool, len(idxs))
	for _, i := range idxs {
		want[i] = true
	}
	files := s.t.Files()
	for i := range files {
		if want[i] {
			files[i].SetPriority(bt.PiecePriorityNormal)
		} else {
			files[i].SetPriority(bt.PiecePriorityNone)
		}
	}

	return nil
}

// SetWantedPieces задаёт белый список piece'ов.
// Всё, что вне диапазона, перестаёт запрашиваться у пиров.
func (s *Session) SetWantedPieces(pr mediaindex.PieceRange) error {
	num := s.t.NumPieces()
	if pr.End > num {
		pr.End = num
	}
	s.t.CancelPieces(0, num)
	if pr.Len() > 0 {
		s.t.DownloadPieces(pr.Start, pr.End)
	}
	s.mu.Lock()
	s.wanted = map[int]bool{}
	for i := pr.Start; i < pr.End; i++ {
		s.wanted[i] = true
	}
	s.mu.Unlock()
	return nil
}

// ClearWantedPieces снимает все piece-приоритеты (файловые остаются).
func (s *Session) ClearWantedPieces() {
	s.t.CancelPieces(0, s.t.NumPieces())
	s.mu.Lock()
	s.wanted = map[int]bool{}
	s.mu.Unlock()
}

// BytesRead возвращает, сколько байт прочитано из потока файла раздачи.
//
// Диагностика: показывает реальный объём данных, которые пришлось прочитать
// (и, значит, скачать) для воспроизведения трека.
func BytesRead(r io.Reader) int64 {
	if l, ok := r.(*limitedFileReader); ok {
		return l.read
	}
	return 0
}

// FilesProgress возвращает, сколько байт уже скачано по указанным файлам и
// сколько всего нужно (с учётом границ piece'ов).
//
// Используется для прогресса загрузки: сетевой счётчик показывает трафик,
// а этот — реальную готовность файлов на диске.
func (s *Session) FilesProgress(idxs []int) (done, total int64) {
	files := s.t.Files()
	for _, i := range idxs {
		if i < 0 || i >= len(files) {
			continue
		}
		total += files[i].Length()
		done += files[i].BytesCompleted()
	}
	return done, total
}

// CleanupIncompleteFiles удаляет с диска файлы раздачи, которые заполнены
// не полностью.
//
// Зачем: anacrolix при записи piece'а, пересекающего границу файлов,
// создаёт ВСЕ затронутые файлы целиком (полного размера), но заполняет
// только свою часть. В итоге рядом с нужным треком остаются «дырявые»
// соседи: файл правильного размера, но с дырами вместо данных.
//
// Вызывать нужно в начале работы с раздачей: пока файл не открыт
// anacrolix'ом, его можно удалить (Windows не даёт удалять открытый файл).
func (s *Session) CleanupIncompleteFiles() []string {
	root := s.eng.opts.DataDir
	if root == "" {
		return nil
	}
	var removed []string
	for _, f := range s.t.Files() {
		if f.BytesCompleted() >= f.Length() {
			continue
		}
		path := filepath.Join(root, filepath.FromSlash(f.Path()))
		info, err := os.Stat(path)
		if err != nil || info.Size() == 0 {
			continue
		}
		if err := os.Remove(path); err != nil {
			continue
		}
		removed = append(removed, f.Path())
	}
	return removed
}

// ListFiles возвращает множество имён файлов раздачи, существующих на диске.
//
// Ключ — путь файла относительно каталога данных (как его показывает
// anacrolix).
func (s *Session) ListFiles() map[string]bool {
	out := map[string]bool{}
	root := s.eng.opts.DataDir
	if root == "" {
		return out
	}
	for _, f := range s.t.Files() {
		path := filepath.Join(root, filepath.FromSlash(f.Path()))
		if info, err := os.Stat(path); err == nil && info.Size() > 0 {
			out[f.Path()] = true
		}
	}
	return out
}

// NewFilesSince возвращает пути файлов, которые появились на диске после
// снимка before и не входят в keep.
//
// Нужны для уборки «дырявых» соседей: anacrolix создаёт их при записи
// граничных piece'ов (файл полного размера, но заполнен частично).
// Удалять нужно после закрытия клиента — пока файл открыт, Windows его
// не удалит.
func (s *Session) NewFilesSince(before map[string]bool, keep map[int]bool) []string {
	root := s.eng.opts.DataDir
	if root == "" {
		return nil
	}
	files := s.t.Files()
	var out []string
	for i, f := range files {
		if keep[i] {
			continue
		}
		name := f.Path()
		if before[name] {
			// Файл был и до загрузки — не трогаем.
			continue
		}
		path := filepath.Join(root, filepath.FromSlash(name))
		st, err := os.Stat(path)
		if err != nil || st.Size() == 0 {
			continue
		}

		out = append(out, path)
	}
	return out
}

// RemoveFiles удаляет указанные файлы (игнорируя ошибки).
//
// Файлы могут быть ещё открыты anacrolix'ом (Windows не даёт удалить
// открытый файл) — поэтому делаем несколько попыток с паузой: обычно
// дескрипторы освобождаются сразу после закрытия клиента.
func (s *Session) RemoveFiles(paths []string) int {
	removed := 0
	for _, p := range paths {
		for attempt := 0; attempt < 10; attempt++ {
			err := os.Remove(p)
			if err == nil {
				removed++
				break
			}
			if os.IsNotExist(err) {
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
	}
	return removed
}

// OpenFile открывает поток чтения файла раздачи.
// Чтение «подтягивает» нужные piece'ы автоматически (readahead).
//
// Важно: поток ограничен логической длиной файла. Хранилище anacrolix
// выравнивает файлы по границе chunk'а (16 КБ), и без этого ограничения
// читатель получал бы «хвост» из мусора за пределами файла — парсеры
// аудиоformats спотыкались бы на нём («invalid sync-code» вместо EOF).
func (s *Session) OpenFile(fileIdx int) (io.ReadSeekCloser, error) {
	files := s.t.Files()
	if fileIdx < 0 || fileIdx >= len(files) {
		return nil, fmt.Errorf("индекс файла %d вне диапазона (%d)", fileIdx, len(files))
	}
	f := files[fileIdx]
	r := f.NewReader()
	// Фиксированное окно опережающего чтения.
	//
	// По умолчанию anacrolix считает readahead как расстояние от начала
	// непрерывного чтения: чем дальше мы прочитали, тем шире окно приоритетов
	// и тем больше кусов клиент качает «про запас» (для трека внутри альбома
	// это означало скачивание всего файла). Нам нужен небольшой фиксированный
	// запас — пара piece'ов вперёд для ровного воспроизведения.
	if rr, ok := r.(interface{ SetReadahead(int64) }); ok {
		rr.SetReadahead(s.readahead)
	}
	return &limitedFileReader{r: r, length: f.Length()}, nil
}

// limitedFileReader — файл раздачи с ограничением по логической длине.
type limitedFileReader struct {
	r      io.ReadSeeker
	length int64
	pos    int64
	// read — сколько байт прочитано (диагностика объёма загрузки).
	read int64
}

// Read читает не больше оставшегося объёма файла.
func (l *limitedFileReader) Read(p []byte) (int, error) {
	if l.pos >= l.length {
		return 0, io.EOF
	}
	if int64(len(p)) > l.length-l.pos {
		p = p[:l.length-l.pos]
	}
	n, err := l.r.Read(p)
	l.pos += int64(n)
	l.read += int64(n)
	if err == nil && l.pos >= l.length {
		err = io.EOF
	}
	return n, err
}

// Seek перематывает поток (в пределах файла).
func (l *limitedFileReader) Seek(offset int64, whence int) (int64, error) {
	var abs int64
	switch whence {
	case io.SeekStart:
		abs = offset
	case io.SeekCurrent:
		abs = l.pos + offset
	case io.SeekEnd:
		abs = l.length + offset
	default:
		return 0, fmt.Errorf("неверный whence: %d", whence)
	}
	if abs < 0 {
		return 0, fmt.Errorf("отрицательная позиция: %d", abs)
	}
	if abs > l.length {
		abs = l.length
	}
	if _, err := l.r.Seek(abs, io.SeekStart); err != nil {
		return 0, err
	}
	l.pos = abs
	return abs, nil
}

// Close закрывает поток.
func (l *limitedFileReader) Close() error {
	if c, ok := l.r.(io.Closer); ok {
		return c.Close()
	}
	return nil
}

// ReadAt читает данные по произвольному смещению (совместимость с io.ReaderAt).
//
// Нужен sniffer'ам: они читают заголовок контейнера по смещению, не трогая
// позицию потока. Чтение по смещению поднимает приоритет нужного piece'а —
// то есть sniffer сам инициирует докачку заголовка.
func (l *limitedFileReader) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, fmt.Errorf("negative offset %d", off)
	}
	if off >= l.length {
		return 0, io.EOF
	}
	if int64(len(p)) > l.length-off {
		p = p[:l.length-off]
	}
	// Сохраняем позицию: ReadAt не должен её менять.
	saved := l.pos
	if _, err := l.Seek(off, io.SeekStart); err != nil {
		return 0, err
	}
	n, err := l.r.Read(p)
	l.pos = saved
	if err == nil && n < len(p) {
		err = io.ErrUnexpectedEOF
	}
	return n, err
}

// WaitPiece дожидается готовности piece'а.
func (s *Session) WaitPiece(ctx context.Context, idx int) error {
	for {
		if s.PieceHas(idx) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// PieceHas сообщает, есть ли piece.
func (s *Session) PieceHas(idx int) bool {
	ps := s.t.PieceState(idx)
	return ps.Ok && ps.Complete
}

// Stats — счётчики сессии.
type Stats struct {
	BytesDownloaded int64
	BytesUploaded   int64
	PiecesComplete  int
	PiecesTotal     int
	ActivePeers     int
	TotalPeers      int
	Complete        bool
}

// Stats возвращает текущие счётчики.
func (s *Session) Stats() Stats {
	st := s.t.Stats()
	return Stats{
		BytesDownloaded: st.BytesReadData.Int64(),
		BytesUploaded:   st.BytesWrittenData.Int64(),
		PiecesComplete:  st.PiecesComplete,
		PiecesTotal:     s.t.NumPieces(),
		ActivePeers:     st.ActivePeers,
		TotalPeers:      st.TotalPeers,
		Complete:        s.t.Complete().Bool(),
	}
}

// AddClientPeer добавляет адреса другого клиента как доверенных пиров.
// Используется в локальных тестах, где трекера нет.
func (s *Session) AddClientPeer(cl *bt.Client) int {
	return s.t.AddClientPeer(cl)
}

// Close удаляет раздачу из клиента.
func (s *Session) Close() error {
	s.t.Drop()
	return nil
}

// ReleasePieces освобождает память (в режиме RAM) под указанные piece'ы.
//
// Вызывается после проигрывания трека, чтобы не держать в RAM всю раздачу.
func (s *Session) ReleasePieces(indices []int) {
	if s.eng == nil || s.eng.mem == nil || len(indices) == 0 {
		return
	}
	s.eng.mem.Release(s.t.InfoHash(), indices)
}

// MemoryUsed возвращает объём данных, удерживаемых в памяти (режим RAM).
func (e *Engine) MemoryUsed() int64 {
	if e.mem == nil {
		return 0
	}
	return e.mem.Used()
}

// InfoHashOf вычисляет info_hash раздачи.
func InfoHashOf(mi *metainfo.MetaInfo) (string, error) {
	if mi == nil {
		return "", errors.New("metainfo пуст")
	}
	if len(mi.InfoBytes) == 0 {
		b, err := bencodeInfoBytes(mi)
		if err != nil {
			return "", err
		}
		mi.InfoBytes = b
	}
	return mi.HashInfoBytes().HexString(), nil
}

// bencodeInfoBytes возвращает bencode-представление info-дик словаря.
func bencodeInfoBytes(mi *metainfo.MetaInfo) ([]byte, error) {
	if len(mi.InfoBytes) > 0 {
		return mi.InfoBytes, nil
	}
	info, err := mi.UnmarshalInfo()
	if err != nil {
		return nil, err
	}
	return bencode.Marshal(info)
}

// newProxyDialer создаёт dialer через SOCKS5/HTTP-прокси.
func newProxyDialer(raw string) (dialer.T, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	switch u.Scheme {
	case "socks5", "socks5h":
		var auth *proxy.Auth
		if u.User != nil {
			pass, _ := u.User.Password()
			auth = &proxy.Auth{User: u.User.Username(), Password: pass}
		}
		d, err := proxy.SOCKS5("tcp", u.Host, auth, proxy.Direct)
		if err != nil {
			return nil, err
		}
		cd, ok := d.(proxy.ContextDialer)
		if !ok {
			return nil, errors.New("SOCKS5-диалер не поддерживает контекст")
		}
		return dialer.WithNetwork{Network: "tcp", Dialer: cd}, nil
	case "http", "https":
		return dialer.WithNetwork{Network: "tcp", Dialer: &httpConnectDialer{proxyURL: u}}, nil
	}
	return nil, fmt.Errorf("неподдерживаемая схема прокси: %s", u.Scheme)
}

// httpConnectDialer реализует HTTP CONNECT через прокси-сервер.
type httpConnectDialer struct {
	proxyURL *url.URL
}

func (h *httpConnectDialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	d := &net.Dialer{}
	conn, err := d.DialContext(ctx, "tcp", h.proxyURL.Host)
	if err != nil {
		return nil, err
	}
	req := &http.Request{
		Method: http.MethodConnect,
		URL:    &url.URL{Opaque: addr},
		Host:   addr,
		Header: make(http.Header),
	}
	if h.proxyURL.User != nil {
		pass, _ := h.proxyURL.User.Password()
		req.Header.Set("Proxy-Authorization", "Basic "+basicAuth(h.proxyURL.User.Username(), pass))
	}
	if err := req.Write(conn); err != nil {
		conn.Close()
		return nil, err
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, req)
	if err != nil {
		conn.Close()
		return nil, err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		conn.Close()
		return nil, fmt.Errorf("прокси CONNECT вернул %s", resp.Status)
	}
	return conn, nil
}

func basicAuth(user, pass string) string {
	return base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))
}
