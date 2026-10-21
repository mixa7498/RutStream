package main

import (
	"bytes"
	"fmt"
	"os"

	"github.com/anacrolix/torrent/metainfo"
)

// runTorrentDump разбирает скачанный .torrent и печатает его содержимое.
func runTorrentDump(args []string) error {
	path := os.Getenv("TEMP") + `\rt.torrent`
	if len(args) > 0 {
		path = args[0]
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	mi, err := metainfo.Load(bytes.NewReader(b))
	if err != nil {
		return fmt.Errorf("не bencode: %w", err)
	}
	info, err := mi.UnmarshalInfo()
	if err != nil {
		return err
	}
	fmt.Println("announce:  ", mi.UpvertedAnnounceList().DistinctValues())
	fmt.Println("файлов:    ", len(info.Files))
	fmt.Println("размер:    ", info.TotalLength())
	fmt.Println("piece:     ", info.PieceLength, " piece'ов:", info.NumPieces())
	files := info.UpvertedFiles()
	for i, f := range files {
		if i >= 20 {
			fmt.Printf("  … ещё %d\n", len(files)-i)
			break
		}
		fmt.Printf("  %-70s %d\n", f.DisplayPath(&info), f.Length)
	}
	return nil
}
