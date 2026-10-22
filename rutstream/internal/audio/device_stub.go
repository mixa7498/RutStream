//go:build nodevice

package audio

import (
	"fmt"
	"io"
	"os"
)

// playToDevice — заглушка для сборок без аудиоустройства
// (headless CI, Linux без ALSA). Сборка: go build -tags nodevice.
func playToDevice(pcm []byte, f Format) error {
	return fmt.Errorf("сборка без поддержки аудиоустройства (тег nodevice)")
}

// DeviceAvailable сообщает, доступен ли вывод в звуковую карту.
func DeviceAvailable() bool { return false }

// NewDeviceWriter — заглушка: звуковой карты в этой сборке нет.
func NewDeviceWriter(f Format) (io.WriteCloser, error) {
	return nil, fmt.Errorf("сборка без поддержки аудиоустройства (тег nodevice)")
}

// init — заглушка, чтобы пакет не требовал ничего лишнего.
func init() {
	_ = os.Stdout
}
