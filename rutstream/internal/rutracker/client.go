// Package rutracker — клиент форума rutracker.org: авторизация, поиск, разбор
// страницы темы, список внутренних файлов торрента, скачивание .torrent.
package rutracker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/html/charset"
)

// Mirrors — домены rutracker в порядке предпочтения.
var Mirrors = []string{
	"https://rutracker.org",
	"https://rutracker.net",
	"https://rutracker.nl",
}

// UserAgent — User-Agent, которым мы представляемся.
const UserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

// ErrChallenge — сайт вернул Cloudflare-challenge вместо страницы.
var ErrChallenge = errors.New("rutracker: требуется проверка Cloudflare")

// ErrAuth — нет авторизации или сессия истекла.
var ErrAuth = errors.New("rutracker: требуется авторизация")

// Options — настройки клиента.
type Options struct {
	// ProxyURL — socks5:// или http:// прокси (пусто = напрямую).
	ProxyURL string
	// Timeout — таймаут одного запроса.
	Timeout time.Duration
	// Logger.
	Logger Logger
	// UserAgent.
	UserAgent string
}

// Logger — минимальный интерфейс логирования.
type Logger interface {
	Printf(format string, args ...any)
}

// Client — клиент форума.
type Client struct {
	opts   Options
	http   *http.Client
	jar    *cookiejar.Jar
	mirror string

	mu       sync.Mutex
	transport Transport

	// subMu/subIDs — кэш подразделов форумов (id подразделов по id раздела).
	subMu   sync.Mutex
	subIDs  map[int64][]int64

	// musicMu/musicIDs — готовый набор id музыкальных разделов с подразделами
	// (задаётся снаружи, чтобы не обходить форум повторно).
	musicMu  sync.Mutex
	musicIDs map[int]bool
}

// New создаёт клиента.
func New(opts Options) (*Client, error) {
	if opts.Timeout <= 0 {
		opts.Timeout = 30 * time.Second
	}
	if opts.UserAgent == "" {
		opts.UserAgent = UserAgent
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	tr := &http.Transport{
		MaxIdleConns:        20,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 15 * time.Second,
	}
	if opts.ProxyURL != "" {
		u, err := url.Parse(opts.ProxyURL)
		if err != nil {
			return nil, fmt.Errorf("прокси: %w", err)
		}
		tr.Proxy = http.ProxyURL(u)
	}
	c := &Client{
		opts:   opts,
		jar:    jar,
		mirror: Mirrors[0],
		http: &http.Client{
			Timeout:   opts.Timeout,
			Transport: tr,
			Jar:       jar,
		},
	}
	return c, nil
}

// Mirror возвращает текущий домен.
func (c *Client) Mirror() string { return c.mirror }

// SetMirror меняет текущий домен.
func (c *Client) SetMirror(m string) { c.mirror = m }

// URL строит абсолютный URL на текущем зеркале.
func (c *Client) URL(path string) string {
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		return path
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return c.mirror + path
}

// Get выполняет GET-запрос и возвращает тело ответа в UTF-8.
//
// Форум отдаётся в windows-1251, поэтому ответ перекодируется.
func (c *Client) Get(ctx context.Context, path string) ([]byte, error) {
	return c.do(ctx, http.MethodGet, path, nil)
}

// Post выполняет POST с формой (application/x-www-form-urlencoded).
func (c *Client) Post(ctx context.Context, path string, form url.Values) ([]byte, error) {
	return c.do(ctx, http.MethodPost, path, form)
}

func (c *Client) do(ctx context.Context, method, path string, form url.Values) ([]byte, error) {
	full := c.URL(path)
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, full, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.opts.UserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "ru-RU,ru;q=0.9,en;q=0.8")
	req.Header.Set("Referer", c.mirror+"/forum/index.php")
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("запрос %s: %w", full, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusFound, http.StatusMovedPermanently:
		// Редирект на страницу логина.
		loc := resp.Header.Get("Location")
		if strings.Contains(loc, "login") {
			return nil, ErrAuth
		}
		return nil, fmt.Errorf("rutracker: неожиданный редирект на %s", loc)
	case http.StatusForbidden, http.StatusServiceUnavailable:
		if IsChallenge(raw) {
			// Прямой запрос не прошёл проверку Cloudflare — пробуем браузер.
			return c.doViaTransport(ctx, method, path, form)
		}
		return nil, fmt.Errorf("rutracker: HTTP %d (%s)", resp.StatusCode, resp.Status)
	default:
		return nil, fmt.Errorf("rutracker: HTTP %d (%s)", resp.StatusCode, resp.Status)
	}
	decoded, err := Decode(raw)
	if err != nil {
		return nil, err
	}
	if IsChallenge(decoded) {
		return c.doViaTransport(ctx, method, path, form)
	}
	return decoded, nil
}

// doViaTransport повторяет запрос через браузерный транспорт.
func (c *Client) doViaTransport(ctx context.Context, method, path string, form url.Values) ([]byte, error) {
	t := c.Transport()
	if t == nil {
		return nil, ErrChallenge
	}
	raw, err := t.Fetch(ctx, method, c.URL(path), form)
	if err != nil {
		return nil, fmt.Errorf("браузерный транспорт: %w", err)
	}
	if IsChallenge(raw) {
		return nil, ErrChallenge
	}
	// Браузер отдаёт HTML уже в UTF-8 — перекодировать нельзя, иначе
	// русские заголовки превратятся в мусор. Двоичные данные (.torrent)
	// не трогаем вовсе.
	return raw, nil
}

// IsChallenge определяет Cloudflare-challenge по телу ответа.
func IsChallenge(body []byte) bool {
	s := string(body)
	if len(s) > 4000 {
		s = s[:4000]
	}
	return strings.Contains(s, "Just a moment") ||
		strings.Contains(s, "Один момент") ||
		strings.Contains(s, "cf-browser-verification") ||
		strings.Contains(s, "challenge-platform")
}

// Decode перекодирует ответ форума из windows-1251 в UTF-8.
func Decode(b []byte) ([]byte, error) {
	r, err := charset.NewReaderLabel("windows-1251", strings.NewReader(string(b)))
	if err != nil {
		return b, nil
	}
	out, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// SessionCookie возвращает значение cookie сессии (bb_session), если есть.
func (c *Client) SessionCookie() string {
	u, _ := url.Parse(c.mirror)
	for _, ck := range c.jar.Cookies(u) {
		if ck.Name == "bb_session" {
			return ck.Value
		}
	}
	return ""
}

// SetSessionCookie устанавливает cookie сессии вручную (импорт из браузера).
func (c *Client) SetSessionCookie(value string) {
	u, _ := url.Parse(c.mirror)
	c.jar.SetCookies(u, []*http.Cookie{{Name: "bb_session", Value: value, Path: "/"}})
}

// SetCookie устанавливает произвольную cookie (перенос из браузера).
func (c *Client) SetCookie(name, value, domain, path string) {
	if path == "" {
		path = "/"
	}
	if domain != "" {
		domain = strings.TrimPrefix(domain, ".")
		u := &url.URL{Scheme: "https", Host: domain}
		c.jar.SetCookies(u, []*http.Cookie{{Name: name, Value: value, Path: path}})
		return
	}
	u, _ := url.Parse(c.mirror)
	c.jar.SetCookies(u, []*http.Cookie{{Name: name, Value: value, Path: path}})
}
