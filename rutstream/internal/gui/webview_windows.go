//go:build windows

package gui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"unsafe"
)

// WebView2Installed проверяет, установлен ли WebView2 Runtime.
//
// Без него окно не создаётся, а библиотека go-webview2 завершает процесс
// через log.Fatal — поэтому проверяем заранее и показываем понятное сообщение.
func WebView2Installed() bool { return WebView2Version() != "" }

// WebView2Version возвращает версию установленного WebView2 Runtime.
func WebView2Version() string {
	dirs := []string{
		`C:\Program Files (x86)\Microsoft\EdgeWebView\Application`,
		`C:\Program Files\Microsoft\EdgeWebView\Application`,
	}
	if local := os.Getenv("LOCALAPPDATA"); local != "" {
		dirs = append(dirs, filepath.Join(local, `Microsoft\EdgeWebView\Application`))
	}
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			bin := filepath.Join(dir, e.Name(), "msedgewebview2.exe")
			if _, err := os.Stat(bin); err == nil {
				return e.Name()
			}
		}
	}
	return ""
}

// WebView2Hint — что делать, если WebView2 не установлен.
func WebView2Hint() string {
	return "Не найден WebView2 Runtime — без него окно приложения не создаётся.\n\n" +
		"Установите его (бесплатно, один раз):\n" +
		"https://developer.microsoft.com/microsoft-edge/webview2/\n\n" +
		"После установки запустите RutStream снова."
}

// ShowError показывает модальное окно с ошибкой.
func ShowError(title, text string) { showMessage(title, text, 0x10) } // MB_ICONERROR

// ShowInfo показывает информационное окно.
func ShowInfo(title, text string) { showMessage(title, text, 0x40) } // MB_ICONINFORMATION

// showMessage выводит окно сообщения средствами Windows.
func showMessage(title, text string, flags uint32) {
	t, err := syscall.UTF16PtrFromString(title)
	if err != nil {
		return
	}
	m, err := syscall.UTF16PtrFromString(text)
	if err != nil {
		return
	}
	user32 := syscall.NewLazyDLL("user32.dll")
	user32.NewProc("MessageBoxW").Call(
		0, uintptr(unsafe.Pointer(m)), uintptr(unsafe.Pointer(t)), uintptr(flags))
}

// OpenInBrowser открывает адрес в браузере по умолчанию.
func OpenInBrowser(url string) error {
	if err := startDetached("rundll32", "url.dll,FileProtocolHandler", url); err == nil {
		return nil
	}
	return startDetached("cmd", "/c", "start", "", url)
}

// startDetached запускает программу без ожидания завершения.
func startDetached(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Start()
}

// StringVersion возвращает версию для диагностики.
func StringVersion() string {
	if v := WebView2Version(); v != "" {
		return fmt.Sprintf("WebView2 %s", v)
	}
	return "WebView2 не найден"
}
