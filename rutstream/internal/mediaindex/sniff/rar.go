package sniff

import (
	"encoding/binary"
	"errors"
	"io"
)

// ErrNotRar — формат не похож на RAR.
var ErrNotRar = errors.New("sniff: не RAR-архив")

// RarEntry — запись в RAR-архиве.
type RarEntry struct {
	Name       string
	DataOffset int64
	PackedSize int64
	UnpackedSize int64
	Method     byte
	CRC32      uint32
	// Solid — файл зависит от данных предыдущих файлов (нужны они целиком).
	Solid bool
	// SplitBefore / SplitAfter — файл разбит на части.
	SplitBefore bool
	SplitAfter  bool
}

// RAR-маркеры.
var (
	rar4Marker = []byte{0x52, 0x61, 0x72, 0x21, 0x1A, 0x07, 0x00} // "Rar!\x1a\x07\x00"
	rar5Marker = []byte{0x52, 0x61, 0x72, 0x21, 0x1A, 0x07, 0x01, 0x00}
)

// Флаги блока RAR4.
const (
	rarLongBlock   = 0x8000
	rarLHDLarge    = 0x0100
	rarLHDUnicode  = 0x0200
	rarLHDSalt     = 0x0400
	rarLHDVersion  = 0x0800
	rarLHDExtTime  = 0x1000
	rarLHDSolid    = 0x0010
	rarLHDSplitBef = 0x0001
	rarLHDSplitAft = 0x0002
)

// ProbeRar разбирает RAR4-архив и возвращает записи с их смещениями.
//
// RAR5 пока не поддерживается: его заголовочки устроены иначе.
func ProbeRar(r io.ReaderAt, size int64) ([]RarEntry, error) {
	marker := make([]byte, 8)
	if _, err := r.ReadAt(marker, 0); err != nil {
		return nil, ErrNotRar
	}
	if len(marker) >= 7 && string(marker[:7]) == string(rar5Marker) {
		return nil, errors.New("sniff: RAR5 пока не поддерживается")
	}
	if len(marker) < 7 || string(marker[:7]) != string(rar4Marker) {
		return nil, ErrNotRar
	}

	var entries []RarEntry
	pos := int64(7)
	for pos+7 <= size {
		hdr := make([]byte, 7)
		if _, err := r.ReadAt(hdr, pos); err != nil {
			break
		}
		flags := binary.LittleEndian.Uint16(hdr[3:5])
		blockType := hdr[2]
		headerSize := int64(7)
		blockSize := int64(binary.LittleEndian.Uint16(hdr[5:7]))
		if flags&rarLongBlock != 0 {
			if pos+11 > size {
				break
			}
			ext := make([]byte, 4)
			if _, err := r.ReadAt(ext, pos+7); err != nil {
				break
			}
			blockSize = int64(binary.LittleEndian.Uint32(ext))
			headerSize = 11
		}
		if blockSize <= 0 || pos+blockSize > size {
			break
		}
		if blockType == 0x74 { // FILE_HEAD
			e, dataOff, ok := parseRar4FileHead(r, pos, headerSize, flags)
			if ok {
				e.DataOffset = dataOff
				entries = append(entries, e)
			}
		}
		pos += blockSize
	}
	if len(entries) == 0 {
		return nil, ErrNotRar
	}
	return entries, nil
}

// parseRar4FileHead разбирает заголовок файла RAR4 и возвращает запись
// и смещение начала данных.
func parseRar4FileHead(r io.ReaderAt, pos, headerSize int64, blockFlags uint16) (RarEntry, int64, bool) {
	body := make([]byte, 7)
	if _, err := r.ReadAt(body, pos+headerSize); err != nil {
		return RarEntry{}, 0, false
	}
	packSize := int64(binary.LittleEndian.Uint32(body[0:4]))
	unpSize := int64(binary.LittleEndian.Uint32(body[4:8]))
	hostOS := body[8]
	crc := binary.LittleEndian.Uint32(body[9:13])
	ftime := binary.LittleEndian.Uint32(body[13:17])
	unpVer := body[17]
	method := body[18]
	nameSize := int64(binary.LittleEndian.Uint16(body[19:21]))

	e := RarEntry{
		PackedSize:   packSize,
		UnpackedSize: unpSize,
		Method:       method,
		CRC32:        crc,
		Solid:        blockFlags&rarLHDSolid != 0,
		SplitBefore:  blockFlags&rarLHDSplitBef != 0,
		SplitAfter:   blockFlags&rarLHDSplitAft != 0,
	}
	_ = hostOS
	_ = ftime
	_ = unpVer

	off := pos + headerSize + 21
	// Большие размеры (LHD_LARGE).
	if blockFlags&rarLHDLarge != 0 {
		ext := make([]byte, 16)
		if _, err := r.ReadAt(ext, off); err != nil {
			return e, 0, false
		}
		highPack := int64(binary.LittleEndian.Uint32(ext[0:4]))
		highUnp := int64(binary.LittleEndian.Uint32(ext[12:16]))
		e.PackedSize |= highPack << 32
		e.UnpackedSize |= highUnp << 32
		off += 16
	}
	// Имя файла.
	if nameSize <= 0 || off+nameSize > 0xFFFF*4 {
		return e, 0, false
	}
	name := make([]byte, nameSize)
	if _, err := r.ReadAt(name, off); err != nil {
		return e, 0, false
	}
	off += nameSize
	e.Name = trimNul(string(name))
	if blockFlags&rarLHDSalt != 0 {
		off += 8
	}
	// EXT_TIME: пропускаем (структура variable-length, разбирать не критично).
	if blockFlags&rarLHDExtTime != 0 {
		off += skipExtTime(r, off)
	}
	return e, off, true
}

// skipExtTime возвращает размер блока EXT_TIME.
func skipExtTime(r io.ReaderAt, off int64) int64 {
	b := make([]byte, 2)
	if _, err := r.ReadAt(b, off); err != nil {
		return 0
	}
	flags := binary.LittleEndian.Uint16(b[0:2])
	size := int64(0)
	if flags&0x0001 != 0 { // mtime
		size += 4
	}
	if flags&0x0002 != 0 { // ctime
		size += 4
	}
	if flags&0x0004 != 0 { // atime
		size += 4
	}
	if flags&0x0008 != 0 { // arctime
		size += 4
	}
	return size
}

func trimNul(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == 0 {
			return s[:i]
		}
	}
	return s
}
