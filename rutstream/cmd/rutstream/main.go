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
	"errors"
	"fmt"
	"io"
	"log/slog"
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

// version — версия сборки. Переопределяется при сборке:
//
//	go build -ldflags "-X main.version=0.2.0"
var version = "0.2.0-dev"

func main() {
	// Сборка идёт как GUI-приложение (-H windowsgui), поэтому при запуске
	// из cmd/PowerShell подключаемся к родительской консоли, чтобы работали
	// команды check/search и их вывод.
	attachConsole()
	setupLogging()

	ctx := context.Background()
	var err error
	// Без аргументов (двойной щелчок по exe) сразу открываем окно.
	if len(os.Args) < 2 {
		err = cmdGUI(ctx, nil)
		if err != nil {
			os.Exit(1)
		}
		return
	}
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
	case "gui":
		err = cmdGUI(ctx, os.Args[2:])
	case "sign":
		err = cmdSign(ctx, os.Args[2:])
	case "raw":
		err = cmdRaw(ctx, os.Args[2:])
	case "history":
		err = cmdHistory(ctx)
	case "login":
		err = cmdLogin(ctx)
	case "check":
		err = cmdCheck(ctx)
	case "session":
		err = cmdSession(ctx)
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
  rutstream gui                         приложение с окном (разделы, поиск, плеер)
  rutstream (без аргументов)            то же, что gui: открыть окно
  rutstream sign [файл]                 подписать бинарник (сертификат создаётся сам)

Переменные окружения:
  RUTSTREAM_USER / RUTSTREAM_PASS  логин и пароль rutracker
  RUTSTREAM_COOKIE                готовая cookie bb_session (или вся строка document.cookie)
  RUTSTREAM_PROXY                 socks5://host:port для сайта и пиров
  RUTSTREAM_NO_BROWSER            не запускать скрытый браузер (часть страниц недоступна)
  RUTSTREAM_BROWSER_VISIBLE=1     показать окно браузера (для отладки)
  RUTSTREAM_CDP_PORT              порт отладки скрытого браузера (по умолчанию 9222)
  RUTSTREAM_DATA                  каталог данных (по умолчанию %APPDATA%/RutStream)`)
}

// setupLogging пишет журнал в каталог данных: у GUI-приложения нет консоли,
// и без файла причину ошибки было бы не видно.
func setupLogging() {
	base := os.Getenv("RUTSTREAM_DATA")
	if base == "" {
		if dir, err := os.UserConfigDir(); err == nil {
			base = filepath.Join(dir, "RutStream")
		} else {
			base = os.TempDir()
		}
	}
	dir := filepath.Join(base, "logs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, "rutstream.log"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	// Файл не закрываем: он живёт столько же, сколько процесс.
	var out io.Writer = f
	if os.Getenv("RUTSTREAM_DEBUG") != "" {
		out = io.MultiWriter(f, os.Stderr)
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(out, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))
	slog.Info("запуск", "version", version, "pid", os.Getpid())
}

// ---------------------------------------------------------------- каркас --

type app struct {
	client  *rutracker.Client
	store   *catalog.Store
	engine  *torrent.Engine
	dataDir string

	// browserMu защищает ленивый запуск скрытого браузера.
	browserMu sync.Mutex
	cdp       *rutracker.CDP
	// browserProc — процесс браузера, если его запустили мы (его и закрываем).
	browserProc *os.Process
	loggedIn    bool
}

func newApp() (*app, error) {
	return newAppWithStorage("")
}

// newAppWithStorage создаёт приложение с принудительным режимом хранения
// ("disk" или "ram"); пустая строка — режим из переменной окружения.
func newAppWithStorage(storage string) (*app, error) {
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
	// appRef нужен, чтобы клиент мог лениво поднять скрытый браузер:
	// замыкание ссылается на приложение, которое создаётся ниже.
	var appRef *app
	opts := rutracker.Options{
		ProxyURL:  os.Getenv("RUTSTREAM_PROXY"),
		Timeout:   30 * time.Second,
		UserAgent: rutracker.BrowserUserAgent(),
		EnsureTransport: func(ctx context.Context) (rutracker.Transport, error) {
			return appRef.ensureBrowser(ctx)
		},
	}
	c, err := rutracker.New(opts)
	if err != nil {
		return nil, err
	}
	store, err := catalog.Open(filepath.Join(dataDir, "catalog.db"))
	if err != nil {
		return nil, err
	}
	// Режим хранения: disk (по умолчанию) или ram (0 байт на SSD при стриминге).
	storageMode := strings.ToLower(os.Getenv("RUTSTREAM_STORAGE"))
	if storageMode == "" {
		storageMode = "disk"
	}
	if storage != "" {
		storageMode = storage
	}
	var memLimit int64 = 512 << 20
	if v := os.Getenv("RUTSTREAM_MEM_LIMIT_MB"); v != "" {
		if mb, err := strconv.ParseInt(v, 10, 64); err == nil {
			memLimit = mb << 20
		}
	}
	engine, err := torrent.New(torrent.Options{
		DataDir:         filepath.Join(dataDir, "data"),
		ProxyURL:        os.Getenv("RUTSTREAM_PROXY"),
		DisableDHT:      true,
		DisablePEX:      true,
		DisableWebseeds: true,
		Seed:            true,
		StorageMode:     storageMode,
		MemLimit:        memLimit,
		Logger:          slog.Default(),
	})
	if err != nil {
		store.Close()
		return nil, err
	}
	a := &app{client: c, store: store, engine: engine, dataDir: dataDir}
	appRef = a
	a.applySession()
	return a, nil
}

// ensureBrowser поднимает скрытый браузер при первом Cloudflare-challenge.
//
// Браузер работает без окна (headless) и с обычным User-Agent, поэтому
// пользователь не видит ни окна, ни значка в панели задач. Пока сайт
// отвечает напрямую, браузер вообще не запускается.
func (a *app) ensureBrowser(ctx context.Context) (rutracker.Transport, error) {
	a.browserMu.Lock()
	defer a.browserMu.Unlock()

	if a.cdp != nil {
		return rutracker.NewCDPTransport(a.cdp), nil
	}
	if os.Getenv("RUTSTREAM_NO_BROWSER") != "" {
		return nil, fmt.Errorf("запуск браузера отключён (RUTSTREAM_NO_BROWSER)")
	}
	port := rutracker.DefaultCDPPort
	if v := os.Getenv("RUTSTREAM_CDP_PORT"); v != "" {
		port = rutracker.PortFromArg(v)
	}

	var proc *os.Process
	if !rutracker.CDPAvailable(port) {
		profile := filepath.Join(a.dataDir, "browser")
		p, err := rutracker.OpenBrowserWithProfile(port, profile)
		if err != nil {
			return nil, err
		}
		proc = p
		// Ждём, пока поднимется отладочный порт. Обычно это 1–3 секунды.
		for i := 0; i < 80; i++ {
			if rutracker.CDPAvailable(port) {
				break
			}
			select {
			case <-ctx.Done():
				_ = proc.Kill()
				return nil, ctx.Err()
			case <-time.After(250 * time.Millisecond):
			}
		}
	}
	if !rutracker.CDPAvailable(port) {
		if proc != nil {
			_ = proc.Kill()
		}
		return nil, fmt.Errorf("скрытый браузер не запустился (порт %d)", port)
	}

	cdp, err := rutracker.ConnectCDP(port)
	if err != nil {
		if proc != nil {
			_ = proc.Kill()
		}
		return nil, err
	}
	a.cdp = cdp
	a.browserProc = proc

	// Переносим сессию в браузер: вкладка нужна, чтобы cookie вообще
	// установились, а затем перезагружаем форум — Cloudflare выдаст
	// cf_clearance уже для авторизованной сессии.
	index := a.client.Mirror() + "/forum/index.php"
	_, _ = cdp.Fetch(index, 90*time.Second)
	for _, ck := range a.client.Cookies() {
		if ck.Value == "" {
			continue
		}
		path := ck.Path
		if path == "" {
			path = "/"
		}
		_ = cdp.SetCookie(&rutracker.BrowserCookie{
			Name:     ck.Name,
			Value:    ck.Value,
			Domain:   ".rutracker.org",
			Path:     path,
			HTTPOnly: ck.HttpOnly,
			Secure:   true,
		})
	}
	_, _ = cdp.Fetch(index, 90*time.Second)

	// Забираем из браузера свежие cookie (в первую очередь cf_clearance),
	// чтобы обычный HTTP-клиент тоже проходил проверку.
	if cookies, err := cdp.Cookies(); err == nil {
		a.client.ImportCookiesTo(cookies)
	}
	return rutracker.NewCDPTransport(cdp), nil
}

// browserReady сообщает, запущен ли скрытый браузер.
func (a *app) browserReady() bool {
	a.browserMu.Lock()
	defer a.browserMu.Unlock()
	return a.cdp != nil
}

func (a *app) close() {
	a.browserMu.Lock()
	if a.cdp != nil {
		a.cdp.Close()
		a.cdp = nil
	}
	proc := a.browserProc
	a.browserProc = nil
	a.browserMu.Unlock()
	// Браузер запускали мы — мы же его и закрываем. Если он был запущен
	// пользователем (порт уже слушал), не трогаем чужой процесс.
	if proc != nil {
		_ = proc.Kill()
	}
	a.engine.Close()
	a.store.Close()
}

// applySession переносит учётные данные из окружения или из сохранённого файла.
func (a *app) applySession() {
	// Переменная окружения может содержать как голое значение bb_session,
	// так и всю строку document.cookie (тогда там есть и cf_clearance).
	if text := strings.TrimSpace(os.Getenv("RUTSTREAM_COOKIE")); text != "" {
		a.client.SetCookieSet(rutracker.ExtractCookieSet(text))
		ck := rutracker.ExtractSessionCookie(text)
		if ck == "" {
			ck = text
		}
		a.client.SetSessionCookie(ck)
		a.loggedIn = true
		return
	}
	// Сохранённая с прошлого раза сессия.
	if data, err := os.ReadFile(a.sessionFile()); err == nil && len(data) > 0 {
		text := string(data)
		a.client.SetCookieSet(rutracker.ExtractCookieSet(text))
		if ck := rutracker.ExtractSessionCookie(text); ck != "" {
			a.client.SetSessionCookie(ck)
			a.loggedIn = true
			return
		}
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

// sessionFile — путь к файлу с сохранёнными cookie.
func (a *app) sessionFile() string {
	return filepath.Join(a.dataDir, "session.cookie")
}

// setSessionFromGUI применяет cookie сессии, введённый в окне приложения.
//
// Принимаем как голое значение bb_session, так и всю строку document.cookie:
// во втором случае сохраняется и cf_clearance — с ней обычный HTTP-клиент
// проходит проверку Cloudflare без запуска браузера.
func (a *app) setSessionFromGUI(text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return fmt.Errorf("пустая cookie")
	}
	set := rutracker.ExtractCookieSet(text)
	session := set["bb_session"]
	if session == "" {
		session = rutracker.ExtractSessionCookie(text)
	}
	if session == "" {
		return fmt.Errorf("в строке нет bb_session")
	}
	set["bb_session"] = session
	a.client.SetCookieSet(set)
	a.client.SetSessionCookie(session)
	if err := a.saveSession(text); err != nil {
		return err
	}

	// Если скрытый браузер уже запущен — обновляем cookie и в нём.
	a.browserMu.Lock()
	cdp := a.cdp
	a.browserMu.Unlock()
	if cdp != nil {
		for name, value := range set {
			if value == "" {
				continue
			}
			path := "/"
			if name == "bb_session" {
				path = "/forum/"
			}
			_ = cdp.SetCookie(&rutracker.BrowserCookie{
				Name:     name,
				Value:    value,
				Domain:   ".rutracker.org",
				Path:     path,
				HTTPOnly: name == "bb_session",
				Secure:   true,
			})
		}
	}
	a.loggedIn = true
	return nil
}

// clearSession удаляет сохранённую сессию.
func (a *app) clearSession() {
	_ = os.Remove(a.sessionFile())
	a.client.SetSessionCookie("")
	a.browserMu.Lock()
	cdp := a.cdp
	a.browserMu.Unlock()
	if cdp != nil {
		_ = cdp.SetCookie(&rutracker.BrowserCookie{
			Name: "bb_session", Value: "", Domain: ".rutracker.org", Path: "/forum/",
			HTTPOnly: true, Secure: true,
		})
	}
	a.loggedIn = false
}

// saveSession сохраняет cookie на диск (вход — один раз).
func (a *app) saveSession(text string) error {
	return os.WriteFile(a.sessionFile(), []byte(strings.TrimSpace(text)), 0o600)
}

// cmdLogin — вход в rutracker.
//
// Форум закрыт Cloudflare-challenge для программных клиентов, поэтому есть
// два пути:
//  1. вход по логину/паролю (работает, если IP не вызывает проверку);
//  2. импорт cookie из обычного браузера: приложение открывает страницу входа,
//     пользователь входит и копирует cookie в буфер обмена.
func cmdLogin(ctx context.Context) error {
	a, err := newApp()
	if err != nil {
		return err
	}
	defer a.close()

	user := os.Getenv("RUTSTREAM_USER")
	pass := os.Getenv("RUTSTREAM_PASS")
	if user != "" && pass != "" {
		if err := a.client.Login(ctx, user, pass); err == nil {
			fmt.Println("Вход выполнен. Сессия:", mask(a.client.SessionCookie()))
			return nil
		} else if !errors.Is(err, rutracker.ErrChallenge) {
			return err
		}
		fmt.Println("Форма входа закрыта проверкой Cloudflare — включаем импорт из браузера.")
	}

	fmt.Println("Открываю страницу входа в вашем браузере…")
	if err := rutracker.OpenLoginPage(a.client.Mirror()); err != nil {
		return err
	}
	fmt.Println("  1) Войдите на rutracker.org (убедитесь, что в правом верхнем углу ваш ник)")
	fmt.Println("  2) Скопируйте cookie одним из способов:")
	fmt.Println("     а) DevTools → Application → Cookies → rutracker.org → bb_session → Value")
	fmt.Println("     б) DevTools → Network → любой запрос → Headers → строка Cookie → скопировать всё")
	fmt.Println("     (в варианте «б» попадёт и cf_clearance — с ней обычный клиент проходит Cloudflare)")
	fmt.Println("  3) Скопируйте выделенное (Ctrl+C) и вернитесь сюда")
	fmt.Print("  Ожидаю cookie из буфера обмена… ")

	waitCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	if err := a.client.WaitForSessionCookie(waitCtx, 5*time.Minute); err != nil {
		return err
	}
	fmt.Println("готово.")
	fmt.Println("Сессия активна:", a.client.LoggedIn(ctx))
	// Сохраняем cookie, чтобы вход был одноразовым.
	if text, err := rutracker.ReadClipboard(); err == nil {
		if err := a.saveSession(text); err != nil {
			fmt.Fprintln(os.Stderr, "не удалось сохранить сессию:", err)
		} else {
			fmt.Println("Сессия сохранена:", a.sessionFile())
		}
	}
	return nil
}

// cmdSession импортирует сессию из браузера пользователя через DevTools-протокол.
//
// Cloudflare пропускает обычный браузер с реальным профилем, но режет программные
// клиенты. Мы подключаемся к уже запущенному Edge/Chrome (--remote-debugging-port),
// один раз открываем форум и забираем оттуда cookie (включая cf_clearance).
// После этого приложение работает на обычном HTTP — браузер больше не нужен.
func cmdSession(ctx context.Context) error {
	port := rutracker.DefaultCDPPort
	if len(os.Args) > 2 {
		port = rutracker.PortFromArg(os.Args[2])
	}
	if !rutracker.CDPAvailable(port) {
		fmt.Printf("Браузер с отладкой на порту %d не запущен.\n", port)
		fmt.Println("Запустите в отдельном окне PowerShell:")
		fmt.Printf(`  & "$env:ProgramFiles(x86)\Microsoft\Edge\Application\msedge.exe" --remote-debugging-port=%d`+"\n", port)
		fmt.Println("Затем повторите: rutstream session")
		return fmt.Errorf("браузер с отладкой не запущен")
	}

	cdp, err := rutracker.ConnectCDP(port)
	if err != nil {
		return err
	}
	defer cdp.Close()

	fmt.Println("Открываю форум в вашем браузере (прохожу проверку Cloudflare)…")
	html, err := cdp.Fetch(a0().client.Mirror()+"/forum/viewforum.php?f=409", 90*time.Second)
	if err != nil {
		return fmt.Errorf("не удалось открыть страницу: %w", err)
	}
	if rutracker.IsChallenge(html) {
		return fmt.Errorf("проверка Cloudflare не пройдена: обновите страницу в окне браузера и повторите")
	}
	fmt.Println("Страница получена, забираю cookie…")

	cookies, err := cdp.Cookies()
	if err != nil {
		return fmt.Errorf("чтение cookie: %w", err)
	}
	app := a0()
	n := app.client.ImportCookiesTo(cookies)
	fmt.Printf("Перенесено cookie: %d\n", n)
	fmt.Println("cf_clearance:", app.client.HasClearance())
	fmt.Println("авторизация: ", app.client.LoggedIn(ctx))

	// Проверяем, что защищённые страницы теперь читаются.
	if _, err := app.client.Topic(ctx, 6924752); err != nil {
		return fmt.Errorf("чтение темы всё ещё не работает: %w", err)
	}
	fmt.Println("чтение темы:   ок")
	topics, err := app.client.Search(ctx, "radiohead", 1698, 0)
	if err != nil {
		return fmt.Errorf("поиск всё ещё не работает: %w", err)
	}
	fmt.Printf("поиск:         ок, найдено %d\n", len(topics))
	return nil
}

// a0 создаёт приложение с подключённым клиентом (для импорта cookie).
func a0() *app {
	if globalApp == nil {
		globalApp = &app{}
		if c, err := rutracker.New(rutracker.Options{Timeout: 30 * time.Second}); err == nil {
			globalApp.client = c
		}
	}
	return globalApp
}

// globalApp — простое переиспользование клиента между командами.
var globalApp *app

// cmdCheck проверяет доступ: авторизация, разделы, защищённая страница.
func cmdCheck(ctx context.Context) error {
	a, err := newApp()
	if err != nil {
		return err
	}
	defer a.close()

	fmt.Printf("зеркало:            %s\n", a.client.Mirror())
	fmt.Printf("bb_session:         %s\n", mask(a.client.SessionCookie()))
	fmt.Printf("cf_clearance:       %v\n", a.client.HasClearance())
	fmt.Printf("авторизация:        %v\n", a.client.LoggedIn(ctx))

	// Защищённая страница (обычно закрыта Cloudflare-challenge без сессии).
	if _, err := a.client.Topic(ctx, 6924752); err != nil {
		fmt.Printf("чтение темы:        ошибка: %v\n", err)
	} else {
		fmt.Println("чтение темы:        ок")
	}
	// Поиск.
	if topics, err := a.client.Search(ctx, "radiohead", 1698, 0); err != nil {
		fmt.Printf("поиск:              ошибка: %v\n", err)
	} else {
		fmt.Printf("поиск:              ок, найдено %d\n", len(topics))
	}
	fmt.Printf("браузер:            %s\n", browserStatus(a))
	return nil
}

// browserStatus описывает состояние скрытого браузера (для check).
func browserStatus(a *app) string {
	if a.browserReady() {
		return "скрытый (headless), запущен"
	}
	if os.Getenv("RUTSTREAM_NO_BROWSER") != "" {
		return "отключён (RUTSTREAM_NO_BROWSER)"
	}
	return "не понадобился — сайт отвечает напрямую"
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

	// Поиск по названию идёт по всему сайту (раздел фильтруется у нас),
	// если пользователь явно не указал раздел.
	topics, err := a.client.Search(ctx, query, int(forumID), 0)
	if err != nil {
		return fmt.Errorf("поиск: %w", err)
	}
	if forumID == 0 {
		fmt.Printf("Найдено %d раздач (по всему сайту)\n\n", len(topics))
	} else {
		fmt.Printf("Найдено %d раздач в разделе %d\n\n", len(topics), forumID)
	}
	for _, t := range topics {
		lossless := ""
		if isLosslessTitle(t.Title) {
			lossless = "lossless"
		}
		fmt.Printf("[%d] %s\n", t.ID, t.Title)
		fmt.Printf("     %s | сиды %d | личеры %d | раздел %d %s | %s\n",
			t.SizeText, t.Seeds, t.Peers, t.ForumID, t.Category, lossless)
		cp := catalog.Topic{
			ID: t.ID, ForumID: int64(t.ForumID), Title: t.Title, InfoHash: t.InfoHash, Size: t.Size,
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
	size := topic.SizeText
	if size == "" && topic.Size > 0 {
		size = human(topic.Size)
	}
	fmt.Printf("размер: %s | сидов: %d | личеров: %d\n", size, topic.Seeds, topic.Peers)
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
	// Индекс строим с доступом к содержимому раздачи: у однофайловых
	// релизов («FLAC (image+.cue)») список треков даёт cue-файл, а не имя файла.
	session, err := a.engine.Start(ctx, mi, torrent.StartOptions{Trackers: trackersOf(mi)})
	if err != nil {
		return err
	}
	defer session.Close()
	ix, err := (&playback.IndexBuilder{Session: session, Info: info}).Build(ctx, mediaindex.Hints{})
	if err != nil {
		return err
	}
	total := info.TotalLength()
	fmt.Printf("Раздача: %s | piece %s | piece'ов %d | файлов %d | треков %d\n\n",
		human(total), human(info.PieceLength), info.NumPieces(), len(ix.Files), len(ix.Tracks))
	if len(ix.Tracks) <= 1 && len(ix.Files) > 1 {
		fmt.Println("Файлы раздачи:")
		for i, f := range ix.Files {
			if i >= 20 {
				fmt.Printf("  … ещё %d\n", len(ix.Files)-i)
				break
			}
			fmt.Printf("  %-70s %s\n", mediaindex.FilePath(f), human(f.Length))
		}
		fmt.Println()
	}
	fmt.Printf("%-4s %-8s %-10s %-11s %-9s %-6s %s\n",
		"№", "длит.", "размер", "скачаем", "лишние", "кодек", "трек")
	for i, t := range ix.Tracks {
		bytes, _, extra := mediaindex.EstimateTrack(info, t)
		fmt.Printf("%-4d %-8s %-10s %-11s %-9s %-6s %s\n",
			i+1, dur(t.DurationSec), human(t.ByteEnd-t.ByteStart),
			human(bytes), human(extra), t.Codec, t.Title)
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

	session, err := a.engine.Start(ctx, mi, torrent.StartOptions{
		Trackers: trackersOf(mi),
	})
	if err != nil {
		return err
	}
	defer session.Close()

	// Индекс строим с доступом к содержимому раздачи: для однофайловых
	// релизов («FLAC (image+.cue)», «альбом одним .zip») список треков
	// известен только из самого файла.
	ix, err := (&playback.IndexBuilder{Session: session, Info: info}).Build(ctx, mediaindex.Hints{})
	if err != nil {
		return err
	}
	if len(ix.Tracks) == 0 {
		return fmt.Errorf("в раздаче не найдено ни одного трека")
	}
	if trackNo < 1 || trackNo > len(ix.Tracks) {
		return fmt.Errorf("трек №%d вне диапазона (1..%d)", trackNo, len(ix.Tracks))
	}
	track := ix.Tracks[trackNo-1]
	bytes, _, extra := mediaindex.EstimateTrack(info, track)
	fmt.Printf("Трек: %s\n", track.Path)
	fmt.Printf("Скачаем %s (из раздачи %s), лишних байт: %s\n",
		human(bytes), human(info.TotalLength()), human(extra))

	player := playback.New(session)
	wav := filepath.Join(a.dataDir, "preview.wav")
	res, err := player.PlayTrack(ctx, playback.Track{
		FileIdx:      track.FileIdx,
		Path:         track.Path,
		Codec:        track.Codec,
		Title:        track.Title,
		SkipSamples:  track.SkipSamples,
		LimitSamples: track.LimitSamples,
	}, wav)
	if err != nil {
		return err
	}
	fmt.Printf("\nВоспроизведено: %d сэмплов (%.1f с), формат %s\n",
		res.Samples, res.DurationSec, formatName(res.Format))
	fmt.Printf("Скачано по сети: %s | время до конца: %s\n", human(res.BytesDownloaded), res.Elapsed.Round(time.Millisecond))
	fmt.Printf("WAV: %s\n", wav)
	// В режиме RAM освобождаем память под скачанные piece'ы.
	player.ReleaseTrack(playback.Track{FileIdx: track.FileIdx}, info)
	fmt.Printf("память под piece'ы: %s\n", human(a.engine.MemoryUsed()))
	return nil
}

// cmdRaw отдаёт сырой HTML страницы (для отладки парсеров).
//
// Использование: rutstream raw /forum/viewtopic.php?t=6917156 [файл]
func cmdRaw(ctx context.Context, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("нужен путь страницы, например /forum/index.php")
	}
	a, err := newApp()
	if err != nil {
		return err
	}
	defer a.close()
	data, err := a.client.Get(ctx, args[0])
	if err != nil {
		return err
	}
	if len(args) > 1 {
		if err := os.WriteFile(args[1], data, 0o644); err != nil {
			return err
		}
		fmt.Printf("сохранено: %s (%d байт)\n", args[1], len(data))
		return nil
	}
	os.Stdout.Write(data)
	return nil
}

func cmdDownload(ctx context.Context, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("нужны topicID и номер трека (или album/discography)")
	}
	id, _ := strconv.ParseInt(args[0], 10, 64)
	what := "1"
	if len(args) > 1 {
		what = args[1]
	}
	// Скачивание всегда идёт на диск: результат должен остаться у пользователя.
	a, err := newAppWithStorage("disk")
	if err != nil {
		return err
	}
	closed := false
	defer func() {
		if !closed {
			a.close()
		}
	}()

	mi, info, err := a.fetchTorrent(ctx, id)
	if err != nil {
		return err
	}

	// Сессия нужна и для индекса (однофайловые раздачи: треклист берётся из
	// cue-файла), и для самой загрузки.
	session, err := a.engine.Start(ctx, mi, torrent.StartOptions{Trackers: trackersOf(mi)})
	if err != nil {
		return err
	}
	defer session.Close()

	ix, err := (&playback.IndexBuilder{Session: session, Info: info}).Build(ctx, mediaindex.Hints{})
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
		album := ""
		if len(ix.Tracks) > 0 {
			album = ix.Tracks[0].Album
		}
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
			return fmt.Errorf("трек номером не указан: %q", what)
		}
		if n < 1 || n > len(ix.Tracks) {
			return fmt.Errorf("трек №%d вне диапазона (1..%d)", n, len(ix.Tracks))
		}
		track := ix.Tracks[n-1]
		fileIdxs = []int{track.FileIdx}
		label = fmt.Sprintf("трек №%d (%s)", n, track.Title)
		if track.Source == mediaindex.SourceCue {
			// Трек лежит внутри альбома: отдельного файла нет, придётся
			// скачать весь FLAC целиком.
			label += " — внутри альбома, скачается весь FLAC"
		}
	}

	// Оценка объёма до старта.
	var total int64
	for _, i := range fileIdxs {
		if p, err := mediaindex.PlanForFile(info, i); err == nil {
			total += p.ByteEnd - p.ByteStart
		}
	}
	fmt.Printf("Задание: %s\n", label)
	fmt.Printf("Файлов: %d, объём: %s (раздача целиком: %s)\n",
		len(fileIdxs), human(total), human(info.TotalLength()))

	fetcher := &download.TorrentFetcher{
		Engine: a.engine,
		MetaInfo: func(ctx context.Context, task *download.Task) (*metainfo.MetaInfo, error) {
			return mi, nil
		},
	}
	mgr := download.NewManager(fetcher, slog.Default())
	hash, _ := torrent.InfoHashOf(mi)
	mgr.Add(download.Task{
		Kind:     kind,
		TopicID:  id,
		Title:    label,
		InfoHash: hash,
		FileIdxs: fileIdxs,
		Priority: 10,
	})

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		_ = mgr.Run(runCtx)
	}()
	start := time.Now()
	var lastDone int64
	for {
		tasks := mgr.Tasks()
		if len(tasks) == 0 {
			break
		}
		t := tasks[0]
		switch t.State {
		case download.StateDone:
			elapsed := time.Since(start).Seconds()
			speed := 0.0
			if elapsed > 0 {
				speed = float64(t.BytesDone) / elapsed
			}
			fmt.Printf("\r  %s / %s (100%%) — %s при %.1f МБ/с        \n",
				human(t.BytesDone), human(t.BytesTotal), human(t.BytesDone), speed/(1<<20))
			fmt.Printf("Готово: %s\n", human(t.BytesDone))
			fmt.Printf("Файлы: %s\n", filepath.Join(a.dataDir, "data"))
			// Убираем «дырявые» файлы-соседи: anacrolix создаёт их при
			// записи граничных piece'ов (полный размер, но с пропусками).
			// Удалять можно только после закрытия клиента — пока файл
			// открыт, Windows его не удалит.
			partial := t.PartialFiles
			session.Close()
			a.close()
			closed = true
			if n := session.RemoveFiles(partial); n > 0 {
				fmt.Printf("удалено частичных файлов: %d\n", n)
			}
			return nil
		case download.StateFailed:
			return fmt.Errorf("задание не выполнено: %s", t.Error)
		}
		// Скорость за последний интервал.
		now := time.Now()
		speed := 0.0
		if d := now.Sub(start).Seconds(); d > 0 {
			speed = float64(t.BytesDone-lastDone) / d
		}
		lastDone = t.BytesDone
		fmt.Printf("\r  %s / %s (%.1f%%) — %.2f МБ/с   ",
			human(t.BytesDone), human(t.BytesTotal), t.Progress(), speed/(1<<20))
		time.Sleep(500 * time.Millisecond)
		start = time.Now()
	}
	return nil
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

// mask скрывает значение cookie.
func mask(s string) string {
	if len(s) <= 6 {
		return "***"
	}
	return s[:3] + "…" + s[len(s)-3:]
}
