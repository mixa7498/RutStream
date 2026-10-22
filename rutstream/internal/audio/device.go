//go:build !nodevice

package audio

import (
	"fmt"
	"io"
	"sync"

	"github.com/hajimehoshi/oto"
)

// Формат звукового устройства: CD-стандарт, к которому приводится любой поток.
const (
	deviceRate     = 44100
	deviceChannels = 2
)

var (
	devMu     sync.Mutex
	devCtx    *oto.Context
	devPlayer *oto.Player
	devErr    error
	devReady  bool
)

// device возвращает общий контекст и проигрыватель.
//
// Важно: oto разрешает создать контекст только ОДИН раз за процесс
// (иначе паника «NewContext can be called only once»), поэтому контекст
// общий для всего приложения, а проигрыватель переиспользуется.
func device() (*oto.Context, *oto.Player, error) {
	devMu.Lock()
	defer devMu.Unlock()
	if !devReady {
		devReady = true
		ctx, err := oto.NewContext(deviceRate, deviceChannels, 2, 4096)
		if err != nil {
			devErr = err
			return nil, nil, err
		}
		devCtx = ctx
	}
	if devErr != nil {
		return nil, nil, devErr
	}
	if devPlayer == nil {
		devPlayer = devCtx.NewPlayer()
	}
	return devCtx, devPlayer, nil
}

// playToDevice воспроизводит PCM через системное устройство.
func playToDevice(pcm []byte, f Format) error {
	_, player, err := device()
	if err != nil {
		return err
	}
	if _, err := player.Write(pcm); err != nil {
		return err
	}
	return nil
}

// DeviceAvailable сообщает, доступен ли вывод в звуковую карту.
func DeviceAvailable() bool {
	_, _, err := device()
	return err == nil
}

// NewDeviceWriter возвращает writer, пишущий PCM в звуковую карту.
//
// Поток приводится к формату устройства (44100 Гц, стерео) простым
// ресемплингом, поэтому подходит любой исходный формат.
func NewDeviceWriter(f Format) (io.WriteCloser, error) {
	if f.SampleRate <= 0 || f.Channels <= 0 {
		return nil, fmt.Errorf("некорректный формат потока")
	}
	_, player, err := device()
	if err != nil {
		return nil, err
	}
	return &deviceWriter{
		player: player,
		res:    newResampler(f.SampleRate, f.Channels),
	}, nil
}

// deviceWriter — вывод PCM в звуковую карту с приведением формата.
type deviceWriter struct {
	player *oto.Player
	res    *pcmResampler
}

func (d *deviceWriter) Write(p []byte) (int, error) {
	out := d.res.Convert(p)
	if len(out) > 0 {
		if _, err := d.player.Write(out); err != nil {
			return 0, err
		}
	}
	return len(p), nil
}

func (d *deviceWriter) Close() error {
	// Дописать остаток, если он есть.
	if out := d.res.Flush(); len(out) > 0 {
		_, _ = d.player.Write(out)
	}
	return nil
}

// pcmResampler приводит PCM (s16le) к частоте и числу каналов устройства.
//
// Преобразование — линейная интерполяция по времени и раздельные каналы
// (моно дублируется в оба).
type pcmResampler struct {
	inRate     int
	inChannels int
	outRate    int
	outCh      int

	step float64
	pos  float64

	prev     [2]float64
	havePrev bool
	seen     int

	pending []byte
	out     []byte
}

// newResampler создаёт преобразователь в формат устройства.
func newResampler(inRate, inChannels int) *pcmResampler {
	return &pcmResampler{
		inRate:     inRate,
		inChannels: inChannels,
		outRate:    deviceRate,
		outCh:      deviceChannels,
		step:       float64(inRate) / float64(deviceRate),
	}
}

// Convert переводит очередной кусок PCM (s16le) в формат устройства.
func (r *pcmResampler) Convert(p []byte) []byte {
	src := p
	if len(r.pending) > 0 {
		src = append(r.pending, p...)
		r.pending = nil
	}
	frameBytes := r.inChannels * 2
	for off := 0; off+frameBytes <= len(src); off += frameBytes {
		left := float64(int16(src[off]) | int16(src[off+1])<<8)
		right := left
		if r.inChannels >= 2 {
			right = float64(int16(src[off+2]) | int16(src[off+3])<<8)
		}
		// Текущий сэмпл имеет индекс seen, предыдущий — seen-1.
		for r.pos < float64(r.seen) {
			l, rr := left, right
			if r.havePrev {
				frac := r.pos - float64(r.seen-1)
				l = r.prev[0] + (left-r.prev[0])*frac
				rr = r.prev[1] + (right-r.prev[1])*frac
			}
			r.out = appendInt16(r.out, int16(l))
			if r.outCh >= 2 {
				r.out = appendInt16(r.out, int16(rr))
			}
			r.pos += r.step
		}
		r.prev[0], r.prev[1] = left, right
		r.havePrev = true
		r.seen++
	}
	if rem := len(src) % frameBytes; rem > 0 {
		r.pending = append([]byte{}, src[len(src)-rem:]...)
	}
	return r.out
}

// Flush возвращает накопленные сэмплы и очищает буфер.
func (r *pcmResampler) Flush() []byte {
	out := r.out
	r.out = nil
	return out
}

// appendInt16 дописывает сэмпл в little-endian.
func appendInt16(b []byte, v int16) []byte {
	return append(b, byte(v), byte(v>>8))
}
