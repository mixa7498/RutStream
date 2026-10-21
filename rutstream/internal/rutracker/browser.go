//go:build browser

// Браузерный транспорт (обход Cloudflare-challenge).
//
// Собирается только с тегом "browser": go build -tags browser.
//
// ВНИМАНИЕ: многие корпоративные политики Application Control (WDAC/AppLocker)
// блокируют бинарники, способные управлять браузером. Поэтому по умолчанию
// этот файл НЕ входит в сборку, а основной путь работы с форумом —
// обычный HTTP-клиент с импортированной сессией (см. FR-RT-5).
package rutracker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
)

// ErrBrowserUnavailable — не найден браузер для браузерного транспорта.
var ErrBrowserUnavailable = errors.New("rutracker: браузер не найден (нужен Chrome или Edge)")

// browserBinaries — кандидаты для запуска.
var browserBinaries = []string{
	`C:\Program Files\Google\Chrome\Application\chrome.exe`,
	`C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`,
	`C:\Program Files\Microsoft\Edge\Application\msedge.exe`,
	`C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`,
	`/usr/bin/google-chrome`,
	`/usr/bin/chromium`,
	`/usr/bin/chromium-browser`,
	"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
}

// BrowserOptions — настройки браузерного транспорта.
type BrowserOptions struct {
	// ProfileDir — каталог профиля (куда сохраняются cookies, чтобы не логиниться заново).
	ProfileDir string
	// ProfileName — имя профиля внутри ProfileDir (для системного профиля Edge: "Default").
	ProfileName string
	// Headless — работать без окна.
	Headless bool
	// Proxy — прокси для браузера.
	Proxy string
	// Timeout — таймаут загрузки страницы.
	Timeout time.Duration
	// BinPath — явный путь к браузеру.
	BinPath string
}

// Browser — браузерный транспорт: умеет пройти Cloudflare-challenge и выполнить вход.
type Browser struct {
	mu     sync.Mutex
	browser *rod.Browser
	opts   BrowserOptions
}

// NewBrowser запускает браузер.
func NewBrowser(opts BrowserOptions) (*Browser, error) {
	if opts.Timeout <= 0 {
		opts.Timeout = 60 * time.Second
	}
	// По умолчанию — видимое окно: Cloudflare заметно агрессивнее к headless.
	if v := os.Getenv("RUTSTREAM_HEADLESS"); v != "" && v != "0" && v != "false" {
		opts.Headless = true
	}
	bin := opts.BinPath
	if bin == "" {
		bin = findBrowser()
	}
	if bin == "" {
		return nil, ErrBrowserUnavailable
	}
	profile := opts.ProfileDir
	if profile == "" {
		profile = filepath.Join(os.TempDir(), "rutstream-browser-profile")
	}
	if v := os.Getenv("RUTSTREAM_BROWSER_PROFILE_NAME"); v != "" {
		opts.ProfileName = v
	}
	if err := os.MkdirAll(profile, 0o755); err != nil {
		return nil, err
	}
	l := launcher.New().Bin(bin).
		Headless(opts.Headless).
		UserDataDir(profile).
		Set("disable-blink-features", "AutomationControlled").
		Set("lang", "ru-RU")
	if opts.ProfileName != "" {
		l = l.Set("profile-directory", opts.ProfileName)
	}
	if opts.Proxy != "" {
		l = l.Proxy(opts.Proxy)
	}
	u, err := l.Launch()
	if err != nil {
		return nil, fmt.Errorf("запуск браузера: %w", err)
	}
	b := rod.New().ControlURL(u)
	if err := b.Connect(); err != nil {
		return nil, fmt.Errorf("подключение к браузеру: %w", err)
	}
	return &Browser{browser: b, opts: opts}, nil
}

// findBrowser ищет установленный Chrome/Edge.
func findBrowser() string {
	for _, p := range browserBinaries {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p
		}
	}
	return ""
}

// Close закрывает браузер.
func (b *Browser) Close() {
	if b.browser == nil {
		return
	}
	defer func() { _ = recover() }() // закрытие не должно ронять приложение
	_ = b.browser.Close()
}

// Get открывает страницу и возвращает её HTML.
func (b *Browser) Get(ctx context.Context, url string) ([]byte, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	page, err := b.browser.Page(proto.TargetCreateTarget{URL: url})
	if err != nil {
		return nil, err
	}
	defer page.Close()
	page = page.Context(ctx)
	if err := page.Timeout(b.opts.Timeout).WaitLoad(); err != nil {
		// Возможно, страница не «загрузилась» полностью — попробуем забрать HTML как есть.
		if html, herr := page.HTML(); herr == nil && len(html) > 0 {
			return []byte(html), nil
		}
		return nil, err
	}
	// Даём Cloudflare-челленджу время пройти.
	deadline := time.Now().Add(b.opts.Timeout)
	for time.Now().Before(deadline) {
		html, err := page.HTML()
		if err == nil && !IsChallenge([]byte(html)) {
			return []byte(html), nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	html, _ := page.HTML()
	return []byte(html), nil
}

// PostForm заполняет форму на странице и отправляет её.
//
// values — карта «имя поля» → «значение». Поля вида "имя:submit" нажимаются,
// остальные заполняются текстом.
func (b *Browser) PostForm(ctx context.Context, url string, values map[string]string) ([]byte, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	page, err := b.browser.Page(proto.TargetCreateTarget{URL: url})
	if err != nil {
		return nil, err
	}
	defer page.Close()
	page = page.Context(ctx)
	// Ожидание загрузки: ошибку игнорируем — DOM может быть уже готов,
	// а событие «load» не срабатывает, пока висят фоновые запросы.
	_ = page.Timeout(15 * time.Second).WaitLoad()

	// Ждём появления поля логина (rod повторяет поиск до таймаута контекста).
	waitCtx, cancel := context.WithTimeout(ctx, b.opts.Timeout)
	defer cancel()
	page = page.Context(waitCtx)
	if _, err := page.Element("input[name=username]"); err != nil {
		return nil, fmt.Errorf("форма входа не найдена: %w", err)
	}
	for name, value := range values {
		if strings.HasSuffix(name, ":submit") {
			continue
		}
		el, err := page.Element(fmt.Sprintf("input[name=%q]", name))
		if err != nil {
			continue
		}
		if err := el.Input(value); err != nil {
			return nil, fmt.Errorf("заполнение поля %s: %w", name, err)
		}
	}
	// Отправляем форму.
	submitted := false
	if el, err := page.Element("form input[type=submit], form button[type=submit]"); err == nil {
		_ = el.Click(proto.InputMouseButtonLeft, 1)
		submitted = true
	}
	if !submitted {
		if el, err := page.Element("form"); err == nil {
			_, _ = el.Eval(`() => { document.querySelector('form').submit(); }`)
			submitted = true
		}
	}
	if !submitted {
		return nil, fmt.Errorf("не найдена кнопка отправки формы")
	}
	// Ждём перехода на страницу после входа.
	_ = page.Timeout(20 * time.Second).WaitNavigation(proto.PageLifecycleEventNameNetworkAlmostIdle)
	_ = page.Timeout(10 * time.Second).WaitLoad()
	html, err := page.HTML()
	if err != nil {
		return nil, err
	}
	if IsChallenge([]byte(html)) {
		return nil, fmt.Errorf("%w: браузер не прошёл проверку Cloudflare", ErrChallenge)
	}
	return []byte(html), nil
}

// Cookies возвращает cookies браузера (для переноса в обычный HTTP-клиент).
func (b *Browser) Cookies() ([]*proto.NetworkCookie, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.browser.GetCookies()
}

// ExportCookies переносит cookies браузера в HTTP-клиент.
func (b *Browser) ExportCookies(c *Client) error {
	cookies, err := b.Cookies()
	if err != nil {
		return err
	}
	for _, ck := range cookies {
		if ck.Value == "" {
			continue
		}
		c.SetCookie(ck.Name, ck.Value, ck.Domain, ck.Path)
	}
	return nil
}
