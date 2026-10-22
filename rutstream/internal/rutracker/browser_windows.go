//go:build windows

package rutracker

import (
	"fmt"
	"os"
	"os/exec"
)

// OpenBrowserWithProfile запускает браузер с отладкой и отдельным профилем.
//
// По умолчанию браузер работает БЕЗ ОКНА (headless): он нужен только для
// прохождения проверки Cloudflare, и пользователь его не видит. Чтобы
// Cloudflare не распознал headless, браузеру подставляется обычный User-Agent
// (см. BrowserUserAgent).
//
// RUTSTREAM_BROWSER_VISIBLE=1 — показать окно браузера. Нужно только для
// отладки: например, если сайт просит ввести капчу.
func OpenBrowserWithProfile(port int, profileDir string) (*os.Process, error) {
	bin := findBrowserBinary()
	if bin == "" {
		return nil, fmt.Errorf("не найден Edge или Chrome")
	}
	if err := os.MkdirAll(profileDir, 0o755); err != nil {
		return nil, err
	}

	args := []string{
		fmt.Sprintf("--remote-debugging-port=%d", port),
		fmt.Sprintf("--user-data-dir=%s", profileDir),
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-blink-features=AutomationControlled",
		"--user-agent=" + BrowserUserAgent(),
		"--mute-audio",
		"--disable-features=Translate,MediaRouter",
		// Не даём браузеру «засыпать»: challenge Cloudflare выполняется
		// скриптами, и фоновые таймеры должны работать.
		"--disable-background-timer-throttling",
		"--disable-renderer-backgrounding",
		"--disable-backgrounding-occluded-windows",
	}
	if !envEnabled("RUTSTREAM_BROWSER_VISIBLE") {
		args = append(args, "--headless=new")
	}

	cmd := exec.Command(bin, args...)
	// Окно консоли (если оно вдруг появится) не показываем; вывод браузера
	// приложению не нужен.
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("запуск браузера: %w", err)
	}
	return cmd.Process, nil
}
