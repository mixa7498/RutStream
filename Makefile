# Сборка и тесты RutStream.
#
# ВАЖНО: бинарники собираются с `-ldflags "-s -w"`.
# На части машин с корпоративной политикой Application Control (WDAC/AppLocker)
# исполняемые файлы с полной таблицей символов Go блокируются, а «обрезанные»
# — проходят. Это подтверждено на целевой машине: без этих флагов запуск
# падает с «An Application Control policy has blocked this file».

GO      ?= go
LDFLAGS := -s -w
BINDIR  := bin

.PHONY: all build test vet clean spike

all: vet test build

## build — собрать приложение и стенд проверок.
build: $(BINDIR)/rutstream $(BINDIR)/spike

$(BINDIR)/rutstream:
	$(GO) build -ldflags "$(LDFLAGS)" -o $@ ./cmd/rutstream

$(BINDIR)/spike:
	$(GO) build -ldflags "$(LDFLAGS)" -o $@ ./cmd/spike

## test — прогнать тесты (собранный тест-бинарь запускается напрямую).
test:
	$(GO) vet ./...
	$(GO) test -ldflags "$(LDFLAGS)" ./...

## vet — статический анализ.
vet:
	$(GO) vet ./...

## spike — собрать и собрать стенд Phase 0.
spike:
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BINDIR)/spike ./cmd/spike

clean:
	rm -rf $(BINDIR)
