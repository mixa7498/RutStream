//go:build browser

package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mixa7/rutstream/internal/rutracker"
)

// loginViaBrowser выполняет вход через headless-браузер (обход Cloudflare-challenge)
// и переносит cookies в обычный HTTP-клиент.
func loginViaBrowser(ctx context.Context, c *rutracker.Client, user, pass string) error {
	br, err := rutracker.NewBrowser(rutracker.BrowserOptions{
		Headless:   true,
		Timeout:    90 * time.Second,
		ProfileDir: browserProfileDir(),
	})
	if err != nil {
		return err
	}
	defer br.Close()

	html, err := br.PostForm(ctx, c.Mirror()+"/forum/login.php", map[string]string{
		"username":     user,
		"password":     pass,
		"autologin":    "on",
		"login:submit": "Вход",
	})
	if err != nil {
		return fmt.Errorf("браузерный вход: %w", err)
	}
	if err := br.ExportCookies(c); err != nil {
		return fmt.Errorf("перенос cookies: %w", err)
	}
	s := string(html)
	if !strings.Contains(s, "logout.php") && !strings.Contains(s, "Мой профиль") &&
		!strings.Contains(s, "Выход") {
		return fmt.Errorf("вход не удался: страница не содержит признака авторизации")
	}
	return nil
}
