# Статус проекта RutStream

**Обновлено:** 21.10.2026
**Этап:** Phase 0 (спайк / Go-No-Go гейт) — завершён на 3 из 5 гипотез

---

## 1. Что уже работает (проверено запуском, не «по памяти»)

| Гипотеза (Phase 0) | Статус | Доказательство |
|---|---|---|
| **H4.** Выборочная загрузка: качаем только нужный файл из мультифайловой раздачи | ✅ **ПРОЙДЕНА** | `internal/torrent/selective_test.go`: раздача 24 МБ (12 файлов по 2 МБ) → скачано **2.11 МБ (8.4 %)** только для файла №8, хеш совпал, остальные piece'ы не запрошены |
| **H3.** Announce-сервер rutracker отвечает не-браузерному клиенту | ✅ **ПРОЙДЕНА** | `http://bt2.t-ru.org/ann` и `http://bt3.t-ru.org/ann` → `HTTP 200`, bencode-ответ: `interval=381`, `peers=1`. Ключевой момент: `info_hash` должен передаваться **сырыми байтами** (не base64) |
| **H5.** Индекс треков строится по метаданным торрента | ✅ частично | `internal/mediaindex`: смещения файлов, piece-план, fuzzy-сопоставление названий, sniffer'ы FLAC/MP3 — покрыты юнит-тестами. Проверка на **реальной** музыкальной раздаче — ждёт аккаунт |
| **H1.** Скачивание `.torrent` (`dl.php`) с сессией | ⏳ требует аккаунт | Код готов (`internal/rutracker/files.go`), HTTP-клиент, авторизация, импорт cookie. Проверка требует логин/пароль |
| **H2.** Announce с **реальным** passkey и реальной раздачей | ⏳ требует аккаунт | Трекер отвечает (H3), но passkey rutracker берётся из магнита/`.torrent` конкретной раздачи |

## 2. Сделанные модули

```
internal/mediaindex/     математика piece'ов, план загрузки, fuzzy-матчинг треков,
                         sniffer'ы FLAC (STREAMINFO/VORBIS_COMMENT/CUESHEET) и MP3 (ID3v2/Xing/VBRI)
internal/torrent/        движок поверх anacrolix/torrent: выборочные piece'ы и файлы,
                         SOCKS5/HTTP-прокси, piece store, статистика
internal/rutracker/      клиент форума: win-1251 → UTF-8, зеркала, cookie-jar,
                         поиск (tracker.php), разбор темы (magnet/метаданные/треклист),
                         список внутренних файлов (viewtorrent.php), .torrent (dl.php), Atom-фид
internal/testutil/       генератор тестовых раздач
cmd/spike/               стенд Phase 0: local | announce | feed | search | topic | files | login
```

## 3. Важные находки (меняют план)

1. **`anacrolix/torrent` требует `cfg.Seed = true`**, иначе клиент отклоняет входящие
   соединения («don't want conns right now») — сидер не отдаёт данные. Найдено
   экспериментально, в логах `client.go:650`.
2. **Приоритет файла (`File.SetPriority(PriorityNone)`) действительно отключает
   загрузку ненужных piece'ов** — это и есть ядро задумки, теперь есть тест.
3. **Политика Application Control на машине заказчика блокирует бинарники с
   браузерной автоматизацией** (проверено: бинарник с `go-rod` и даже с
   «голым» CDP-клиентом блокируются; бинарник только с `net/http` — работает).
   → Браузерный транспорт вынесен **за build-тег `browser`** и по умолчанию
     не собирается. Основной путь — обычный HTTP + импорт cookie `bb_session`.
4. **Бинарники Go нужно собирать с `-ldflags "-s -w"`** — с полной таблицей
   символов они блокируются той же политикой. Закреплено в `Makefile`.
5. Cloudflare-challenge на форме закрывает HTML-страницы (`viewforum.php`,
   `viewtopic.php`, `tracker.php`, `dl.php`), но:
   - `index.php` (дерево разделов) — отдаётся без challenge;
   - `feed.rutracker.cc/atom/f/{id}.atom` — работает **без авторизации**;
   - announce-сервер — работает без challenge.
6. Форум отдаётся в **windows-1251** — нужен перекод.

## 4. Что нужно от заказчика

| # | Что | Зачем | Как передать |
|---|---|---|---|
| 1 | **Логин/пароль rutracker** (или готовая cookie `bb_session`) | Проверить H1/H2 на реальной раздаче: скачать `.torrent`, получить magnet с passkey, сделать announce, найти трек в настоящей дискографии | env: `RUTSTREAM_USER` / `RUTSTREAM_PASS` или `RUTSTREAM_COOKIE` |
| 2 | Подтвердить, что P2P-раздачи на rutracker можно качать с вашего IP (домашний, без VPN) | Если IP без «проблемной» репутации — challenge может не возникать, и тогда хватит чистого HTTP | — |
| 3 | Решить, нужен ли собственный прокси/VPN для P2P | В РФ rutracker заблокирован; для peer-соединений нужен SOCKS5/VPN | Настройка в приложении уже заложена |

## 5. Следующие шаги (Phase 1)

1. `internal/audio` — декодеры FLAC/MP3 → PCM, вывод (WAV-файл для тестов, `beep` для устройства).
2. Сквозной сценарий на реальной раздаче: **тема → .torrent → индекс → piece-план → поток → звук**.
3. `internal/catalog` — SQLite: разделы, раздачи, треки, индекс, история.
4. `cmd/rutstream` — основное приложение (CLI-каркас → Wails UI).
5. `internal/playback` — оркестратор: resolve → plan → stream → play, переключение треков без пересоздания сессии.

## 6. Как собирать и запускать

```powershell
# Go установлен локально: %TEMP%\opencode\go
$env:GOPATH="$env:TEMP\opencode\gopath"; $env:GOMODCACHE="$env:GOPATH\pkg\mod"; $env:GOCACHE="$env:GOPATH\build-cache"
$go="$env:TEMP\opencode\go\bin\go.exe"

cd rutstream
& $go build -ldflags "-s -w" -o bin\spike.exe ./cmd/spike
.\bin\spike.exe local          # проверка H4 (выборочная загрузка)
.\bin\spike.exe announce http://bt2.t-ru.org/ann   # проверка H3 (трекер)
.\bin\spike.exe feed 1760      # живой Atom-фид раздела
```

Тесты:

```powershell
& $go test -c -ldflags "-s -w" -o bin\rt.test.exe ./internal/rutracker
.\bin\rt.test.exe "-test.v=true"
```
