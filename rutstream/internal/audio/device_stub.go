//go:build nodevice

package audio

import (
	"fmt"
	"os"
)

// playToDevice — заглушка для сборок без аудиоустройства
// (headless CI, Linux без ALSA). Сборка: go build -tags nodevice.
func playToDevice(pcm []byte, f Format) error {
	return fmt.Errorf("сборка без поддержки аудиоустройства (тег nodevice)")
}

// DeviceAvailable сообщает, доступен ли вывод в звуковую карту.
func DeviceAvailable() bool { return false }

// init — заглушка, чтобы пакет не требовал ничего лишнего.
func init() {
	_ = os.Stdout
}
