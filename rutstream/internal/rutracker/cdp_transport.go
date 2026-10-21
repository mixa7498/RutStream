package rutracker

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// CDPTransport выполняет запросы к форуму в браузере пользователя через
// Chrome DevTools Protocol.
//
// Схема:
//   - GET  — открываем вкладку на нужный URL и забираем HTML;
//   - POST — открываем «чистую» страницу того же домена и выполняем в ней
//     fetch() с credentials:'include' (куки браузера уходят автоматически).
type CDPTransport struct {
	cdp *CDP
	// Timeout — предельное время одного запроса.
	Timeout time.Duration
}

// NewCDPTransport создаёт транспорт поверх подключения к браузеру.
func NewCDPTransport(cdp *CDP) *CDPTransport {
	return &CDPTransport{cdp: cdp, Timeout: 90 * time.Second}
}

// Cookies возвращает cookie браузера.
func (t *CDPTransport) Cookies() ([]*BrowserCookie, error) { return t.cdp.Cookies() }

// Fetch выполняет запрос через браузер.
func (t *CDPTransport) Fetch(ctx context.Context, method, rawURL string, form url.Values) ([]byte, error) {
	switch strings.ToUpper(method) {
	case "POST":
		return t.post(ctx, rawURL, form)
	default:
		// Двоичные ответы (.torrent) навигацией получить нельзя —
		// забираем их через fetch() внутри страницы.
		if isBinaryURL(rawURL) {
			return t.getBinary(ctx, rawURL)
		}
		html, err := t.cdp.Fetch(rawURL, t.Timeout)
		if err != nil {
			return nil, err
		}
		return html, nil
	}
}

// isBinaryURL определяет, что ответ — двоичный файл, а не HTML-страница.
func isBinaryURL(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	p := strings.ToLower(u.Path)
	return strings.Contains(p, "dl.php") || strings.HasSuffix(p, ".torrent")
}

// getBinary забирает двоичный ответ через fetch() в контексте страницы форума.
func (t *CDPTransport) getBinary(ctx context.Context, rawURL string) ([]byte, error) {
	// Открываем корень форума: запрос должен уйти от того же origin.
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	base := u.Scheme + "://" + u.Host + "/forum/index.php"
	if _, err := t.cdp.Fetch(base, t.Timeout); err != nil {
		return nil, err
	}
	script := fmt.Sprintf(`(async () => {
		const resp = await fetch(%q, {credentials: 'include'});
		const buf = new Uint8Array(await resp.arrayBuffer());
		let s = '';
		const chunk = 0x8000;
		for (let i = 0; i < buf.length; i += chunk) {
			s += String.fromCharCode.apply(null, buf.subarray(i, i + chunk));
		}
		return btoa(s);
	})()`, rawURL)

	conn, err := t.cdp.currentConn()
	if err != nil {
		return nil, err
	}
	b64, err := t.cdp.evaluate(conn, script)
	if err != nil {
		return nil, err
	}
	if b64 == "" {
		return nil, fmt.Errorf("CDP: пустой двоичный ответ %s", rawURL)
	}
	return base64.StdEncoding.DecodeString(b64)
}

// post выполняет POST-запрос из контекста страницы форума.
func (t *CDPTransport) post(ctx context.Context, rawURL string, form url.Values) ([]byte, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	// Открываем корень форума: запрос уйдёт от того же origin.
	base := u.Scheme + "://" + u.Host + "/forum/index.php"
	if _, err := t.cdp.Fetch(base, t.Timeout); err != nil {
		return nil, err
	}
	values := make([]string, 0, len(form))
	for k, v := range form {
		for _, item := range v {
			values = append(values, fmt.Sprintf("%q:%q", k, item))
		}
	}
	script := fmt.Sprintf(`(async () => {
		const body = new URLSearchParams({%s});
		const resp = await fetch(%q, {
			method: 'POST',
			credentials: 'include',
			body: body,
			headers: {'Content-Type': 'application/x-www-form-urlencoded'}
		});
		const buf = new Uint8Array(await resp.arrayBuffer());
		let s = '';
		const chunk = 0x8000;
		for (let i = 0; i < buf.length; i += chunk) {
			s += String.fromCharCode.apply(null, buf.subarray(i, i + chunk));
		}
		return btoa(s);
	})()`, strings.Join(values, ","), u.String())

	conn, err := t.cdp.currentConn()
	if err != nil {
		return nil, err
	}
	b64, err := t.cdp.evaluate(conn, script)
	if err != nil {
		return nil, err
	}
	if b64 == "" {
		return nil, fmt.Errorf("CDP: пустой ответ на POST %s", rawURL)
	}
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, fmt.Errorf("CDP: base64: %w", err)
	}
	return raw, nil
}

// currentConn возвращает текущее соединение с вкладкой.
func (c *CDP) currentConn() (*websocket.Conn, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil {
		return nil, fmt.Errorf("CDP: нет активной вкладки")
	}
	return c.conn, nil
}

// MarshalJSONHelper — вспомогательная функция для отладки.
func MarshalJSONHelper(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
