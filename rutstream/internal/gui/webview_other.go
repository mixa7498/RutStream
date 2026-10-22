//go:build !windows

package gui

import "os/exec"

// WebView2Installed — на других системах окна WebView2 нет.
func WebView2Installed() bool { return false }

// WebView2Version — пустая строка.
func WebView2Version() string { return "" }

// WebView2Hint — подсказка для не-Windows.
func WebView2Hint() string {
	return "Окно приложения доступно только в Windows."
}

// ShowError — заглушка.
func ShowError(title, text string) {}

// ShowInfo — заглушка.
func ShowInfo(title, text string) {}

// OpenInBrowser открывает адрес системным браузером.
func OpenInBrowser(url string) error {
	return exec.Command("xdg-open", url).Start()
}

// StringVersion — версия WebView2.
func StringVersion() string { return "" }
