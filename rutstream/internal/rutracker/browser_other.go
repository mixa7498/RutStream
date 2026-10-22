//go:build !windows

package rutracker

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
)

// browserUnixBinaries — кандидаты для запуска на Linux/macOS.
var browserUnixBinaries = []string{
	"/usr/bin/google-chrome",
	"/usr/bin/chromium",
	"/usr/bin/chromium-browser",
	"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
}

// OpenBrowserWithProfile запускает браузер с отладкой и отдельным профилем.
//
// На Linux/macOS браузер тоже работает без окна: он нужен только для
// проверки Cloudflare. RUTSTREAM_BROWSER_VISIBLE=1 показывает окно.
func OpenBrowserWithProfile(port int, profileDir string) (*os.Process, error) {
	bin := ""
	for _, p := range browserUnixBinaries {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			bin = p
			break
		}
	}
	if bin == "" {
		return nil, fmt.Errorf("не найден Chrome или Chromium")
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
		"--disable-background-timer-throttling",
		"--disable-renderer-backgrounding",
		"--disable-backgrounding-occluded-windows",
	}
	if !envEnabled("RUTSTREAM_BROWSER_VISIBLE") {
		args = append(args, "--headless=new")
	}
	_ = runtime.GOOS
	cmd := exec.Command(bin, args...)
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("запуск браузера: %w", err)
	}
	return cmd.Process, nil
}
