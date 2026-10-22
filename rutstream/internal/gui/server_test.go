package gui

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestServerAssets — встроенные ресурсы интерфейса отдаются,
// а API отвечает JSON'ом.
func TestServerAssets(t *testing.T) {
	srv := New(Options{})
	url, err := srv.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	// Главная страница.
	resp, err := http.Get(url + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("статус главной = %d", resp.StatusCode)
	}
	if !strings.Contains(string(body), "RutStream") {
		t.Errorf("главная страница без разметки: %q", string(body[:min(120, len(body))]))
	}

	// Стили и скрипт.
	for _, path := range []string{"/style.css", "/app.js"} {
		r, err := http.Get(url + path)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(r.Body)
		r.Body.Close()
		if r.StatusCode != 200 || len(data) == 0 {
			t.Errorf("%s: статус %d, %d байт", path, r.StatusCode, len(data))
		}
	}

	// Состояние — JSON с ожидаемыми полями.
	r, err := http.Get(url + "/api/state")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	var state struct {
		LoggedIn bool `json:"loggedIn"`
		HasDevice bool `json:"hasDevice"`
		Downloads []struct {
			ID string `json:"id"`
		} `json:"downloads"`
	}
	if err := json.NewDecoder(r.Body).Decode(&state); err != nil {
		t.Fatalf("разбор ответа состояния: %v", err)
	}
	if state.LoggedIn {
		t.Error("без клиента сессия не должна быть активной")
	}

	// Неизвестный раздел API — 404.
	r2, err := http.Get(url + "/api/nope")
	if err != nil {
		t.Fatal(err)
	}
	r2.Body.Close()
	if r2.StatusCode != http.StatusNotFound {
		t.Errorf("статус неизвестного метода = %d, ожидалось 404", r2.StatusCode)
	}
}

// TestServerStop — после Stop сервер не отвечает.
func TestServerStop(t *testing.T) {
	srv := New(Options{})
	url, err := srv.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	srv.Stop()
	time.Sleep(200 * time.Millisecond)
	if _, err := http.Get(url + "/api/state"); err == nil {
		t.Error("сервер продолжает отвечать после Stop")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
