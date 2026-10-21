// Package sniff разбирает заголовки аудиоконтейнеров, чтобы построить карту треков
// внутри одного файла, не скачивая его целиком.
package sniff

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Track — трек внутри одного контейнера.
type Track struct {
	Index       int
	Title       string
	Offset      int64 // смещение в байтах от начала аудиоданных файла
	DurationSec float64
	// Size — размер данных трека (для записей внутри архива).
	Size int64
	// Codec — кодек трека (для записей внутри архива).
	Codec string
	// Method — метод сжатия (для ZIP: 0 — без сжатия, 8 — deflate).
	Method uint16
}

// Result — результат разбора контейнера.
type Result struct {
	Format      string // "flac", "mp3", ...
	Codec       string
	Lossless    bool
	DurationSec float64
	SampleRate  int
	BitDepth    int
	Channels    int
	BitrateKbps int
	Tracks      []Track
	Tags        map[string]string
	HasCueSheet bool
}

// ErrUnsupported — формат не распознан.
var ErrUnsupported = errors.New("sniff: формат не распознан")

// Probe определяет формат по сигнатуре и разбирает метаданные.
func Probe(r io.ReaderAt, size int64) (*Result, error) {
	if r == nil || size <= 0 {
		return nil, ErrUnsupported
	}
	head := make([]byte, min64(size, 16))
	if _, err := r.ReadAt(head, 0); err != nil && err != io.EOF {
		return nil, err
	}
	switch {
	case len(head) >= 4 && string(head[0:4]) == "fLaC":
		return probeFLAC(r, size)
	case len(head) >= 3 && string(head[0:3]) == "ID3":
		return probeMP3(r, size)
	case len(head) >= 2 && head[0] == 0xFF && head[1]&0xE0 == 0xE0:
		return probeMP3(r, size)
	case len(head) >= 4 && string(head[0:4]) == "PK\x03\x04":
		return probeZipResult(r, size)
	}
	return nil, ErrUnsupported
}

// probeZipResult превращает список записей ZIP-архива в карту треков.
func probeZipResult(r io.ReaderAt, size int64) (*Result, error) {
	entries, err := ProbeZip(r, size)
	if err != nil {
		return nil, err
	}
	res := &Result{Format: "zip", Codec: "zip", Tags: map[string]string{}}
	for _, e := range entries {
		if !e.IsAudio {
			continue
		}
		codec := codecOfName(e.Name)
		res.Tracks = append(res.Tracks, Track{
			Index:  len(res.Tracks) + 1,
			Title:  titleFromName(e.Name),
			Offset: e.DataOffset,
			// Длительность неизвестна до распаковки — оцениваем по размеру.
			DurationSec: estimateDuration(e.UncompressedSize),
			Size:         e.UncompressedSize,
			Codec:        codec,
			Method:       e.Method,
		})
	}
	if len(res.Tracks) == 0 {
		return res, fmt.Errorf("%w: в архиве нет аудиофайлов", ErrUnsupported)
	}
	return res, nil
}

// codecOfName определяет кодек по расширению имени файла.
func codecOfName(name string) string {
	lower := strings.ToLower(name)
	switch {
	case strings.HasSuffix(lower, ".flac"):
		return "flac"
	case strings.HasSuffix(lower, ".mp3"):
		return "mp3"
	case strings.HasSuffix(lower, ".m4a"), strings.HasSuffix(lower, ".alac"):
		return "aac"
	case strings.HasSuffix(lower, ".ape"):
		return "ape"
	case strings.HasSuffix(lower, ".wv"):
		return "wavpack"
	case strings.HasSuffix(lower, ".ogg"), strings.HasSuffix(lower, ".oga"):
		return "ogg"
	case strings.HasSuffix(lower, ".opus"):
		return "opus"
	case strings.HasSuffix(lower, ".wav"):
		return "wav"
	}
	return "unknown"
}

// titleFromName убирает путь и расширение из имени файла.
func titleFromName(p string) string {
	name := p
	if i := strings.LastIndexAny(name, "/\\"); i >= 0 {
		name = name[i+1:]
	}
	if i := strings.LastIndex(name, "."); i > 0 {
		name = name[:i]
	}
	return name
}

// estimateDuration грубо оценивает длительность по размеру (lossless ≈ 0.8 МБ на минуту).
func estimateDuration(size int64) float64 {
	if size <= 0 {
		return 0
	}
	return float64(size) / (0.8 * 1024 * 1024) * 60
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

// ---------------------------------------------------------------- FLAC -----

// probeFLAC разбирает блоки метаданных FLAC: STREAMINFO, VORBIS_COMMENT, CUESHEET.
func probeFLAC(r io.ReaderAt, size int64) (*Result, error) {
	res := &Result{
		Format:   "flac",
		Codec:    "flac",
		Lossless: true,
		Tags:     map[string]string{},
	}
	buf := make([]byte, 4)
	if _, err := r.ReadAt(buf, 4); err != nil {
		return res, err
	}
	pos := int64(8)
	for {
		if pos+4 > size {
			break
		}
		hdr := make([]byte, 4)
		if _, err := r.ReadAt(hdr, pos); err != nil {
			break
		}
		last := hdr[0]&0x80 != 0
		blockType := hdr[0] & 0x7F
		length := int64(hdr[1])<<16 | int64(hdr[2])<<8 | int64(hdr[3])
		body := make([]byte, length)
		if pos+4+length > size {
			break
		}
		if _, err := r.ReadAt(body, pos+4); err != nil {
			break
		}
		switch blockType {
		case 0: // STREAMINFO
			parseStreamInfo(body, res)
		case 4: // VORBIS_COMMENT
			parseVorbisComment(body, res.Tags)
		case 5: // CUESHEET
			parseCueSheet(body, res)
		}
		pos += 4 + length
		if last {
			break
		}
	}
	if res.SampleRate > 0 && res.DurationSec == 0 {
		if total, ok := res.Tags["TOTAL_SAMPLES"]; ok {
			var n int64
			if _, err := fmt.Sscanf(total, "%d", &n); err == nil {
				res.DurationSec = float64(n) / float64(res.SampleRate)
			}
		}
	}
	return res, nil
}

func parseStreamInfo(b []byte, res *Result) {
	if len(b) < 34 {
		return
	}
	// биты 0..27: min/max block size, min/max frame size
	// биты 28..63: sample rate (20), channels (3), bps (5), total samples (36)
	v := uint64(b[10])<<56 | uint64(b[11])<<48 | uint64(b[12])<<40 | uint64(b[13])<<32 |
		uint64(b[14])<<24 | uint64(b[15])<<16 | uint64(b[16])<<8 | uint64(b[17])
	sampleRate := int((v >> 44) & 0xFFFFF)
	channels := int((v >> 41) & 0x7) + 1
	bitsPerSample := int((v >> 36) & 0x1F) + 1
	totalSamples := int64(v & 0xFFFFFFFFF)
	res.SampleRate = sampleRate
	res.Channels = channels
	res.BitDepth = bitsPerSample
	if sampleRate > 0 && totalSamples > 0 {
		res.DurationSec = float64(totalSamples) / float64(sampleRate)
	}
}

func parseVorbisComment(b []byte, tags map[string]string) {
	if len(b) < 4 {
		return
	}
	pos := 0
	vendorLen := int(binary.LittleEndian.Uint32(b[pos:]))
	pos += 4
	if pos+vendorLen > len(b) {
		return
	}
	pos += vendorLen
	if pos+4 > len(b) {
		return
	}
	count := int(binary.LittleEndian.Uint32(b[pos:]))
	pos += 4
	for i := 0; i < count && pos+4 <= len(b); i++ {
		l := int(binary.LittleEndian.Uint32(b[pos:]))
		pos += 4
		if pos+l > len(b) {
			return
		}
		s := string(b[pos : pos+l])
		pos += l
		if eq := indexByte(s, '='); eq > 0 {
			tags[upper(s[:eq])] = s[eq+1:]
		}
	}
}

func indexByte(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}

func upper(s string) string {
	b := []byte(s)
	for i := range b {
		if b[i] >= 'a' && b[i] <= 'z' {
			b[i] -= 32
		}
	}
	return string(b)
}

// parseCueSheet разбирает CUESHEET: список треков с точными смещениями в сэмплах.
func parseCueSheet(b []byte, res *Result) {
	if len(b) < 396 {
		return
	}
	pos := 128 // media catalog number
	pos += 8    // lead-in
	// байт: is_cd (1 бит) + reserved (7 бит) + num tracks (8 бит)
	numTracks := int(b[pos+1])
	pos += 2
	res.HasCueSheet = true
	var tracks []Track
	for i := 0; i < numTracks && pos+36 <= len(b); i++ {
		offset := int64(binary.BigEndian.Uint64(b[pos:]))
		pos += 8
		pos += 8 // number + isrc(12)
		idxCount := int(b[pos])
		pos++
		// пропускаем index points
		pos += idxCount * 12
		tracks = append(tracks, Track{Index: i + 1, Offset: offset})
	}
	// lead-out
	if pos+8 <= len(b) {
		leadOut := int64(binary.BigEndian.Uint64(b[pos:]))
		if len(tracks) > 0 && res.SampleRate > 0 {
			for i := range tracks {
				next := leadOut
				if i+1 < len(tracks) {
					next = tracks[i+1].Offset
				}
				samples := next - tracks[i].Offset
				if samples < 0 {
					samples = 0
				}
				tracks[i].DurationSec = float64(samples) / float64(res.SampleRate)
			}
		}
	}
	res.Tracks = tracks
}

// ---------------------------------------------------------------- MP3 ------

// probeMP3 разбирает ID3v2 и MPEG-фреймы: длительность, битрейт, теги.
func probeMP3(r io.ReaderAt, size int64) (*Result, error) {
	res := &Result{
		Format: "mp3",
		Codec:  "mp3",
		Tags:   map[string]string{},
	}
	pos := int64(0)
	// ID3v2
	head := make([]byte, 10)
	if _, err := r.ReadAt(head, 0); err == nil && string(head[0:3]) == "ID3" {
		flags := head[5]
		tagSize := int64(syncSafe(head[6:10]))
		pos = 10 + tagSize
		if flags&0x10 != 0 {
			pos += 10 // footer
		}
		if pos > size {
			pos = 10
		}
		res.Tags = parseID3v2(r, tagSize)
	}

	// Первый MPEG-фрейм
	frame := make([]byte, 4)
	if _, err := r.ReadAt(frame, pos); err != nil {
		return res, err
	}
	if frame[0] != 0xFF || frame[1]&0xE0 != 0xE0 {
		return res, fmt.Errorf("sniff: mpeg sync не найден")
	}
	version := int((frame[1] >> 3) & 0x03) // 3=MPEG1, 2=MPEG2, 0=MPEG2.5
	layer := int((frame[1] >> 1) & 0x03)   // 1=LayerIII
	bitrateIdx := int((frame[2] >> 4) & 0x0F)
	sampleRateIdx := int((frame[2] >> 2) & 0x03)
	padding := int((frame[2] >> 1) & 0x01)

	sampleRates := map[int][3]int{
		3: {44100, 48000, 32000}, // MPEG1
		2: {22050, 24000, 16000}, // MPEG2
		0: {11025, 12000, 8000},  // MPEG2.5
	}
	if version == 1 {
		return res, fmt.Errorf("sniff: mpeg version 1 не поддерживается")
	}
	rate := sampleRates[version][sampleRateIdx]
	if rate == 0 {
		return res, fmt.Errorf("sniff: неверный sample rate index")
	}
	res.SampleRate = rate

	bitrates := map[int][15]int{
		// MPEG1 Layer III
		3: {0, 32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320},
		// MPEG2/2.5 Layer III
		2: {0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160},
	}
	_ = layer
	bitrate := 0
	if version == 3 {
		bitrate = bitrates[3][bitrateIdx]
	} else {
		bitrate = bitrates[2][bitrateIdx]
	}
	if bitrate == 0 {
		return res, fmt.Errorf("sniff: неверный bitrate index")
	}
	res.BitrateKbps = bitrate

	// Xing/Info (VBR) — ищем сразу после заголовка фрейма + side info.
	sideInfo := 17
	if version == 3 && frame[3]&0x20 != 0 { // MPEG1 stereo
		sideInfo = 32
	}
	xingPos := pos + 4 + int64(sideInfo)
	xing := make([]byte, 8)
	if _, err := r.ReadAt(xing, xingPos); err == nil {
		magic := string(xing[0:4])
		if magic == "Xing" || magic == "Info" {
			flags := binary.BigEndian.Uint32(xing[4:8])
			off := xingPos + 8
			var frames, byteCount uint32
			if flags&0x01 != 0 {
				buf := make([]byte, 4)
				if _, err := r.ReadAt(buf, off); err == nil {
					frames = binary.BigEndian.Uint32(buf)
					off += 4
				}
			}
			if flags&0x02 != 0 {
				buf := make([]byte, 4)
				if _, err := r.ReadAt(buf, off); err == nil {
					byteCount = binary.BigEndian.Uint32(buf)
					off += 4
				}
			}
			if frames > 0 {
				samplesPerFrame := 1152
				if version != 3 {
					samplesPerFrame = 576
				}
				res.DurationSec = float64(frames) * float64(samplesPerFrame) / float64(rate)
				res.Tags["VBR"] = "1"
			}
			if byteCount > 0 && res.DurationSec > 0 {
				res.BitrateKbps = int(float64(byteCount) * 8 / res.DurationSec / 1000)
			}
		}
	}
	// VBRI (Fraunhofer)
	vbri := make([]byte, 4)
	if _, err := r.ReadAt(vbri, pos+4+32); err == nil && string(vbri[0:4]) == "VBRI" {
		buf := make([]byte, 16)
		if _, err := r.ReadAt(buf, pos+4+32); err == nil {
			byteCount := binary.BigEndian.Uint32(buf[10:14])
			frames := binary.BigEndian.Uint32(buf[14:18])
			if frames > 0 {
				samplesPerFrame := 1152
				if version != 3 {
					samplesPerFrame = 576
				}
				res.DurationSec = float64(frames) * float64(samplesPerFrame) / float64(rate)
			}
			if byteCount > 0 && res.DurationSec > 0 {
				res.BitrateKbps = int(float64(byteCount) * 8 / res.DurationSec / 1000)
			}
		}
	}
	// CBR-оценка
	if res.DurationSec == 0 {
		audioBytes := size - pos
		if audioBytes > 0 {
			res.DurationSec = float64(audioBytes) * 8 / float64(bitrate*1000)
		}
	}
	_ = padding

	title := res.Tags["TITLE"]
	if title == "" {
		title = "track"
	}
	res.Tracks = []Track{{Index: 1, Title: title, Offset: 0, DurationSec: res.DurationSec}}
	return res, nil
}

func syncSafe(b []byte) uint32 {
	return uint32(b[0]&0x7F)<<21 | uint32(b[1]&0x7F)<<14 | uint32(b[2]&0x7F)<<7 | uint32(b[3]&0x7F)
}

// parseID3v2 читает основные текстовые фреймы (TIT2, TPE1, TALB, TCON, TRCK, TYER).
func parseID3v2(r io.ReaderAt, tagSize int64) map[string]string {
	tags := map[string]string{}
	if tagSize <= 10 || tagSize > 8<<20 {
		return tags
	}
	buf := make([]byte, tagSize)
	if _, err := r.ReadAt(buf, 10); err != nil {
		return tags
	}
	ver := buf[3]
	pos := int64(0)
	for pos+10 <= tagSize {
		id := string(buf[pos : pos+4])
		if id == "\x00\x00\x00\x00" {
			break
		}
		size := int64(0)
		if ver == 4 {
			size = int64(syncSafe(buf[pos+4 : pos+8]))
		} else {
			size = int64(binary.BigEndian.Uint32(buf[pos+4 : pos+8]))
		}
		pos += 10
		if size <= 0 || pos+size > tagSize {
			break
		}
		body := buf[pos : pos+size]
		pos += size
		val := decodeID3Text(body)
		switch id {
		case "TIT2":
			tags["TITLE"] = val
		case "TPE1":
			tags["ARTIST"] = val
		case "TPE2":
			tags["ALBUMARTIST"] = val
		case "TALB":
			tags["ALBUM"] = val
		case "TCON":
			tags["GENRE"] = val
		case "TRCK":
			tags["TRACK"] = val
		case "TYER", "TDRC":
			tags["YEAR"] = val
		case "TXXX":
			// пары "KEY=value"
			if eq := indexByte(val, '='); eq > 0 {
				tags[upper(val[:eq])] = val[eq+1:]
			}
		}
	}
	return tags
}

func decodeID3Text(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	enc := b[0]
	body := b[1:]
	switch enc {
	case 0: // ISO-8859-1
		return string(body)
	case 1: // UTF-16 с BOM
		return decodeUTF16(body)
	case 2: // UTF-16BE
		return decodeUTF16BE(body)
	case 3: // UTF-8
		return string(body)
	}
	return string(body)
}

func decodeUTF16(b []byte) string {
	if len(b) >= 2 && b[0] == 0xFF && b[1] == 0xFE {
		return decodeUTF16LE(b[2:])
	}
	if len(b) >= 2 && b[0] == 0xFE && b[1] == 0xFF {
		return decodeUTF16BE(b[2:])
	}
	return decodeUTF16LE(b)
}

func decodeUTF16LE(b []byte) string {
	u := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		u = append(u, binary.LittleEndian.Uint16(b[i:i+2]))
	}
	return string(utf16Decode(u))
}

func decodeUTF16BE(b []byte) string {
	u := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		u = append(u, binary.BigEndian.Uint16(b[i:i+2]))
	}
	return string(utf16Decode(u))
}

func utf16Decode(u []uint16) []rune {
	out := make([]rune, 0, len(u))
	for _, c := range u {
		out = append(out, rune(c))
	}
	return out
}
