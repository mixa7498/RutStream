//go:build windows

package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/jchv/go-webview2"
	"github.com/mixa7/rutstream/internal/gui"
)

// cmdGUI запускает приложение с окном.
func cmdGUI(ctx context.Context, args []string) error {
	a, err := newApp()
	if err != nil {
		return err
	}
	defer a.close()

	server := gui.New(gui.Options{
		Client:  a.client,
		Engine:  a.engine,
		Store:   a.store,
		DataDir: a.dataDir,
		Logger:  slog.Default(),
	})
	url, err := server.Start(ctx)
	if err != nil {
		return err
	}
	defer server.Stop()
	fmt.Println("RutStream:", url)
	fmt.Println("Закройте окно, чтобы выйти.")

	w := webview2.NewWithOptions(webview2.WebViewOptions{
		Debug:     true,
		AutoFocus: true,
		WindowOptions: webview2.WindowOptions{
			Title:  "RutStream — музыка с rutracker",
			Width:  1280,
			Height: 820,
			Center: true,
		},
	})
	w.Navigate(url)

	// Закрываем приложение по Ctrl+C и по закрытию окна.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sig
		server.Stop()
		w.Dispatch(func() {
			w.Destroy()
		})
	}()
	w.Run()
	return nil
}
