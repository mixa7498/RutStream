package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mixa7/rutstream/internal/sign"
)

// cmdSign подписывает исполняемый файл сертификатом для подписи кода.
//
//   rutstream sign                      — создать самоподписанный сертификат
//                                         и подписать им rutstream.exe
//   rutstream sign path/to/app.exe      — подписать указанный файл
//   rutstream sign --pfx cert.pfx app.exe — подписать сертификатом из PFX
//
// Метка времени ставится автоматически, поэтому подпись остаётся действительной
// и после истечения сертификата.
func cmdSign(ctx context.Context, args []string) error {
	var (
		pfxPath     string
		password    string
		subject     = "RutStream"
		years       = 5
		noTimestamp bool
		targets     []string
	)
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--pfx":
			i++
			if i >= len(args) {
				return fmt.Errorf("--pfx требует путь к файлу")
			}
			pfxPath = args[i]
		case "--password":
			i++
			if i >= len(args) {
				return fmt.Errorf("--password требует значение")
			}
			password = args[i]
		case "--name":
			i++
			if i >= len(args) {
				return fmt.Errorf("--name требует значение")
			}
			subject = args[i]
		case "--years":
			i++
			if i >= len(args) {
				return fmt.Errorf("--years требует число")
			}
			fmt.Sscanf(args[i], "%d", &years)
		case "--no-timestamp":
			noTimestamp = true
		default:
			targets = append(targets, args[i])
		}
	}
	if len(targets) == 0 {
		// По умолчанию — сам бинарник рядом с исполняемым файлом.
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		targets = []string{exe}
	}

	tsa := sign.DefaultTSA
	if noTimestamp {
		tsa = ""
	}

	// Подписать работающий файл нельзя: Windows держит его открытым и не
	// даёт ни удалить, ни переименовать. Поэтому, если цель — сам запущенный
	// бинарник, подписываем копию рядом и говорим, что её переименовать.
	if exe, err := os.Executable(); err == nil {
		for i, t := range targets {
			if sameFile(t, exe) {
				copyPath := t + ".signed"
				if err := copyFile(t, copyPath); err != nil {
					return err
				}
				if err := signFile(copyPath, pfxPath, password, subject, years, tsa); err != nil {
					os.Remove(copyPath)
					return err
				}
				targets[i] = ""
				fmt.Printf("Подписана копия: %s\n", copyPath)
				fmt.Println("Поднимите её на место запущенного файла:")
				fmt.Printf("  mv %s %s\n", copyPath, t)
			}
		}
		var rest []string
		for _, t := range targets {
			if t != "" {
				rest = append(rest, t)
			}
		}
		targets = rest
		if len(targets) == 0 {
			return nil
		}
	}

	for _, t := range targets {
		if err := signFile(t, pfxPath, password, subject, years, tsa); err != nil {
			return err
		}
		fmt.Printf("Подписано: %s\n", t)
	}
	return nil
}

// signFile подписывает файл: своим PFX (--pfx) или самоподписанным сертификатом.
func signFile(path, pfxPath, password, subject string, years int, tsa string) error {
	if pfxPath != "" {
		return sign.SignWithPFX(path, pfxPath, password, tsa)
	}
	cert, key, pfx, der, err := sign.CreateSelfSigned(sign.SelfSignedOptions{
		Subject:      subject,
		ValidYears:   years,
		TimestampURL: tsa,
	})
	if err != nil {
		return err
	}
	if err := sign.SignFile(path, cert, key, tsa); err != nil {
		return err
	}
	base := strings.TrimSuffix(path, filepath.Ext(path))
	if err := sign.WritePFXFile(base+"-key.pfx", pfx); err != nil {
		return err
	}
	if err := sign.WriteCertificateFile(base+"-cert.cer", der); err != nil {
		return err
	}
	return nil
}

// sameFile сравнивает пути (с учётом того, что один из них может быть
// относительным).
func sameFile(a, b string) bool {
	aa, err1 := filepath.Abs(a)
	bb, err2 := filepath.Abs(b)
	if err1 != nil || err2 != nil {
		return false
	}
	return strings.EqualFold(aa, bb)
}

// copyFile копирует файл.
func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}

// replaceFile заменяет файл (на Windows — сначала убираем оригинал).
func replaceFile(src, dst string) error {
	_ = os.Remove(dst)
	return os.Rename(src, dst)
}

var _ = replaceFile
