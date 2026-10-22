//go:build windows

package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/jchv/go-webview2"
	"github.com/mixa7/rutstream/internal/gui"
)

// cmdGUI запускает приложение с окном.
func cmdGUI(ctx context.Context, args []string) (err error) {
	// Ошибка в окне не должна превращаться в молчаливое исчезновение
	// процесса: показываем её пользователю.
	defer func() {
		if r := recover(); r != nil {
			msg := fmt.Sprintf("Непредвиденная ошибка при запуске окна:\n%v", r)
			fmt.Fprintln(os.Stderr, msg)
			go gui.ShowError("RutStream", msg)
			err = fmt.Errorf("%s", msg)
		}
	}()

	if !gui.WebView2Installed() {
		gui.ShowError("RutStream — не найден WebView2", gui.WebView2Hint())
		return fmt.Errorf("WebView2 Runtime не установлен")
	}

	a, err := newApp()
	if err != nil {
		gui.ShowError("RutStream — ошибка запуска", err.Error())
		return err
	}
	defer a.close()

	server := gui.New(gui.Options{
		Client:       a.client,
		Engine:       a.engine,
		Store:        a.store,
		DataDir:      a.dataDir,
		Logger:       slog.Default(),
		BrowserReady: a.browserReady,
		SaveSession:  a.setSessionFromGUI,
		ClearSession: a.clearSession,
	})
	url, err := server.Start(ctx)
	if err != nil {
		gui.ShowError("RutStream — ошибка запуска", err.Error())
		return err
	}
	defer server.Stop()
	slog.Info("интерфейс запущен", "url", url)

	w := webview2.NewWithOptions(webview2.WebViewOptions{
		Debug:     os.Getenv("RUTSTREAM_DEBUG") != "",
		AutoFocus: true,
		DataPath:  filepath.Join(a.dataDir, "webview2"),
		WindowOptions: webview2.WindowOptions{
			Title:  "RutStream — музыка с rutracker",
			Width:  1280,
			Height: 820,
			Center: true,
		},
	})
	if w == nil {
		gui.ShowError("RutStream — окно не создано",
			"WebView2 Runtime установлен, но окно создать не удалось.\n\n"+
				"Попробуйте перезапустить приложение. Если ошибка повторяется, "+
				"переустановите WebView2 Runtime.")
		return fmt.Errorf("не удалось создать окно WebView2")
	}
	w.Navigate(url)

	// Закрываем приложение по Ctrl+C и по закрытию окна.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sig)
	go func() {
		<-sig
		server.Stop()
		defer func() { _ = recover() }()
		w.Dispatch(func() { w.Destroy() })
	}()

	w.Run()
	return nil
}
