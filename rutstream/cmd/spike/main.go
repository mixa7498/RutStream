// Command spike — проверочный стенд Phase 0 (Go/No-Go гейт по ТЗ).
//
// Команды:
//	spike local     — H4: скачивание только выбранного файла из мультифайловой раздачи
//	spike plan      — построение индекса треков по .torrent-файлу
//	spike announce  — H2/H3: проверка доступности announce-сервера rutracker
//	spike selftest  — быстрый прогон всех локальных проверок
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mixa7/rutstream/internal/mediaindex"
	"github.com/mixa7/rutstream/internal/testutil"
	"github.com/mixa7/rutstream/internal/torrent"
	"github.com/anacrolix/torrent/metainfo"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "local":
		err = runLocal(os.Args[2:])
	case "plan":
		err = runPlan(os.Args[2:])
	case "announce":
		err = runAnnounce(os.Args[2:])
	case "feed":
		err = runFeed(os.Args[2:])
	case "search":
		err = runSearch(os.Args[2:])
	case "login":
		err = runLogin(os.Args[2:])

	case "topic":
		err = runTopic(os.Args[2:])
	case "files":
		err = runFiles(os.Args[2:])
	case "selftest":
		err = runSelfTest()
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "ОШИБКА:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "использование: spike <local|plan|announce|selftest> [args]")
}

const mb = 1 << 20

// runLocal — главная проверка H4: «качаем один файл из раздачи, а не раздачу целиком».
func runLocal(args []string) error {
	target := 7
	if len(args) > 0 {
		n, err := strconv.Atoi(args[0])
		if err != nil {
			return fmt.Errorf("неверный индекс файла: %w", err)
		}
		target = n
	}
	ctx := context.Background()
	tmp, err := os.MkdirTemp("", "rutstream-spike-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	// --- создаём раздачу: 12 «треков» по 2 МБ ---
	const fileCount = 12
	const fileSize = 2 * mb
	name := "Artist - Album (2024) [FLAC]"
	files := make([]testutil.GeneratedFile, 0, fileCount)
	for i := 1; i <= fileCount; i++ {
		files = append(files, testutil.GeneratedFile{
			Path: fmt.Sprintf("CD1/%02d - Track %02d.flac", i, i),
			Size: fileSize,
		})
	}
	mi, err := testutil.MakeTorrent(name, files, 256*1024, tmp)
	if err != nil {
		return err
	}
	info, err := mi.UnmarshalInfo()
	if err != nil {
		return err
	}
	total := info.TotalLength()
	fmt.Printf("Раздача: %q, %d файлов, %s, piece=%s, piece'ов=%d\n",
		name, len(info.Files), human(total), human(int64(info.PieceLength)), info.NumPieces())

	// --- сидер ---
	seedDir := filepath.Join(tmp, "seed")
	if err := copyTree(filepath.Join(tmp, name), filepath.Join(seedDir, name)); err != nil {
		return err
	}
	seedEngine, err := torrent.New(torrent.Options{
		DataDir: seedDir, DisableDHT: true, DisablePEX: true, DisableWebseeds: true,
		ListenHost: "127.0.0.1", Seed: true,
		Logger: spikeLogger("seed"),
	})
	if err != nil {
		return err
	}
	defer seedEngine.Close()
	seedSession, err := seedEngine.Start(ctx, mi, torrent.StartOptions{})
	if err != nil {
		return err
	}
	// Данные уже на диске — клиент должен их проверить и начать раздавать.
	if err := seedSession.Torrent().VerifyData(); err != nil {
		return fmt.Errorf("проверка данных сидера: %w", err)
	}
	waitComplete(ctx, seedSession, 20*time.Second)

	var peerAddr string
	for _, a := range seedEngine.Client().ListenAddrs() {
		host, port, err := net.SplitHostPort(a.String())
		if err != nil {
			continue
		}
		if host == "0.0.0.0" || host == "::" || host == "" {
			host = "127.0.0.1"
		}
		peerAddr = net.JoinHostPort(host, port)
		break
	}
	if peerAddr == "" {
		return fmt.Errorf("сидер не слушает ни одного адреса")
	}
	fmt.Printf("Сидер готов: %s (пиков=%d)\n", peerAddr, seedSession.Stats().PiecesComplete)

	// --- личер ---
	leecherDir := filepath.Join(tmp, "leecher")
	leecherEngine, err := torrent.New(torrent.Options{
		DataDir: leecherDir, DisableDHT: true, DisablePEX: true, DisableWebseeds: true,
		ListenHost: "127.0.0.1", Seed: true,
		Logger: spikeLogger("leecher"),
	})
	if err != nil {
		return err
	}
	defer leecherEngine.Close()
	leecherSession, err := leecherEngine.Start(ctx, mi, torrent.StartOptions{
		PeerAddrs: []string{peerAddr},
	})
	if err != nil {
		return err
	}

	// Явно сообщаем клиенту адрес сидера (на случай, если spec-путь не сработал).
	addedPeers := leecherSession.AddClientPeer(seedEngine.Client())
	fmt.Printf("Личер: добавлено пиров=%d (адрес сидера %s)\n", addedPeers, peerAddr)
	fmt.Printf("Диагностика: gotInfo=%v closed=%v numPieces=%d info=%v\n",
		leecherSession.Torrent().GotInfo(),
		false,
		leecherSession.Torrent().NumPieces(),
		leecherSession.Info() != nil)

	// Ключевой шаг: нужен только один файл.
	if err := leecherSession.SetWantedFiles([]int{target}); err != nil {
		return err
	}
	plan, err := mediaindex.PlanForFile(&info, target)
	if err != nil {
		return err
	}
	fmt.Printf("Целевой файл #%d: %s (%s), piece'ы [%d,%d)\n",
		target, plan.Path, human(plan.ByteEnd-plan.ByteStart), plan.PieceStart, plan.PieceEnd)

	// Читаем файл — piece'ы будут подтягиваться по мере чтения.
	start := time.Now()
	r, err := leecherSession.OpenFile(target)
	if err != nil {
		return err
	}
	defer r.Close()
	h := sha256.New()
	type readResult struct {
		n   int64
		err error
	}
	done := make(chan readResult, 1)
	go func() {
		n, err := io.Copy(h, r)
		done <- readResult{n, err}
	}()
	var n int64
	for {
		select {
		case res := <-done:
			n = res.n
			if res.err != nil {
				return fmt.Errorf("чтение файла: %w", res.err)
			}
			goto readDone
		case <-time.After(500 * time.Millisecond):
			st := leecherSession.Stats()
			fmt.Printf("  ...качаем: %s из %s, пиров=%d (активных %d), piece'ов %d/%d\n",
				human(st.BytesDownloaded), human(plan.ByteEnd-plan.ByteStart),
				st.TotalPeers, st.ActivePeers, st.PiecesComplete, st.PiecesTotal)
		case <-time.After(120 * time.Second):
			return fmt.Errorf("таймаут: за 120 с не удалось получить файл (скачано %s)",
				human(leecherSession.Stats().BytesDownloaded))
		}
	}
readDone:
	elapsed := time.Since(start)
	fmt.Printf("Прочитано из потока: %s\n", human(n))
	st := leecherSession.Stats()

	// Сверяем с оригиналом.
	want, err := fileHash(filepath.Join(tmp, name, filepath.FromSlash(plan.Path)))
	if err != nil {
		return err
	}
	got := hex.EncodeToString(h.Sum(nil))

	fmt.Printf("\n=== РЕЗУЛЬТАТ ===\n")
	fmt.Printf("Скачано байт (факт трафика): %s\n", human(st.BytesDownloaded))
	fmt.Printf("Размер раздачи целиком:     %s\n", human(total))
	fmt.Printf("Процент от раздачи:         %.2f %%\n", 100*float64(st.BytesDownloaded)/float64(total))
	fmt.Printf("Отдано пиру:                %s\n", human(st.BytesUploaded))
	fmt.Printf("Время:                      %s\n", elapsed.Round(time.Millisecond))
	fmt.Printf("Хеш совпал:                 %v (%s)\n", got == want, got[:16])

	fail := false
	if got != want {
		fmt.Println("ПРОВАЛ: содержимое не совпадает с оригиналом")
		fail = true
	}
	limit := int64(fileSize + 2*256*1024)
	if st.BytesDownloaded > limit {
		fmt.Printf("ПРОВАЛ: скачано %s, ожидалось не более %s\n", human(st.BytesDownloaded), human(limit))
		fail = true
	}
	if st.BytesDownloaded < fileSize {
		fmt.Printf("ПРОВАЛ: скачано меньше размера файла (%s < %s)\n", human(st.BytesDownloaded), human(fileSize))
		fail = true
	}
	if !fail {
		fmt.Println("УСПЕХ (H4): скачан только нужный файл, остальные piece'ы не запрошены")
	}
	if fail {
		return fmt.Errorf("проверка H4 не пройдена")
	}
	return nil
}

// runPlan — строит индекс треков по .torrent-файлу (без скачивания данных).
func runPlan(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: spike plan <file.torrent>")
	}
	b, err := os.ReadFile(args[0])
	if err != nil {
		return err
	}
	mi, err := metainfo.Load(bytesReader(b))
	if err != nil {
		return err
	}
	info, err := mi.UnmarshalInfo()
	if err != nil {
		return err
	}
	hash, _ := torrent.InfoHashOf(mi)
	fmt.Printf("info_hash: %s\n", hash)
	fmt.Printf("announce:  %v\n", mi.UpvertedAnnounceList().DistinctValues())
	fmt.Printf("размер:    %s, piece=%s, piece'ов=%d\n",
		human(info.TotalLength()), human(info.PieceLength), info.NumPieces())

	ix, err := mediaindex.Build(&info, mediaindex.Hints{})
	if err != nil {
		return err
	}
	fmt.Printf("\nФайлов: %d, треков: %d\n", len(ix.Files), len(ix.Tracks))
	fmt.Println()
	fmt.Printf("%-4s %-8s %-10s %-8s %-12s %s\n", "№", "размер", "piece'ы", "лишние", "кодек", "путь")
	for i, t := range ix.Tracks {
		if i >= 40 {
			fmt.Printf("... ещё %d\n", len(ix.Tracks)-i)
			break
		}
		fmt.Printf("%-4d %-8s %-10s %-8s %-12s %s\n",
			i, human(t.ByteEnd-t.ByteStart),
			fmt.Sprintf("[%d,%d)", t.PieceStart, t.PieceEnd),
			human(t.ExtraBytes), t.Codec, t.Path)
	}
	return nil
}

// runAnnounce — проверка доступности announce-сервера (H2/H3).
func runAnnounce(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: spike announce <announce-url> [info_hash_hex]")
	}
	url := args[0]
	res, err := announce(url, 20*time.Second)
	if err != nil {
		return err
	}
	fmt.Printf("Announce %s\n", url)
	fmt.Printf("  HTTP %d, %d байт, тип %s\n", res.Status, len(res.Body), res.ContentType)
	if res.IsBencoded {
		fmt.Printf("  ответ bencoded: интервал=%d, пиров=%d\n", res.Interval, res.Peers)
		fmt.Println("  УСПЕХ (H3): трекер отвечает bencode-ответом, не-браузерный клиент допускается")
	} else {
		fmt.Printf("  первые байты: %q\n", snippet(res.Body, 200))
		fmt.Println("  ВНИМАНИЕ (H3): ответ не bencoded — announce может быть закрыт Cloudflare")
	}
	return nil
}

func runSelfTest() error {
	fmt.Println("=== Локальные проверки ===")
	if err := runLocal(nil); err != nil {
		return err
	}
	return nil
}

// spikeLogger создаёт логгер; при RUTSTREAM_DEBUG=1 включается уровень Debug.
func spikeLogger(name string) *slog.Logger {
	level := slog.LevelInfo
	if os.Getenv("RUTSTREAM_DEBUG") != "" {
		level = slog.LevelDebug
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})).With("client", name)
}

func waitComplete(ctx context.Context, s *torrent.Session, timeout time.Duration) bool {	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if s.Stats().Complete {
			return true
		}
		time.Sleep(200 * time.Millisecond)
	}
	return false
}

func fileHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func copyTree(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		out, err := os.Create(target)
		if err != nil {
			return err
		}
		defer out.Close()
		_, err = io.Copy(out, in)
		return err
	})
}

func human(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.2f ГБ", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.2f МБ", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.2f КБ", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d Б", n)
}

func snippet(b []byte, n int) string {
	s := strings.TrimSpace(string(b))
	if len(s) > n {
		s = s[:n] + "…"
	}
	return s
}
