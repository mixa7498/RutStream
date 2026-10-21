package rutracker

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

// formhashRe извлекает скрытое поле formhash (phpBB3).
var formhashRe = regexp.MustCompile(`name="formhash"\s+value="([a-zA-Z0-9]+)"`)

// Login выполняет вход на форум.
func (c *Client) Login(ctx context.Context, username, password string) error {
	page, err := c.Get(ctx, "/forum/login.php")
	if err != nil {
		if err == ErrChallenge {
			return fmt.Errorf("%w: для входа нужен браузерный транспорт", ErrChallenge)
		}
		return err
	}
	form := url.Values{}
	form.Set("username", username)
	form.Set("password", password)
	form.Set("autologin", "on")
	form.Set("login", "Вход")
	form.Set("redirect", "index.php")
	if m := formhashRe.FindSubmatch(page); m != nil {
		form.Set("formhash", string(m[1]))
	}
	resp, err := c.http.Post(c.URL("/forum/login.php"), "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("вход: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusFound && resp.StatusCode != http.StatusMovedPermanently {
		return fmt.Errorf("вход: HTTP %d", resp.StatusCode)
	}
	if c.SessionCookie() == "" {
		return fmt.Errorf("%w: сервер не выдал cookie bb_session", ErrAuth)
	}
	return nil
}

// LoggedIn проверяет, что сессия действительна.
func (c *Client) LoggedIn(ctx context.Context) bool {
	page, err := c.Get(ctx, "/forum/index.php")
	if err != nil {
		return false
	}
	s := string(page)
	return strings.Contains(s, "bb_session") == false &&
		(strings.Contains(s, "logout.php") || strings.Contains(s, "Мой профиль") || strings.Contains(s, "Выход"))
}
