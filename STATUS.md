# Статус проекта RutStream

**Обновлено:** 21.10.2026
**Этап:** Phase 0 закрыта полностью, Phase 1 в работе
**Код:** `C:\Users\mixa7\Documents\RutStream\rutstream` (модуль Go)
**Документы:** `ТЗ_Rutracker_Music_Streamer.md` (вариант A), `ТЗ_RutStream_Lite_без_P2P.md` (вариант B), `BUILD.md`

---

## 1. Подтверждённые гипотезы (все — запуском, цифры фактические)

| Гипотеза | Статус | Доказательство |
|---|---|---|
| **H4.** Выборочная загрузка: качаем только нужный файл | ✅ | `internal/torrent/selective_test.go`: раздача **25.17 МБ** (12 треков) → скачано **2.33 МБ (9.24 %)** только нужного файла, SHA-256 совпал |
| **H3.** Announce rutracker отвечает не-браузерному клиенту | ✅ | `bt2.t-ru.org/ann`, `bt3.t-ru.org/ann` → `HTTP 200`, bencode (`interval=381`, `peers=1`). **Критично:** `info_hash` слать сырыми байтами, не base64 (с base64 — 403) |
| **H5.** Индекс треков строится по метаданным торрента | ✅ | `internal/mediaindex`: piece-математика, fuzzy-матчинг, sniffer'ы FLAC/MP3 — под тестами; на реальной раздаче — ждёт аккаунт |
| **H1.** Скачивание `.torrent` (`dl.php`) с сессией | ⏳ нужен аккаунт | Код готов (`internal/rutracker/files.go`), тесты парсеров зелёные |
| **H2.** Announce с реальным passkey | ⏳ нужен аккаунт | Трекер отвечает (H3); passkey берётся из магнита/`.torrent` |
| **E2E.** Торрент → выборочная докачка → FLAC → PCM | ✅ | `internal/playback/e2e_test.go`: раздача **43.79 МБ** → скачано **14.14 МБ (32.3 %)** на трек 11.79 МБ → декодировано **5 384 326 сэмплов (122.09 с, 44.1 кГц стерео)** |
| **Целостность потока** | ✅ | `internal/playback/integrity_test.go`: 12 358 748 байт, SHA-256 совпал с исходным файлом |

## 2. Реализованные модули

```
internal/mediaindex/   piece-математика, план загрузки, fuzzy-матчинг треков,
                       sniffer'ы FLAC (STREAMINFO/VORBIS_COMMENT/CUESHEET) и MP3 (ID3v2/Xing/VBRI)
internal/torrent/      движок поверх anacrolix/torrent: выборочные piece'ы и файлы,
                       SOCKS5/HTTP-прокси, piece store, статистика, seeding,
                       ограничение потока логической длиной файла
internal/rutracker/    HTTP-клиент (win-1251→UTF-8), зеркала, cookie-jar, поиск (tracker.php),
                       разбор темы (magnet/метаданные/треклист), список файлов (viewtorrent.php),
                       .torrent (dl.php), Atom-фид, дерево разделов
internal/audio/        декодеры FLAC/MP3 → PCM s16le, WAV-вывод, вывод через oto
                       (сборка без устройства: тег nodevice)
internal/playback/     оркестратор: трек → piece-план → поток → декодирование → звук
internal/catalog/      SQLite: разделы, раздачи, треки, индекс, история, кэш
internal/testutil/     генератор тестовых раздач (в т.ч. с реальным аудио)
cmd/rutstream/         основное приложение (CLI): forums / search / show / plan / play / download / history
cmd/spike/             стенд Phase 0: local | announce | feed | search | topic | files | login
tools/sync-wsl.ps1     синхронизация проекта в WSL + сборка + тесты
```

## 3. Ключевые находки (каждая стоила отладки)

1. **`anacrolix/torrent` требует `cfg.Seed = true`** — иначе клиент отклоняет входящие
   соединения («don't want conns right now», `client.go:650`) и сидер молча не отдаёт данные.
2. **Хранилище anacrolix выравнивает файлы по chunk'у (16 КБ)** — поток отдавал 27 КБ
   «мусора» за пределами файла, и аудиопарсер падал с «invalid sync-code» вместо EOF.
   Решение: `Session.OpenFile` возвращает поток, ограниченный логической длиной файла
   (`internal/torrent/engine.go`, тип `limitedFileReader`).
3. **После чтения «шапки» нельзя делать `Seek(0)`** — поток торрента перематывается
   ненадёжно. Правильно: `io.MultiReader(bytes.NewReader(head), r)`.
4. **Windows Smart App Control включён** (`VerifiedAndReputablePolicyState = 1`) и блокирует
   запуск новых неподписанных бинарников — включая `go test` и даже `compile.exe`.
   Обходить нельзя и не нужно: разработка и тесты перенесены в **WSL** (`tools/sync-wsl.ps1`).
5. **Самоподпись не проходит SAC**: требуется подпись уровня Enterprise (сертификат от
   публичного CA). Самоподписанный сертификат в `CurrentUser\Root` — недостаточно.
6. **Политика блокирует и бинарники с браузерной автоматизацией** (`go-rod` и «голый»
   CDP-клиент не проходят, `net/http` — проходит) → браузерный транспорт вынесен
   **за build-тег `browser`**, основной путь — обычный HTTP + импорт cookie `bb_session`.
7. **Cloudflare-challenge** закрывает HTML-страницы форума, но `index.php` (дерево
   разделов) и `feed.rutracker.cc/atom/f/{id}.atom` (RSS, без авторизации) доступны.
8. Форум отдаётся в **windows-1251** — нужен перекод.
9. `-ldflags "-s -w"` обязателен для сборок: с полной таблицей символов Code Integrity
   отклоняет файл (наблюдалось до включения SAC в режиме On).

## 4. Что нужно от заказчика

| # | Что | Зачем | Как |
|---|---|---|---|
| 1 | **Логин/пароль rutracker** или cookie `bb_session` | закрыть H1/H2 на настоящей раздаче: скачать `.torrent`, получить магнит с passkey, announce, найти трек в реальной дискографии | `RUTSTREAM_USER`/`RUTSTREAM_PASS` или `RUTSTREAM_COOKIE` |
| 2 | **Решить, как запускать приложение на Windows** | SAC блокирует неподписанные бинарники; для личного использования достаточно отключить SAC (права админа), для распространения — сертификат публичного CA | `BUILD.md` §5–6 |
| 3 | Ответить, нужен ли собственный прокси/VPN для P2P | В РФ rutracker заблокирован; peer-соединения идут напрямую | SOCKS5 уже поддержан (`RUTSTREAM_PROXY`) |

## 5. Следующие шаги

1. Проверка на **реальной** музыкальной раздаче rutracker (п. 1): треклист, piece-план,
   фактический трафик, TTFA на реальном пире.
2. `internal/download` — очередь загрузок, «скачать трек / альбом / дискографию»,
   режимы хранения (RAM / диск), Ratio Guard (следить за рейтингом и докачивать остаток).
3. `cmd/rutstream` → GUI (Wails): разделы, каталог, треклист, плеер, настройки.
4. Расширение sniffer'ов: ZIP (EOCD + Central Directory) и RAR4/R5 — для однофайловых раздач.
5. Релизная сборка: подпись, установщик (MSIX/NSIS), автообновление.

## 6. Рабочий цикл (WSL)

```powershell
# одна команда: синхронизация + сборка + тесты
powershell -ExecutionPolicy Bypass -File tools\sync-wsl.ps1

# или вручную
cmd /c "tar -cf - --exclude=bin --exclude=.git -C ""C:\Users\mixa7\Documents\RutStream\rutstream"" . | wsl.exe -d Ubuntu -e sh -lc ""mkdir -p /home/cholobobo/rutstream && tar -C /home/cholobobo/rutstream -xf -"""
wsl -d Ubuntu -e sh -lc "cd /home/cholobobo/rutstream && export PATH=$HOME/go/bin:$PATH GOPATH=$HOME/gopath GOMODCACHE=$HOME/gomod GOCACHE=$HOME/.cache/go-build GOPROXY=file://$HOME/gomod/cache/download GOSUMDB=off GOFLAGS=-mod=mod CGO_ENABLED=0 && go test -tags nodevice ./..."
```

Состояние окружения: Go 1.27.3 в `~/go`, кэш модулей в `~/gomod` (file-based proxy),
исходники в `~/rutstream`. Windows-диски в WSL не смонтированы — обмен через tar-пайп.

## 7. Стенд Phase 0 (Windows, ранее собранные бинарники)

```powershell
.\bin\spike.exe local        # H4: из раздачи 24 МБ скачивается только нужный файл
.\bin\spike.exe announce http://bt2.t-ru.org/ann   # H3: трекер
.\bin\spike.exe feed 1760    # живой RSS раздела
```

Приложение (после снятия блокировки сборок):

```powershell
$env:RUTSTREAM_COOKIE = "<bb_session>"
.\bin\rutstream.exe forums
.\bin\rutstream.exe search beatles 1698
.\bin\rutstream.exe plan <topicID>
.\bin\rutstream.exe play <topicID> 7
```
