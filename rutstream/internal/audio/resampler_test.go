//go:build !nodevice

package audio

import (
	"encoding/binary"
	"math"
	"testing"
)

// TestResamplerSameRate — при одинаковой частоте ресемплинг не должен
// менять сигнал (разве что погрешность округления).
func TestResamplerSameRate(t *testing.T) {
	const rate = 44100
	const n = 1000
	in := make([]byte, n*4)
	for i := 0; i < n; i++ {
		v := int16((i * 300) % 20000)
		binary.LittleEndian.PutUint16(in[i*4:], uint16(v))
		binary.LittleEndian.PutUint16(in[i*4+2:], uint16(v))
	}
	r := newResampler(rate, 2)
	out := r.Convert(in)
	// Первый сэмпл интерполируется «вхолостую», дальше — один в один.
	if len(out) < (n-1)*4 {
		t.Fatalf("вышло %d байт, ожидалось ~%d", len(out), (n-1)*4)
	}
	// Сравниваем второй сэмпл: должен совпасть с исходным.
	got := int16(binary.LittleEndian.Uint16(out[4:]))
	want := int16(binary.LittleEndian.Uint16(in[4:]))
	if got != want {
		t.Errorf("сэмпл 1 = %d, ожидалось %d", got, want)
	}
}

// TestResamplerDownmix — моно превращается в стерео с одинаковыми каналами.
func TestResamplerDownmix(t *testing.T) {
	const rate = 44100
	in := make([]byte, 100*2) // моно
	for i := 0; i < 100; i++ {
		binary.LittleEndian.PutUint16(in[i*2:], uint16(1000+i))
	}
	r := newResampler(rate, 1)
	out := r.Convert(in)
	if len(out) == 0 {
		t.Fatal("пустой результат")
	}
	if len(out)%4 != 0 {
		t.Fatalf("размер %d не кратен 4 (стерео-фрейм)", len(out))
	}
	for i := 0; i+3 < len(out); i += 4 {
		l := int16(binary.LittleEndian.Uint16(out[i:]))
		rr := int16(binary.LittleEndian.Uint16(out[i+2:]))
		if l != rr {
			t.Errorf("каналы различаются на фрейме %d: %d и %d", i/4, l, rr)
			break
		}
	}
}

// TestResamplerUpsample — при повышении частоты число сэмплов растёт
// пропорционально, сигнал остаётся в допустимых пределах.
func TestResamplerUpsample(t *testing.T) {
	const inRate = 22050
	const n = 1000
	in := make([]byte, n*4)
	for i := 0; i < n; i++ {
		v := int16(math.Sin(float64(i)/10) * 10000)
		binary.LittleEndian.PutUint16(in[i*4:], uint16(v))
		binary.LittleEndian.PutUint16(in[i*4+2:], uint16(v))
	}
	r := newResampler(inRate, 2)
	out := r.Convert(in)
	frames := len(out) / 4
	if frames < n || frames > n*3 {
		t.Errorf("получено %d сэмплов из %d — ожидалось примерно вдвое больше", frames, n)
	}
	for i := 0; i+1 < len(out); i += 2 {
		v := int16(binary.LittleEndian.Uint16(out[i:]))
		if v > 11000 || v < -11000 {
			t.Errorf("сэмпл %d = %d — выход за пределы сигнала", i/2, v)
			break
		}
	}
}
