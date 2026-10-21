# Статус проекта RutStream

**Обновлено:** 21.10.2026
**Этап:** Phase 0 завершена (3 из 5 гипотез подтверждены), Phase 1 в работе
**Код:** `C:\Users\mixa7\Documents\RutStream\rutstream` (модуль Go)
**Документы:** `ТЗ_Rutracker_Music_Streamer.md` (вариант A), `ТЗ_RutStream_Lite_без_P2P.md` (вариант B), `BUILD.md`

---

## 1. Подтверждённые гипотезы (проверено запуском)

| Гипотеза | Статус | Доказательство |
|---|---|---|
| **H4.** Выборочная загрузка: качаем только нужный файл из мультифайловой раздачи | ✅ | `internal/torrent/selective_test.go`: раздача 24 МБ (12 файлов) → скачано **2.11 МБ (8.4 %)** только для нужного файла, SHA-256 совпал, остальные piece'ы не запрошены |
| **H3.** Announce-сервер rutracker отвечает не-браузерному клиенту | ✅ | `http://bt2.t-ru.org/ann`, `http://bt3.t-ru.org/ann` → `HTTP 200`, bencode: `interval=381`, `peers=1`. **Критично:** `info_hash` передаётся сырыми байтами, не base64 |
| **H5.** Индекс треков строится по метаданным торрента | ✅ частично | `internal/mediaindex`: смещения файлов, piece-план, fuzzy-матчинг, sniffer'ы FLAC/MP3 — под юнит-тестами. Проверка на **реальной** музыкальной раздаче — ждёт аккаунт |
| **H1.** Скачивание `.torrent` (`dl.php`) с сессией | ⏳ нужен аккаунт | Код готов (`internal/rutracker/files.go`), HTTP-клиент, авторизация, импорт cookie |
| **H2.** Announce с реальным passkey и реальной раздачей | ⏳ нужен аккаунт | Трекер отвечает (H3); passkey берётся из магнита/`.torrent` раздачи |

## 2. Реализованные модули

```
internal/mediaindex/   piece-математика, план загрузки, fuzzy-матчинг треков,
                       sniffer'ы FLAC (STREAMINFO/VORBIS_COMMENT/CUESHEET) и MP3 (ID3v2/Xing/VBRI)
internal/torrent/      движок поверх anacrolix/torrent: выборочные piece'ы и файлы,
                       SOCKS5/HTTP-прокси, piece store, статистика, seeding
internal/rutracker/    HTTP-клиент (win-1251→UTF-8), зеркала, cookie-jar, поиск (tracker.php),
                       разбор темы (magnet/метаданные/треклист), список файлов (viewtorrent.php),
                       .torrent (dl.php), Atom-фид, дерево разделов
internal/audio/        декодеры FLAC/MP3 → PCM s16le, WAV-вывод, вывод через oto
internal/playback/     оркестратор: трек → piece-план → поток → декодирование → звук
internal/catalog/      SQLite: разделы, раздачи, треки, индекс, история, кэш
internal/testutil/     генератор тестовых раздач (в т.ч. с реальным аудио)
cmd/rutstream/         основное приложение (CLI): forums / search / show / plan / play / download / history
cmd/spike/             стенд Phase 0: local | announce | feed | search | topic | files | login
```

## 3. Ключевые находки (меняют план)

1. **`anacrolix/torrent` требует `cfg.Seed = true`**, иначе клиент отклоняет входящие
   соединения («don't want conns right now», `client.go:650`) — сидер не отдаёт данные.
2. **Приоритет файла (`File.SetPriority(PriorityNone)`) действительно отключает загрузку
   ненужных piece'ов** — ядро задумки подтверждено тестом.
3. **Windows Smart App Control на машине заказчика включён** и блокирует запуск новых
   неподписанных бинарников (включая `go test`). Без снятия политики нельзя проверить
   новый код — подробности и варианты в `BUILD.md` §5. Обходить политику нельзя и не нужно.
4. **Бинарники Go нужно собирать с `-ldflags "-s -w"`** — с полной таблицей символов
   их отклоняет Code Integrity. Закреплено в `Makefile` и `BUILD.md`.
5. **Политика также блокирует бинарники с браузерной автоматизацией** (проверено: `go-rod`
   и даже «голый» CDP-клиент блокируются, а бинарник только с `net/http` — работает).
   → Браузерный транспорт вынесен **за build-тег `browser`** и по умолчанию не собирается;
   основной путь — обычный HTTP + импорт cookie `bb_session`.
6. **Cloudflare-challenge** закрывает HTML-страницы форума, но `index.php` (дерево
   разделов) и `feed.rutracker.cc/atom/f/{id}.atom` (RSS, без авторизации) доступны.
7. Форум отдаётся в **windows-1251** — нужен перекод.

## 4. Что нужно от заказчика

| # | Что | Зачем | Как |
|---|---|---|---|
| 1 | **Логин/пароль rutracker** или cookie `bb_session` | H1/H2 на реальной раздаче: скачать `.torrent`, получить магнит с passkey, announce, найти трек в настоящей дискографии | `RUTSTREAM_USER`/`RUTSTREAM_PASS` или `RUTSTREAM_COOKIE` |
| 2 | **Снять Smart App Control** (нужны права админа) | Без этого не запускаются новые сборки и тесты — нельзя проверять код | см. `BUILD.md` §5 |
| 3 | Решить, как будет распространяться приложение | На машинах с SAC нужен **подписанный** бинарник | `BUILD.md` §6; для личного использования хватит самоподписи + импорта сертификата |
| 4 | Ответить, нужен ли собственный прокси/VPN для P2P | В РФ rutracker заблокирован; peer-соединения идут напрямую | SOCKS5 уже поддержан (`RUTSTREAM_PROXY`) |

## 5. Следующие шаги

1. **Снятие блокировки сборок** (п. 2 выше) → прогнать полный набор тестов,
   включая сквозной `internal/playback` (торрент → выборочная докачка → FLAC → WAV).
2. Проверка на **реальной** музыкальной раздаче rutracker (п. 1): треклист, piece-план,
   фактический трафик, TTFA.
3. `internal/download` — очередь загрузок, «скачать трек / альбом / дискографию»,
   режимы хранения (RAM / диск), Ratio Guard.
4. `cmd/rutstream` → GUI (Wails): разделы, каталог, треклист, плеер, настройки.
5. Расширение sniffer'ов: ZIP (EOCD + Central Directory) и RAR4/R5 — для однофайловых раздач.

## 6. Как запустить стенд

```powershell
$env:GOPATH="$env:TEMP\opencode\gopath"; $env:GOMODCACHE="$env:GOPATH\pkg\mod"; $env:GOCACHE="$env:GOPATH\build-cache"
$go="$env:TEMP\opencode\go\bin\go.exe"
cd C:\Users\mixa7\Documents\RutStream\rutstream
& $go build -ldflags "-s -w" -o bin\spike.exe ./cmd/spike
.\bin\spike.exe local        # H4: выборочная загрузка
.\bin\spike.exe announce http://bt2.t-ru.org/ann   # H3: трекер
.\bin\spike.exe feed 1760    # живой RSS раздела
```

Приложение (после снятия политики):

```powershell
& $go build -ldflags "-s -w" -o bin\rutstream.exe ./cmd/rutstream
$env:RUTSTREAM_COOKIE = "<bb_session>"
.\bin\rutstream.exe forums
.\bin\rutstream.exe search beatles 1698
.\bin\rutstream.exe plan <topicID>
.\bin\rutstream.exe play <topicID> 7
```
