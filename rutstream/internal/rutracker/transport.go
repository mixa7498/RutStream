package rutracker

import (
	"context"
	"fmt"
	"net/url"
	"strings"
)

// Transport — способ получить страницу форума.
//
// Прямой HTTP-клиент упирается в Cloudflare: проверяются не только cookie,
// но и TLS-отпечаток клиента, поэтому cookie из браузера не помогают Go-программе.
// Браузерный транспорт выполняет запрос в ВАШЕМ браузере (реальный профиль,
// реальные cookie, реальный отпечаток) — Cloudflare такой запрос пропускает.
type Transport interface {
	// Fetch выполняет запрос и возвращает тело ответа.
	Fetch(ctx context.Context, method, rawURL string, form url.Values) ([]byte, error)
	// Cookies возвращает cookie браузера (для импорта в HTTP-клиент).
	Cookies() ([]*BrowserCookie, error)
}

// SetTransport задаёт резервный транспорт для обхода Cloudflare-challenge.
func (c *Client) SetTransport(t Transport) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.transport = t
}

// Transport возвращает установленный резервный транспорт.
func (c *Client) Transport() Transport {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.transport
}

// fetchViaTransport выполняет запрос через резервный транспорт.
func (c *Client) fetchViaTransport(ctx context.Context, method, path string, form url.Values) ([]byte, error) {
	t := c.Transport()
	if t == nil {
		return nil, ErrChallenge
	}
	return t.Fetch(ctx, method, c.URL(path), form)
}

// IsBinaryContent признаёт содержимое, которое нельзя читать как текст.
func IsBinaryContent(b []byte) bool {
	for _, x := range b {
		if x == 0 {
			return true
		}
	}
	return false
}

// DescribePage кратко описывает страницу (для диагностики).
func DescribePage(html []byte) string {
	s := string(html)
	if i := strings.Index(s, "<title>"); i >= 0 {
		if j := strings.Index(s[i:], "</title>"); j > 0 {
			return strings.TrimSpace(s[i+7 : i+j])
		}
	}
	if len(s) > 120 {
		return s[:120]
	}
	return s
}

// ErrNoTransport — резервный транспорт не настроен.
var ErrNoTransport = fmt.Errorf("rutracker: браузерный транспорт не настроен")
