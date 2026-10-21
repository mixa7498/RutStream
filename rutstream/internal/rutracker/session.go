package rutracker

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// bbSessionRe выделяет значение cookie bb_session.
var bbSessionRe = regexp.MustCompile(`bb_session=([A-Za-z0-9\-_]+)`)

// OpenLoginPage открывает страницу входа в браузере пользователя по умолчанию.
//
// Так обходится Cloudflare-challenge: браузер пользователя — обычный, не
// автоматизированный, поэтому проверка проходит штатно. Пользователь входит
// и копирует значение cookie (см. ExtractSession).
func OpenLoginPage(mirror string) error {
	url := mirror + "/forum/login.php"
	switch {
	case exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Run() == nil:
		return nil
	case exec.Command("cmd", "/c", "start", "", url).Run() == nil:
		return nil
	case exec.Command("xdg-open", url).Run() == nil:
		return nil
	}
	return fmt.Errorf("не удалось открыть браузер: откройте вручную %s", url)
}

// ExtractSessionCookie достаёт значение bb_session из произвольной строки
// (например, из буфера обмена, куда скопировали document.cookie).
func ExtractSessionCookie(s string) string {
	m := bbSessionRe.FindStringSubmatch(s)
	if m == nil {
		return ""
	}
	return m[1]
}

// ReadClipboard читает текст из буфера обмена (Windows).
func ReadClipboard() (string, error) {
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", "Get-Clipboard -Raw")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("чтение буфера обмена: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// ImportSessionFromClipboard забирает из буфера обмена значение bb_session
// и проверяет его работоспособность.
func (c *Client) ImportSessionFromClipboard(ctx context.Context) error {
	text, err := ReadClipboard()
	if err != nil {
		return err
	}
	cookie := ExtractSessionCookie(text)
	if cookie == "" {
		return fmt.Errorf("в буфере обмена нет cookie bb_session — выполните в консоли браузера: copy(document.cookie)")
	}
	c.SetSessionCookie(cookie)
	if !c.LoggedIn(ctx) {
		return fmt.Errorf("cookie не принят: проверьте, что вы вошли на rutracker.org")
	}
	return nil
}

// WaitForSessionCookie ждёт, пока пользователь скопирует cookie в буфер обмена.
func (c *Client) WaitForSessionCookie(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if text, err := ReadClipboard(); err == nil {
			if cookie := ExtractSessionCookie(text); cookie != "" {
				c.SetSessionCookie(cookie)
				if c.LoggedIn(ctx) {
					return nil
				}
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return fmt.Errorf("время ожидания истекло: cookie не скопирована")
}
