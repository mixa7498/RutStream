package sniff

import (
	"encoding/binary"
	"errors"
	"io"
	"strings"
)

// ErrNotArchive — формат не похож на ZIP.
var ErrNotArchive = errors.New("sniff: не ZIP-архив")

// ZipEntry — запись в ZIP-архиве с точными байтовыми границами данных.
type ZipEntry struct {
	Name          string
	Method        uint16
	CompressedSize int64
	UncompressedSize int64
	CRC32         uint32
	// DataOffset — смещение данных файла от начала архива.
	DataOffset int64
	// LocalHeaderOffset — смещение local file header.
	LocalHeaderOffset int64
	// IsAudio — расширение похоже на аудио.
	IsAudio bool
}

// ProbeZip разбирает ZIP-архив и возвращает список записей с их смещениями.
//
// Нужны два блока:
//   - End of Central Directory (хвост) — список записей и смещение central directory;
//   - Central Directory — имена, размеры, CRC и смещения local header;
//   - Local File Header — точное смещение данных (длина имени/extra может отличаться
//     от центральной записи).
func ProbeZip(r io.ReaderAt, size int64) ([]ZipEntry, error) {
	// 1) Ищем EOCD в последних 64 КБ (+22 байта заголовка).
	const eocdLen = 22
	const maxComment = 0xFFFF
	if size < eocdLen {
		return nil, ErrNotArchive
	}
	tailLen := int64(eocdLen + maxComment)
	if tailLen > size {
		tailLen = size
	}
	tail := make([]byte, tailLen)
	if _, err := r.ReadAt(tail, size-tailLen); err != nil && err != io.EOF {
		return nil, err
	}
	eocdOff := int64(-1)
	for i := len(tail) - eocdLen; i >= 0; i-- {
		if binary.LittleEndian.Uint32(tail[i:i+4]) == 0x06054b50 {
			eocdOff = int64(i)
			break
		}
	}
	if eocdOff < 0 {
		return nil, ErrNotArchive
	}
	eocd := tail[eocdOff:]
	cdEntries := int64(binary.LittleEndian.Uint16(eocd[10:12]))
	cdSize := int64(binary.LittleEndian.Uint32(eocd[12:16]))
	cdOffset := int64(binary.LittleEndian.Uint32(eocd[16:20]))
	if cdOffset == 0xFFFFFFFF || cdSize == 0xFFFFFFFF {
		// ZIP64 — читаем из ZIP64 EOCD.
		z64, err := probeZip64(r, size, eocdOff)
		if err != nil {
			return nil, err
		}
		cdEntries, cdSize, cdOffset = z64.entries, z64.size, z64.offset
	}
	if cdOffset < 0 || cdOffset > size {
		return nil, ErrNotArchive
	}

	// 2) Читаем central directory.
	cd := make([]byte, cdSize)
	if _, err := r.ReadAt(cd, cdOffset); err != nil && err != io.EOF {
		return nil, err
	}
	var entries []ZipEntry
	pos := 0
	for i := int64(0); i < cdEntries && pos+46 <= len(cd); i++ {
		if binary.LittleEndian.Uint32(cd[pos:pos+4]) != 0x02014b50 {
			break
		}
		method := binary.LittleEndian.Uint16(cd[pos+10:pos+12])
		crc := binary.LittleEndian.Uint32(cd[pos+16:pos+20])
		csize := int64(binary.LittleEndian.Uint32(cd[pos+20:pos+24]))
		usize := int64(binary.LittleEndian.Uint32(cd[pos+24:pos+28]))
		nameLen := int64(binary.LittleEndian.Uint16(cd[pos+28:pos+30]))
		extraLen := int64(binary.LittleEndian.Uint16(cd[pos+30:pos+32]))
		commentLen := int64(binary.LittleEndian.Uint16(cd[pos+32:pos+34]))
		localOff := int64(binary.LittleEndian.Uint32(cd[pos+42:pos+46]))
		if int64(pos)+46+nameLen > int64(len(cd)) {
			break
		}
		name := string(cd[pos+46 : pos+46+int(nameLen)])
		entries = append(entries, ZipEntry{
			Name:              name,
			Method:            method,
			CompressedSize:    csize,
			UncompressedSize:  usize,
			CRC32:             crc,
			LocalHeaderOffset: localOff,
			IsAudio:           looksLikeAudio(name),
		})
		pos += 46 + int(nameLen+extraLen+commentLen)
	}
	if len(entries) == 0 {
		return nil, ErrNotArchive
	}

	// 3) Уточняем смещение данных по local file header.
	for i := range entries {
		e := &entries[i]
		if e.LocalHeaderOffset+30 > size {
			continue
		}
		lh := make([]byte, 30)
		if _, err := r.ReadAt(lh, e.LocalHeaderOffset); err != nil {
			continue
		}
		if binary.LittleEndian.Uint32(lh[0:4]) != 0x04034b50 {
			continue
		}
		nameLen := int64(binary.LittleEndian.Uint16(lh[26:28]))
		extraLen := int64(binary.LittleEndian.Uint16(lh[28:30]))
		e.DataOffset = e.LocalHeaderOffset + 30 + nameLen + extraLen
	}
	// Порядок записей сохраняем: в архиве он соответствует порядку треков альбома.
	return entries, nil
}

type zip64Info struct {
	entries int64
	size    int64
	offset  int64
}

// probeZip64 читает ZIP64 End of Central Directory.
func probeZip64(r io.ReaderAt, size int64, eocdOff int64) (zip64Info, error) {
	// Ищем ZIP64 EOCD locator (20 байт) перед EOCD.
	if eocdOff < 20 {
		return zip64Info{}, ErrNotArchive
	}
	loc := make([]byte, 20)
	if _, err := r.ReadAt(loc, eocdOff-20); err != nil {
		return zip64Info{}, err
	}
	if binary.LittleEndian.Uint32(loc[0:4]) != 0x07064b50 {
		return zip64Info{}, ErrNotArchive
	}
	z64Off := int64(binary.LittleEndian.Uint64(loc[8:16]))
	if z64Off+56 > size {
		return zip64Info{}, ErrNotArchive
	}
	hdr := make([]byte, 56)
	if _, err := r.ReadAt(hdr, z64Off); err != nil {
		return zip64Info{}, err
	}
	if binary.LittleEndian.Uint32(hdr[0:4]) != 0x06064b50 {
		return zip64Info{}, ErrNotArchive
	}
	return zip64Info{
		entries: int64(binary.LittleEndian.Uint64(hdr[32:40])),
		size:    int64(binary.LittleEndian.Uint64(hdr[40:48])),
		offset:  int64(binary.LittleEndian.Uint64(hdr[48:56])),
	}, nil
}

// looksLikeAudio определяет, похоже ли имя файла на аудио.
func looksLikeAudio(name string) bool {
	lower := strings.ToLower(name)
	for _, ext := range []string{".flac", ".mp3", ".m4a", ".ape", ".wv", ".ogg", ".opus", ".wav", ".aiff", ".alac", ".tak", ".dsf", ".dff"} {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}
