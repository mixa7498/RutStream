//go:build !nodevice

package audio

import (
	"fmt"

	"github.com/hajimehoshi/oto"
)

// playToDevice воспроизводит PCM через системное устройство.
func playToDevice(pcm []byte, f Format) error {
	if f.SampleRate <= 0 || f.Channels <= 0 {
		return fmt.Errorf("некорректный формат потока")
	}
	ctx, err := oto.NewContext(f.SampleRate, f.Channels, 2, 4096)
	if err != nil {
		return err
	}
	defer ctx.Close()
	player := ctx.NewPlayer()
	defer player.Close()
	if _, err := player.Write(pcm); err != nil {
		return err
	}
	return nil
}

// DeviceAvailable сообщает, доступен ли вывод в звуковую карту.
func DeviceAvailable() bool {
	f := Format{SampleRate: 44100, Channels: 2}
	ctx, err := oto.NewContext(f.SampleRate, f.Channels, 2, 4096)
	if err != nil {
		return false
	}
	ctx.Close()
	return true
}
