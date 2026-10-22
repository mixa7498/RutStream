//go:build !windows

package main

import (
	"context"
	"fmt"
)

// cmdGUI на не-Windows недоступна: там нет WebView2.
func cmdGUI(ctx context.Context, args []string) error {
	return fmt.Errorf("окно приложения доступно только в Windows; используйте консольные команды")
}
