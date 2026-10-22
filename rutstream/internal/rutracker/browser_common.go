package rutracker

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// BrowserUserAgent — User-Agent, под которым работает и скрытый браузер,
// и обычный HTTP-клиент.
//
// Cloudflare привязывает cookie cf_clearance к User-Agent. Если браузер
// получает clearance под одним UA, а HTTP-клиент приходит с другим, проверка
// снова не проходит. Поэтому UA общий: его же передаём браузеру через
// --user-agent (в headless-режиме Edge по умолчанию представляется
// HeadlessChrome, и Cloudflare такой браузер не пропускает).
var (
	uaOnce sync.Once
	uaVal  string
)

// BrowserUserAgent возвращает UA установленного Edge/Chrome.
func BrowserUserAgent() string {
	uaOnce.Do(func() {
		uaVal = detectBrowserUserAgent()
	})
	return uaVal
}

// detectBrowserUserAgent строит UA по версии установленного браузера.
func detectBrowserUserAgent() string {
	bin := findBrowserBinary()
	if bin == "" {
		return fallbackUserAgent
	}
	appDir := filepath.Dir(bin)
	version := newestVersionDir(appDir)
	if version == "" {
		return fallbackUserAgent
	}
	major := strings.SplitN(version, ".", 2)[0]
	if _, err := strconv.Atoi(major); err != nil {
		return fallbackUserAgent
	}
	base := "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 " +
		"(KHTML, like Gecko) Chrome/" + major + ".0.0.0 Safari/537.36"
	if strings.Contains(strings.ToLower(bin), "msedge") {
		return base + " Edg/" + major + ".0.0.0"
	}
	return base
}

// fallbackUserAgent — обычный Edge UA: используется, если версию определить
// не удалось. Главное — чтобы в строке не было "HeadlessChrome".
const fallbackUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) " +
	"AppleWebKit/537.36 (KHTML, like Gecko) Chrome/155.0.0.0 Safari/537.36 Edg/155.0.0.0"

// versionDirRe — имя каталога версии браузера (155.0.4283.62).
var versionDirRe = regexp.MustCompile(`^\d+\.\d+\.\d+\.\d+$`)

// newestVersionDir возвращает самую новую версию среди подкаталогов appDir.
func newestVersionDir(appDir string) string {
	entries, err := os.ReadDir(appDir)
	if err != nil {
		return ""
	}
	best := ""
	var bestParts [4]int
	for _, e := range entries {
		if !e.IsDir() || !versionDirRe.MatchString(e.Name()) {
			continue
		}
		parts := strings.Split(e.Name(), ".")
		var nums [4]int
		for i := 0; i < 4 && i < len(parts); i++ {
			nums[i], _ = strconv.Atoi(parts[i])
		}
		if best == "" || lessVersion(bestParts, nums) {
			best = e.Name()
			bestParts = nums
		}
	}
	return best
}

// lessVersion сообщает, что a старее b.
func lessVersion(a, b [4]int) bool {
	for i := 0; i < 4; i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

// browserBinaries — кандидаты для запуска скрытого браузера.
var browserBinaries = []string{
	`C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`,
	`C:\Program Files\Microsoft\Edge\Application\msedge.exe`,
	`C:\Program Files\Google\Chrome\Application\chrome.exe`,
	`C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`,
}

// findBrowserBinary ищет установленный Edge/Chrome.
func findBrowserBinary() string {
	for _, p := range browserBinaries {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p
		}
	}
	return ""
}

// envEnabled сообщает, что переменная окружения задана и не равна 0/false.
func envEnabled(name string) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(name)))
	return v != "" && v != "0" && v != "false"
}
