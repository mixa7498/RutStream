package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mixa7/rutstream/internal/rutracker"
)

// runLogin выполняет вход и проверяет сессию.
//
// Способы (в порядке приоритета):
//  1. Переменные окружения RUTSTREAM_USER / RUTSTREAM_PASS — обычный вход по форме.
//  2. RUTSTREAM_COOKIE — готовая cookie bb_session (импорт из браузера).
func runLogin(args []string) error {
	c, err := rutracker.New(rutracker.Options{Timeout: 30 * time.Second})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	if ck := os.Getenv("RUTSTREAM_COOKIE"); ck != "" {
		c.SetSessionCookie(strings.TrimSpace(ck))
		fmt.Println("Cookie сессии импортирована вручную.")
	} else {
		user := os.Getenv("RUTSTREAM_USER")
		pass := os.Getenv("RUTSTREAM_PASS")
		if user == "" || pass == "" {
			return fmt.Errorf("задайте RUTSTREAM_USER и RUTSTREAM_PASS (или RUTSTREAM_COOKIE с готовой bb_session)")
		}
		if err := c.Login(ctx, user, pass); err != nil {
			if !errors.Is(err, rutracker.ErrChallenge) {
				return err
			}
			fmt.Println("Cloudflare-challenge: вход через браузерный транспорт…")
			if err := loginViaBrowser(ctx, c, user, pass); err != nil {
				return err
			}
			fmt.Println("Вход выполнен через браузер, cookies перенесены в HTTP-клиент.")
		} else {
			fmt.Println("Вход по форме выполнен.")
		}
	}
	fmt.Println("Сес bb_session:", mask(c.SessionCookie()))
	fmt.Println("Авторизация активна:", c.LoggedIn(ctx))
	return nil
}

// runTopic открывает тему и печатает её метаданные, треклист и список файлов.
func runTopic(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: spike topic <topic_id>")
	}
	var topicID int64
	fmt.Sscanf(args[0], "%d", &topicID)

	c, err := rutracker.New(rutracker.Options{Timeout: 30 * time.Second})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if ck := os.Getenv("RUTSTREAM_COOKIE"); ck != "" {
		c.SetSessionCookie(strings.TrimSpace(ck))
	}

	page, err := c.Topic(ctx, topicID)
	if err != nil {
		return err
	}
	fmt.Printf("Тема #%d: %s\n", topicID, page.Title)
	fmt.Printf("hash: %s\nтрекеры: %s\n", page.InfoHash, strings.Join(page.Trackers, ", "))
	fmt.Printf("исполнитель: %s | альбом: %s | год: %s | жанр: %s | формат: %s | битрейт: %s\n",
		page.Artist, page.Album, page.Year, page.Genre, page.Format, page.Bitrate)
	fmt.Printf("треков в треклисте: %d\n", len(page.Tracks))
	for i, t := range page.Tracks {
		if i >= 15 {
			fmt.Printf("... ещё %d\n", len(page.Tracks)-i)
			break
		}
		fmt.Printf("  %2d. %-50s %s\n", t.Pos, t.Title, humanDur(t.Duration))
	}
	return nil
}

// runFiles печатает список внутренних файлов раздачи.
func runFiles(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: spike files <topic_id>")
	}
	var topicID int64
	fmt.Sscanf(args[0], "%d", &topicID)

	c, err := rutracker.New(rutracker.Options{Timeout: 30 * time.Second})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if ck := os.Getenv("RUTSTREAM_COOKIE"); ck != "" {
		c.SetSessionCookie(strings.TrimSpace(ck))
	}
	files, err := c.TorrentFiles(ctx, topicID)
	if err != nil {
		return err
	}
	fmt.Printf("Файлов: %d\n", len(files))
	for i, f := range files {
		if i >= 30 {
			fmt.Printf("... ещё %d\n", len(files)-i)
			break
		}
		fmt.Printf("  %-60s %s\n", f.Path, f.SizeText)
	}
	return nil
}

func humanDur(sec float64) string {
	if sec <= 0 {
		return ""
	}
	m := int(sec) / 60
	s := int(sec) % 60
	return fmt.Sprintf("%d:%02d", m, s)
}

// runFeed показывает свежие раздачи раздела (Atom-фид, без авторизации).
func runFeed(args []string) error {
	forumID := 1760
	if len(args) > 0 {
		fmt.Sscanf(args[0], "%d", &forumID)
	}
	c, err := rutracker.New(rutracker.Options{Timeout: 20 * time.Second})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	entries, err := c.Feed(ctx, forumID)
	if err != nil {
		return err
	}
	fmt.Printf("Раздел f=%d: записей %d\n", forumID, len(entries))
	for _, e := range entries {
		fmt.Printf("\n• %s\n", e.Title)
		fmt.Printf("  автор: %s | тема: %s\n", e.Author, e.URL)
		fmt.Printf("  hash:  %s\n", e.InfoHash)
		fmt.Printf("  трекеры: %s\n", strings.Join(e.Trackers, ", "))
	}
	return nil
}

// runSearch ищет раздачи в разделе (обычный HTTP-клиент).
func runSearch(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: spike search <query> <forum_id>")
	}
	query := args[0]
	var forumID int
	fmt.Sscanf(args[1], "%d", &forumID)

	c, err := rutracker.New(rutracker.Options{Timeout: 30 * time.Second})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if ck := os.Getenv("RUTSTREAM_COOKIE"); ck != "" {
		c.SetSessionCookie(strings.TrimSpace(ck))
	}
	topics, err := c.Search(ctx, query, forumID, 0)
	if err != nil {
		return err
	}
	fmt.Printf("Найдено %d раздач:\n", len(topics))
	for _, t := range topics {
		fmt.Printf("  [%d] %s\n", t.ID, t.Title)
		fmt.Printf("       размер %s | сиды %d | личеры %d | %s\n", t.SizeText, t.Seeds, t.Peers, t.Date)
	}
	return nil
}

func mask(s string) string {
	if len(s) <= 6 {
		return "***"
	}
	return s[:3] + "…" + s[len(s)-3:]
}

// browserProfileDir возвращает каталог профиля браузера (чтобы не логиниться заново).
func browserProfileDir() string {
	if dir := os.Getenv("RUTSTREAM_BROWSER_PROFILE"); dir != "" {
		return dir
	}
	return filepath.Join(os.TempDir(), "rutstream-browser-profile")
}
