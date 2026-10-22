package rutracker

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// CDP — клиент Chrome DevTools Protocol, подключающийся к уже запущенному
// браузеру пользователя (Edge/Chrome с --remote-debugging-port).
//
// Зачем: Cloudflare пропускает обычный браузер с реальным профилем, но режет
// программные клиенты. Мы один раз открываем страницу в ВАШЕМ браузере,
// получаем cookie (в т.ч. cf_clearance) и дальше работаем на обычном HTTP.
type CDP struct {
	endpoint string

	mu      sync.Mutex
	conn    *websocket.Conn
	msgID   int
	pending map[int]chan cdpResponse
	events  chan cdpResponse
	closed  bool
}

type cdpResponse struct {
	ID     int             `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

// ConnectCDP подключается к браузеру с указанным портом отладки.
func ConnectCDP(port int) (*CDP, error) {
	endpoint := fmt.Sprintf("http://127.0.0.1:%d", port)
	resp, err := http.Get(endpoint + "/json/version")
	if err != nil {
		return nil, fmt.Errorf("браузер с отладкой на порту %d недоступен: %w "+
			"(запустите Edge с --remote-debugging-port=%d)", port, err, port)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("CDP: HTTP %d", resp.StatusCode)
	}
	return &CDP{endpoint: endpoint, pending: map[int]chan cdpResponse{}}, nil
}

// CDPAvailable проверяет, запущен ли браузер с отладкой.
func CDPAvailable(port int) bool {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/json/version", port))
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	return resp.StatusCode == http.StatusOK
}

// Close закрывает соединение.
func (c *CDP) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	c.closed = true
	if c.conn != nil {
		_ = c.conn.Close()
	}
}

// newTarget создаёт вкладку с указанным URL и подключается к ней.
func (c *CDP) newTarget(rawURL string) (*websocket.Conn, error) {
	endpoint := c.endpoint + "/json/new?" + url.QueryEscape(rawURL)
	req, err := http.NewRequest(http.MethodPut, endpoint, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		resp, err = http.Get(endpoint)
		if err != nil {
			return nil, err
		}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
		return nil, fmt.Errorf("CDP: не удалось создать вкладку (HTTP %d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var target struct {
		WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&target); err != nil {
		return nil, err
	}
	if target.WebSocketDebuggerURL == "" {
		return nil, fmt.Errorf("CDP: вкладка без websocket-адреса")
	}
	conn, _, err := websocket.DefaultDialer.Dial(target.WebSocketDebuggerURL, nil)
	if err != nil {
		return nil, err
	}
	return conn, nil
}

// startReader запускает единственного читателя websocket и раздаёт ответы.
func (c *CDP) startReader(conn *websocket.Conn) {
	events := make(chan cdpResponse, 256)
	c.mu.Lock()
	c.events = events
	c.mu.Unlock()
	go func() {
		for {
			var resp cdpResponse
			if err := conn.ReadJSON(&resp); err != nil {
				close(events)
				return
			}
			if resp.ID != 0 {
				c.mu.Lock()
				ch, ok := c.pending[resp.ID]
				delete(c.pending, resp.ID)
				c.mu.Unlock()
				if ok {
					ch <- resp
					continue
				}
			}
			select {
			case events <- resp:
			default:
			}
		}
	}()
}

// call отправляет команду и ждёт ответа.
func (c *CDP) call(conn *websocket.Conn, method string, params map[string]any, timeout time.Duration) (json.RawMessage, error) {
	c.mu.Lock()
	c.msgID++
	id := c.msgID
	ch := make(chan cdpResponse, 1)
	c.pending[id] = ch
	c.mu.Unlock()

	msg := map[string]any{"id": id, "method": method}
	if params != nil {
		msg["params"] = params
	}
	if err := conn.WriteJSON(msg); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, err
	}
	select {
	case resp, ok := <-ch:
		if !ok {
			return nil, fmt.Errorf("CDP: соединение закрыто")
		}
		if resp.Error != nil {
			return nil, fmt.Errorf("CDP %s: %s", method, resp.Error.Message)
		}
		return resp.Result, nil
	case <-time.After(timeout):
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, fmt.Errorf("CDP %s: таймаут", method)
	}
}

// Fetch открывает страницу в браузере и возвращает её HTML.
//
// Страница может сначала показать challenge Cloudflare и перезагрузиться
// автоматически — поэтому опрашиваем содержимое, пока оно не перестанет
// быть challenge'ем.
func (c *CDP) Fetch(rawURL string, timeout time.Duration) ([]byte, error) {
	c.mu.Lock()
	if c.conn != nil {
		_ = c.conn.Close()
		c.conn = nil
	}
	c.mu.Unlock()

	conn, err := c.newTarget(rawURL)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()
	c.startReader(conn)

	// Включаем домены Page и Network — без этого нет событий загрузки и cookie.
	if _, err := c.call(conn, "Page.enable", nil, 10*time.Second); err != nil {
		return nil, err
	}
	if _, err := c.call(conn, "Network.enable", nil, 10*time.Second); err != nil {
		return nil, err
	}

	deadline := time.Now().Add(timeout)
	var last string
	for time.Now().Before(deadline) {
		state, html, err := c.pageState(conn)
		if err == nil {
			if html != "" {
				last = html
			}
			// Отдаём страницу, когда она загрузилась и наполнена.
			if state == "complete" && len(html) > 500 && !IsChallenge([]byte(html)) {
				return []byte(html), nil
			}
		}
		time.Sleep(400 * time.Millisecond)
	}
	if last != "" {
		return []byte(last), nil
	}
	return nil, fmt.Errorf("CDP: не удалось получить страницу %s за %s", rawURL, timeout)
}

// pageState возвращает состояние загрузки и текущий HTML страницы.
func (c *CDP) pageState(conn *websocket.Conn) (string, string, error) {
	raw, err := c.evaluate(conn, `JSON.stringify({
		state: document.readyState,
		title: document.title || '',
		html: (document.documentElement ? document.documentElement.outerHTML : '')
	})`)
	if err != nil {
		return "", "", err
	}
	var out struct {
		State string `json:"state"`
		Title string `json:"title"`
		HTML  string `json:"html"`
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return "", "", err
	}
	return out.State, out.HTML, nil
}

// evaluate выполняет JS и возвращает результат.
func (c *CDP) evaluate(conn *websocket.Conn, expr string) (string, error) {
	res, err := c.call(conn, "Runtime.evaluate", map[string]any{
		"expression":    expr,
		"returnByValue": true,
		"awaitPromise":  true,
	}, 20*time.Second)
	if err != nil {
		return "", err
	}
	var out struct {
		Result struct {
			Value string `json:"value"`
		} `json:"result"`
	}
	if err := json.Unmarshal(res, &out); err != nil {
		return "", err
	}
	return out.Result.Value, nil
}

// Cookies возвращает все cookie браузера (для переноса в HTTP-клиент).
func (c *CDP) Cookies() ([]*BrowserCookie, error) {
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn == nil {
		return nil, fmt.Errorf("CDP: нет вкладки")
	}
	res, err := c.call(conn, "Network.getAllCookies", nil, 20*time.Second)
	if err != nil {
		return nil, err
	}
	var out struct {
		Cookies []struct {
			Name     string  `json:"name"`
			Value    string  `json:"value"`
			Domain   string  `json:"domain"`
			Path     string  `json:"path"`
			Expires  float64 `json:"expires"`
			HTTPOnly bool    `json:"httpOnly"`
			Secure   bool    `json:"secure"`
		} `json:"cookies"`
	}
	if err := json.Unmarshal(res, &out); err != nil {
		return nil, err
	}
	res2 := make([]*BrowserCookie, 0, len(out.Cookies))
	for _, ck := range out.Cookies {
		res2 = append(res2, &BrowserCookie{
			Name:     ck.Name,
			Value:    ck.Value,
			Domain:   ck.Domain,
			Path:     ck.Path,
			HTTPOnly: ck.HTTPOnly,
			Secure:   ck.Secure,
		})
	}
	return res2, nil
}

// BrowserCookie — cookie браузера.
type BrowserCookie struct {
	Name     string
	Value    string
	Domain   string
	Path     string
	HTTPOnly bool
	Secure   bool
}

// ImportCookiesTo переносит cookie браузера в HTTP-клиент.
// Возвращает количество перенесённых cookie.
func (c *Client) ImportCookiesTo(cookies []*BrowserCookie) int {
	n := 0
	for _, ck := range cookies {
		if ck.Value == "" {
			continue
		}
		domain := strings.TrimPrefix(ck.Domain, ".")
		if domain != "" && !strings.Contains(c.mirror, domain) {
			continue
		}
		path := ck.Path
		if path == "" {
			path = "/"
		}
		host := &url.URL{Scheme: "https", Host: domain}
		c.jar.SetCookies(host, []*http.Cookie{{
			Name: ck.Name, Value: ck.Value, Path: path, Secure: ck.Secure, HttpOnly: ck.HTTPOnly,
		}})
		n++
	}
	return n
}

// DefaultCDPPort — порт отладки, который мы просим открыть у пользователя.
const DefaultCDPPort = 9222

// PortFromArg извлекает номер порта из аргумента.
func PortFromArg(s string) int {
	if s == "" {
		return DefaultCDPPort
	}
	p, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || p <= 0 {
		return DefaultCDPPort
	}
	return p
}

// CurrentLocation возвращает текущий адрес страницы в браузере (диагностика).
func (c *CDP) CurrentLocation() (string, error) {
	conn, err := c.currentConn()
	if err != nil {
		return "", err
	}
	return c.evaluate(conn, "location.href")
}

// EnsureTab открывает вкладку, если её ещё нет.
//
// Нужно перед Network.setCookie: cookie ставится в контексте вкладки,
// поэтому без вкладки вызов молча не срабатывает.
func (c *CDP) EnsureTab(rawURL string) error {
	c.mu.Lock()
	has := c.conn != nil
	c.mu.Unlock()
	if has {
		return nil
	}
	_, err := c.Fetch(rawURL, 45*time.Second)
	return err
}

// SetCookie устанавливает cookie в браузере.
//
// Это нужно, чтобы браузер был авторизован на форуме: сессия rutracker
// (bb_session) выдаётся сервером и действует для любого клиента, поэтому
// можно перенести её из нашего HTTP-клиента в браузер.
func (c *CDP) SetCookie(ck *BrowserCookie) error {
	conn, err := c.currentConn()
	if err != nil {
		return err
	}
	domain := ck.Domain
	if domain == "" {
		domain = "rutracker.org"
	}
	path := ck.Path
	if path == "" {
		path = "/"
	}
	params := map[string]any{
		"name":     ck.Name,
		"value":    ck.Value,
		"domain":   domain,
		"path":     path,
		"secure":   ck.Secure,
		"httpOnly": ck.HTTPOnly,
	}
	if _, err := c.call(conn, "Network.setCookie", params, 10*time.Second); err != nil {
		return err
	}
	return nil
}
