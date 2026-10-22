// Package sign — подпись исполняемых файлов Windows (Authenticode) средствами Go.
//
// Зачем отдельным пакетом: приложение должно собираться и подписываться без
// внешних инструментов (signtool, PowerShell). Команда `rutstream sign`
// создаёт самоподписанный сертификат и подписывает им бинарник, либо
// подписывает сертификатом из PFX (например, купленным у публичного CA).
package sign

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"time"

	"github.com/KarpelesLab/authenticode"
	"software.sslmate.com/src/go-pkcs12"
)

// DefaultTSA — сервис метки времени (подпись остаётся действительной после
// истечения сертификата).
const DefaultTSA = "http://timestamp.digicert.com"

// SelfSignedOptions — параметры самоподписанного сертификата.
type SelfSignedOptions struct {
	// Subject — имя владельца (CN).
	Subject string
	// ValidYears — срок действия.
	ValidYears int
	// TimestampURL — адрес сервиса метки времени (пусто — без метки).
	TimestampURL string
}

// CreateSelfSigned создаёт самоподписанный сертификат для подписи кода.
//
// Возвращает сертификат, ключ, PFX (файл с ключом) и DER (файл для
// установки в «Доверенные корневые центры сертификации»).
func CreateSelfSigned(opts SelfSignedOptions) (cert *x509.Certificate, key crypto.Signer, pfx, der []byte, err error) {
	if opts.Subject == "" {
		opts.Subject = "RutStream"
	}
	if opts.ValidYears <= 0 {
		opts.ValidYears = 5
	}
	key, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("генерация ключа: %w", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, nil, nil, err
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: opts.Subject, Organization: []string{"RutStream"}},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.AddDate(opts.ValidYears, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err = x509.CreateCertificate(rand.Reader, tmpl, tmpl, key.Public(), key)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("создание сертификата: %w", err)
	}
	cert, err = x509.ParseCertificate(der)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	pfx, err = encodePFX(cert, key)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	return cert, key, pfx, der, nil
}

// encodePFX упаковывает сертификат с ключом в PFX (пустой пароль).
func encodePFX(cert *x509.Certificate, key crypto.Signer) ([]byte, error) {
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, err
	}
	priv, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return nil, err
	}
	return pkcs12.Encode(rand.Reader, priv, cert, nil, "")
}

// SignFile подписывает PE-файл (EXE/DLL) сертификатом.
//
// Метка времени добавляется, если задан timestampURL.
func SignFile(path string, cert *x509.Certificate, key crypto.Signer, timestampURL string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	signer := &certSigner{cert: cert, key: key, chain: []*x509.Certificate{cert}}
	out, err := authenticode.Sign(data, signer, authenticode.SignOptions{
		Hash:   crypto.SHA256,
		TSAURL: timestampURL,
	})
	if err != nil {
		return fmt.Errorf("подпись %s: %w", filepath.Base(path), err)
	}
	return os.WriteFile(path, out, 0o644)
}

// SignWithPFX подписывает файл сертификатом из PFX-файла.
func SignWithPFX(path, pfxPath, password, timestampURL string) error {
	pfx, err := os.ReadFile(pfxPath)
	if err != nil {
		return err
	}
	cert, key, err := decodePFX(pfx, password)
	if err != nil {
		return err
	}
	return SignFile(path, cert, key, timestampURL)
}

// certSigner — адаптер ключа и сертификата к интерфейсу библиотеки.
type certSigner struct {
	cert  *x509.Certificate
	key   crypto.Signer
	chain []*x509.Certificate
}

func (s *certSigner) Public() crypto.PublicKey { return s.key.Public() }
func (s *certSigner) Sign(rand io.Reader, digest []byte, opts crypto.SignerOpts) ([]byte, error) {
	return s.key.Sign(rand, digest, opts)
}
func (s *certSigner) Certificate() *x509.Certificate         { return s.cert }
func (s *certSigner) CertificateChain() []*x509.Certificate  { return s.chain }

// decodePFX разбирает PFX (пустой пароль — для созданных нами файлов).
func decodePFX(data []byte, password string) (*x509.Certificate, crypto.Signer, error) {
	key, cert, err := pkcs12.Decode(data, password)
	if err != nil {
		return nil, nil, fmt.Errorf("чтение PFX: %w", err)
	}
	signer, ok := key.(crypto.Signer)
	if !ok {
		return nil, nil, fmt.Errorf("ключ в PFX не поддерживает подпись")
	}
	return cert, signer, nil
}

// WriteCertificateFile сохраняет сертификат в виде .cer (DER в PEM-контейнере).
func WriteCertificateFile(path string, der []byte) error {
	buf := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	return os.WriteFile(path, buf, 0o644)
}

// WritePFXFile сохраняет PFX с ключом.
func WritePFXFile(path string, pfx []byte) error {
	return os.WriteFile(path, pfx, 0o600)
}
