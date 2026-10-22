# Сборка и проверки RutStream.
#
# Всё делается обычной командой make — никаких PowerShell-скриптов:
#
#   make build   — собрать приложение (Windows, один exe)
#   make windows — собрать Windows-бинарник из Linux/macOS
#   make test    — прогнать тесты
#   make sign    — собрать и подписать (сертификат создаётся автоматически)
#   make clean   — убрать артефакты сборки

GO      ?= go
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo 0.2.0)
LDFLAGS := -s -w -H windowsgui -X main.version=$(VERSION)
BIN     := bin/rutstream.exe

.PHONY: all build windows test vet sign clean

all: build

## Сборка приложения под Windows (один exe, без консоли).
build:
	cd rutstream && $(GO) build -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/rutstream

## Сборка Windows-бинарника из Linux или macOS.
windows:
	cd rutstream && GOOS=windows GOARCH=amd64 CGO_ENABLED=0 $(GO) build -tags nodevice -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/rutstream

## Тесты.
test:
	cd rutstream && $(GO) test ./...

## Статический анализ.
vet:
	cd rutstream && $(GO) vet ./...

## Сборка и подпись.
##
## Подписать работающий файл нельзя (Windows держит его открытым), поэтому
## сначала делаем копию, подписываем её и заменяем оригинал.
sign: build
	cd rutstream && cp $(BIN) $(BIN).tmp && ./$(BIN).tmp sign $(BIN).tmp && mv -f $(BIN).tmp $(BIN)
	@echo "Подписано: rutstream/$(BIN)"
	@echo "Сертификат (необязательно): certutil -addstore TrustedPublisher rutstream/bin/rutstream-cert.cer"

## Уборка.
clean:
	rm -f rutstream/bin/*
