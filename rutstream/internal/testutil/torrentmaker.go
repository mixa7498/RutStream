// Package testutil помогает создавать тестовые раздачи и проверять движок.
package testutil

import (
	"crypto/rand"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
)

// GeneratedFile — описание искусственного файла внутри раздачи.
type GeneratedFile struct {
	// Path — путь относительно корня раздачи (например "CD1/01 - Song.flac").
	Path string
	// Size — размер в байтах.
	Size int64
	// Fill — если задана, содержимое файла заполняется ею (иначе — случайные байты).
	Fill byte
}

// MakeTorrent создаёт metainfo для мультифайловой раздачи и (опционально)
// записывает сами файлы на диск в каталог destDir (в раскладке <destDir>/<Name>/<path>).
func MakeTorrent(name string, files []GeneratedFile, pieceLen int64, destDir string) (*metainfo.MetaInfo, error) {
	return MakeTorrentWithContent(name, files, pieceLen, destDir, nil)
}

// MakeTorrentWithContent — как MakeTorrent, но позволяет подставить реальное
// содержимое для отдельных файлов: content[путь внутри раздачи] = путь к источнику.
func MakeTorrentWithContent(name string, files []GeneratedFile, pieceLen int64, destDir string, content map[string]string) (*metainfo.MetaInfo, error) {
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })

	info := &metainfo.Info{
		Name:        name,
		PieceLength: pieceLen,
	}
	for _, f := range files {
		parts := strings.Split(f.Path, "/")
		info.Files = append(info.Files, metainfo.FileInfo{
			Length: f.Size,
			Path:   parts,
		})
	}

	// Создаём содержимое файлов, чтобы вычислить piece-хеши.
	root := filepath.Join(destDir, name)
	for _, f := range files {
		p := filepath.Join(root, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return nil, err
		}
		if src, ok := content[f.Path]; ok {
			if err := copyFile(src, p); err != nil {
				return nil, err
			}
			continue
		}
		if err := writeFile(p, f); err != nil {
			return nil, err
		}
	}

	if err := info.GeneratePieces(func(fi metainfo.FileInfo) (io.ReadCloser, error) {
		return os.Open(filepath.Join(root, filepath.FromSlash(strings.Join(fi.Path, "/"))))
	}); err != nil {
		return nil, fmt.Errorf("вычисление piece-хешей: %w", err)
	}

	infoBytes, err := bencode.Marshal(info)
	if err != nil {
		return nil, err
	}
	mi := &metainfo.MetaInfo{InfoBytes: infoBytes}
	mi.SetDefaults()
	return mi, nil
}

// copyFile копирует файл-источник в путь назначения.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

func writeFile(path string, f GeneratedFile) error {	out, err := os.Create(path)
	if err != nil {
		return err
	}
	defer out.Close()
	buf := make([]byte, 64*1024)
	for i := range buf {
		if f.Fill != 0 {
			buf[i] = f.Fill
		} else {
			buf[i] = byte(i % 251)
		}
	}
	// Добавим немного «случайности» в начало, чтобы файлы не были идентичны.
	if _, err := rand.Read(buf[:1024]); err != nil {
		return err
	}
	remaining := f.Size
	for remaining > 0 {
		n := int64(len(buf))
		if n > remaining {
			n = remaining
		}
		if _, err := out.Write(buf[:n]); err != nil {
			return err
		}
		remaining -= n
	}
	return nil
}
