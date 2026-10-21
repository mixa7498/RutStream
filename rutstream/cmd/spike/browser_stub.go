//go:build !browser

package main

import (
	"context"
	"errors"

	"github.com/mixa7/rutstream/internal/rutracker"
)

// loginViaBrowser — заглушка: сборка без браузерного транспорта.
func loginViaBrowser(ctx context.Context, c *rutracker.Client, user, pass string) error {
	return errors.New("сборка без браузерного транспорта (пересоберите с -tags browser)")
}
