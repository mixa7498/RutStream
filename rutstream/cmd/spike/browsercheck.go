//go:build browser

package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/mixa7/rutstream/internal/rutracker"
)

// runBrowserCheck открывает страницу в headless-браузере и печатает,
// что именно пришло: форму входа или challenge.
func runBrowserCheck(args []string) error {
	url := "https://rutracker.org/forum/login.php"
	if len(args) > 0 {
		url = args[0]
	}
	br, err := rutracker.NewBrowser(rutracker.BrowserOptions{
		Headless:   true,
		Timeout:    60 * time.Second,
		ProfileDir: browserProfileDir(),
	})
	if err != nil {
		return err
	}
	defer br.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	html, err := br.Get(ctx, url)
	if err != nil {
		return err
	}
	s := string(html)
	fmt.Printf("длина HTML: %d\n", len(s))
	fmt.Printf("это challenge: %v\n", rutracker.IsChallenge(html))
	fmt.Printf("есть input[name=username]: %v\n", strings.Contains(s, `name="username"`))
	fmt.Printf("есть input[name=password]: %v\n", strings.Contains(s, `name="password"`))
	fmt.Printf("есть formhash: %v\n", strings.Contains(s, "formhash"))
	fmt.Printf("есть login.php в форме: %v\n", strings.Contains(s, "login.php"))
	if i := strings.Index(s, "<form"); i >= 0 {
		end := i + 900
		if end > len(s) {
			end = len(s)
		}
		fmt.Println("--- фрагмент формы ---")
		fmt.Println(strings.ReplaceAll(s[i:end], "\n", " "))
	} else {
		fmt.Println("--- начало страницы ---")
		if len(s) > 600 {
			s = s[:600]
		}
		fmt.Println(strings.ReplaceAll(s, "\n", " "))
	}
	return nil
}

var _ = os.Stdout
