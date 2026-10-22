package rutracker

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// bbSessionRe выделяет значение cookie bb_session из строки вида
// «bb_session=…», «bb_session: …» или строки таблицы DevTools.
var bbSessionRe = regexp.MustCompile(`(?i)bb_session[ \t]*[=:]?[ \t]*["']?([A-Za-z0-9_-]+)`)

// bareSessionRe — значение bb_session без имени cookie: «0-12345678-…».
var bareSessionRe = regexp.MustCompile(`^[0-9]+-[0-9]+-[A-Za-z0-9_-]+$`)

// cookiePairRe разбирает пару «имя=значение» в строке cookie.
var cookiePairRe = regexp.MustCompile(`([A-Za-z0-9_\-]+)=([^;\s]*)`)

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

// ExtractSessionCookie достаёт значение bb_session из произвольной строки.
//
// Принимается:
//   - голое значение из DevTools: «0-12345678-AbCdEf…»;
//   - строка «bb_session=…» или «bb_session: …»;
//   - полная строка cookie, скопированная из заголовка запроса.
//
// document.cookie здесь не поможет: bb_session помечена HttpOnly и в
// JavaScript не видна.
func ExtractSessionCookie(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	// Кавычки появляются, если значение скопировали из JSON или таблицы.
	s = strings.Trim(s, "\"'")
	if bareSessionRe.MatchString(s) {
		return s
	}
	if m := bbSessionRe.FindStringSubmatch(s); m != nil {
		return m[1]
	}
	return ""
}

// ExtractCookieSet разбирает строку cookie в набор «имя → значение».
//
// Нужен для совместимости: если пользователь вставил полную строку cookie,
// вместе с bb_session можно перенести и cf_clearance.
func ExtractCookieSet(s string) map[string]string {
	out := map[string]string{}
	for _, m := range cookiePairRe.FindAllStringSubmatch(s, -1) {
		out[m[1]] = m[2]
	}
	return out
}

// SetCookieSet устанавливает набор cookie для домена rutracker.
func (c *Client) SetCookieSet(cookies map[string]string) {
	if len(cookies) == 0 {
		return
	}
	u, _ := url.Parse(c.mirror)
	list := make([]*http.Cookie, 0, len(cookies))
	for name, value := range cookies {
		if value == "" {
			continue
		}
		list = append(list, &http.Cookie{Name: name, Value: value, Path: "/"})
	}
	c.jar.SetCookies(u, list)
}

// Cookies возвращает cookie текущей сессии (для переноса в скрытый браузер).
//
// Путь /forum/ берём намеренно: bb_session выдаётся именно на него, а
// cf_clearance — на весь домен. Так в выборку попадают обе.
func (c *Client) Cookies() []*http.Cookie {
	u, err := url.Parse(c.mirror + "/forum/")
	if err != nil {
		return nil
	}
	return c.jar.Cookies(u)
}

// HasClearance сообщает, есть ли среди cookie Cloudflare-разрешение.
func (c *Client) HasClearance() bool {
	u, _ := url.Parse(c.mirror)
	for _, ck := range c.jar.Cookies(u) {
		if ck.Name == "cf_clearance" && ck.Value != "" {
			return true
		}
	}
	return false
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
// (или полную строку cookie) и проверяет его работоспособность.
func (c *Client) ImportSessionFromClipboard(ctx context.Context) error {
	text, err := ReadClipboard()
	if err != nil {
		return err
	}
	return c.ImportCookies(ctx, text)
}

// ImportCookies устанавливает cookie из строки и проверяет доступ к форуму.
func (c *Client) ImportCookies(ctx context.Context, text string) error {
	cookie := ExtractSessionCookie(text)
	if cookie == "" {
		return fmt.Errorf("не найден bb_session — скопируйте значение: " +
			"DevTools → Application → Cookies → rutracker.org → bb_session → Value")
	}
	set := ExtractCookieSet(text)
	set["bb_session"] = cookie
	c.SetCookieSet(set)
	c.SetSessionCookie(cookie)
	if !c.LoggedIn(ctx) {
		return fmt.Errorf("cookie не принят: проверьте, что вы вошли на rutracker.org")
	}
	return nil
}

// WaitForSessionCookie ждёт, пока пользователь скопирует cookie в буфер обмена.
//
// Принимается и значение bb_session, и полная строка cookie.
func (c *Client) WaitForSessionCookie(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if text, err := ReadClipboard(); err == nil {
			text = strings.TrimSpace(text)
			if cookie := ExtractSessionCookie(text); cookie != "" {
				c.SetCookieSet(ExtractCookieSet(text))
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
