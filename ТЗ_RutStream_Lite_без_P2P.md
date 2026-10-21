# Техническое задание (вариант B, без P2P)
## «RutStream Lite» — каталог, поиск и воспроизведение музыки из открытых стриминг-источников (SoundCloud, Bandcamp, Deezer, iTunes, MusicBrainz)

**Версия:** 1.0
**Дата:** 21.10.2026
**Тип:** настольное приложение (Windows / Linux / macOS), Go
**Связь с основным ТЗ:** `ТЗ_Rutracker_Music_Streamer.md` (вариант A, P2P). Настоящий документ — **независимая, самостоятельная реализация** упрощённого сценария; архитектура спроектирована так, чтобы вариант A позже добавлялся как ещё один адаптер источников (см. §14).

---

## 0. Резюме

### 0.1 Что и зачем

Цель исходной задачи — доступ к музыке, недоступной в локальных сервисах, с приоритетом lossless и полными дискографиями. Вариант B сознательно **отказывается от P2P** и строит приложение на открытых HTTP-API. Проверено, что это даёт:

**Что получаем:**
- **Каталог и поиск без ключей и без авторизации** — Deezer API, iTunes Search API, MusicBrainz (проверено: все отвечают `200` без токенов).
- **Доступ к «нецензурированному» контенту** — SoundCloud и Bandcamp: там нет российской маркировки «18+», нет санкционных ограничений на артистов, огромный слой инди/андеграунда.
- **Полные треки** — там, где это разрешено источником: SoundCloud (официальный плеер) и Bandcamp (`streaming_url` для streamable-альбомов).
- **Скачивание** — где это разрешено: Bandcamp free-download (mp3-128/320) и купленные альбомы (в т.ч. **lossless**).
- **Ноль износа SSD** при стриминге, ноль рейтинговых/аккаунтных рисков, ноль блокировок за P2P.
- **6–12 дней** до рабочего релиза вместо 37–51.

**Чего не получаем (и это надо понимать до старта):**
- ❌ **Lossless из открытых источников** — почти нигде. Только купленные Bandcamp-альбомы.
- ❌ **Полные дискографии «одним файлом»** и офлайн-библиотека на 500 МБ.
- ❌ **Треки длиннее 30 секунд** в Deezer/iTunes — там только превью.
- ❌ **Собственный BitTorrent-движок** → нет piece-level стриминга, нет частичных загрузок.

**Вывод:** вариант B — это **каталог + превью + полные треки из нецензурируемых источников**, а не замена торрент-решению. Он отлично работает как **fallback-модуль** внутри варианта A (см. §23.2 основного ТЗ) и как самостоятельное приложение, если основной риск варианта A (announce за Cloudflare, §22 R-1) окажется неустранимым.

### 0.2 Проверенные факты (разведка 21.10.2026, живые запросы)

| Источник | Эндпоинт | Результат | Аутентификация |
|---|---|---|---|
| **Deezer** | `GET https://api.deezer.com/search?q=radiohead&limit=5` | `200`, JSON: `id, title, title_short, duration, artist, album, link, preview, explicit_lyrics` | **не нужна** |
| **Deezer** | `GET https://api.deezer.com/track/302127` | `200`, JSON, поле `preview` (подписанный URL) | **не нужна** |
| **Deezer** | `GET <preview-url>` | `200`, `audio/mpeg`, 479 827 Б (30 с @128 kbps) | подпись в URL, ~срок действия ограничен |
| **iTunes** | `GET https://itunes.apple.com/search?term=radiohead&entity=song&limit=2` | `200`, JSON: `trackName, artistName, collectionName, trackTimeMillis, previewUrl (m4a/AAC), kind, trackExplicitness` | **не нужна** |
| **iTunes** | `GET <previewUrl>` | 30 с AAC (`.plus.aac.p.m4a`) | — |
| **MusicBrainz** | `GET https://musicbrainz.org/ws/2/recording?query=...&fmt=json` | `200`, JSON (треки, релизы, ISRC) | не нужна, **требуется корректный User-Agent** (политика MB) |
| **Bandcamp** | `GET https://<artist>.bandcamp.com/album/<slug>` | `200`, HTML с атрибутом `data-tralbum` (JSON) и `data-band` | не нужна |
| **Bandcamp** | `GET https://bandcamp.com/api/mobile/24/tralbum_details?tralbum_id=<id>&tralbum_type=a&band_id=<id>` | `200`, JSON: треки с `title, track_num, duration, streaming_url, is_streamable, price, has_digital_download, art_id, band_name, album_title` | не нужна |
| **Bandcamp** | `GET <streaming_url>` | полный трек (если `is_streamable=true`) | — |
| **SoundCloud** | `GET https://soundcloud.com/oembed?format=json&url=<url трека>` | `200`, JSON: `title, author_name, thumbnail_url, html` (в html — id трека) | **не нужна** |
| **SoundCloud** | `GET https://api-v2.soundcloud.com/search?q=...` | **`401`** без корректного `client_id` | нужен `client_id` |
| **SoundCloud** | `GET https://api.soundcloud.com/tracks/<id>/stream` | `403` (legacy-эндпоинт мёртв) | — |
| **Bandcamp** | `GET https://bandcamp.com/api/mobile/24/search?query=...` | `{"error":"bad function"}` — публичного поиска нет | — |
| **Bandcamp** | `GET https://bandcamp.com/search?q=...` | HTML-shell без результатов (SSR-данных нет) | — |

**Ключевые выводы из разведки:**

1. **Каталог и поиск решаются без P2P и без ключей** — Deezer + iTunes + MusicBrainz. Это 80 % ценности каталога.
2. **SoundCloud и Bandcamp отдают полные треки**, но их поиск закрыт (401 / no-SSR) → нужен либо `client_id` SoundCloud, либо браузерный транспорт (headless Chromium) — тот же механизм, что в варианте A.
3. **Deezer/iTunes дают только 30 секунд** — превью, не трек. Честный UI обязан это показывать.
4. **Bandcamp — единственный источник, где легально доступно скачивание** (free-download и покупки, включая lossless).

---

## 1. Границы и принципы

### 1.1 Жёсткие правила реализации

| # | Правило |
|---|---|
| R-1 | Никакого обхода DRM и средств защиты: только публичные HTTP-эндпоинты и официальные плееры |
| R-2 | Никакого «вытаскивания» закрытых потоков: SoundCloud — через официальный плеер (iframe + postMessage), а не через поддельные client_id/токены |
| R-3 | Скачивание разрешено только там, где источник сам его разрешает (Bandcamp free-download, покупки) |
| R-4 | Никакой телеметрии; только локальная БД |
| R-5 | Соблюдение ToS: приложение — плеер и каталог, не массовый загрузчик |
| R-6 | Пользователь может в любой момент включить «приватный режим» (без записи истории и кэша) |

### 1.2 Out of scope

- P2P/BitTorrent (переносится в вариант A).
- Собственные CDN/прокси для обхода гео-блокировок.
- Скачивание из Deezer/iTunes/SoundCloud (запрещено ToS/DRM).
- Лайб-миксер, эквалайзер, кроссфейд (кроме gapless) — можно в Phase 2+.
- Мобильные клиенты.

---

## 2. Архитектура

### 2.1 Ключевая идея: адаптер источника

Всё приложение строится вокруг единого интерфейса источника. Это позволяет варианту A (P2P) добавиться позже как ещё один адаптер — **без переписывания UI, каталога и плеера**.

```go
// internal/source
type Source interface {
    ID() string                 // "deezer", "itunes", "bandcamp", "soundcloud", "rutracker"(A)
    Name() string
    Capabilities() Caps         // Search, FullTrack, PreviewOnly, Download
    Search(ctx context.Context, q Query) ([]Result, error)
    Artist(ctx context.Context, id string) (*Artist, error)
    Album(ctx context.Context, id string) (*Album, error)
    Track(ctx context.Context, id string) (*Track, error)
    Stream(ctx context.Context, t *Track) (Stream, error)   // Stream = io.ReadCloser + формат + «живой» лицензионный токен
}

type Caps struct {
    Search      bool
    FullTracks  bool   // можно слушать целиком
    PreviewOnly bool   // только N секунд
    Download    bool   // разрешена загрузка
    Lossless    bool   // бывает lossless
    NeedsBrowser bool  // транспорт через headless-Chromium
}
```

### 2.2 Слои

```
┌──────────────────────────────────────────────────────────────────────┐
│  UI (Wails v2 + React/TS)                                            │
│  Поиск │ Каталог │ Исполнитель │ Альбом │ Плеер │ Плейлист │ Настройки│
└────────────────────────────┬─────────────────────────────────────────┘
                             │
┌────────────────────────────┴─────────────────────────────────────────┐
│  Core: Catalog (SQLite+FTS) │ Player (очередь, состояние) │ Recommender│
├──────────────────────────────────────────────────────────────────────┤
│  SourceManager                                                      │
│  ┌─────────┐ ┌─────────┐ ┌──────────┐ ┌────────────┐ ┌───────────┐  │
│  │ Deezer  │ │ iTunes  │ │ Bandcamp │ │ SoundCloud │ │ RutTracker│  │
│  │ search+ │ │ search+ │ │ mobile24 │ │ oEmbed +   │ │ (вариант A,│  │
│  │ preview │ │ preview │ │ + tralbum │ │ player embed│ │ добавится)│  │
│  └─────────┘ └─────────┘ └──────────┘ └────────────┘ └───────────┘  │
├──────────────────────────────────────────────────────────────────────┤
│  Transport: HTTP (retry, кэш, rate-limit) │ Browser (rod) │ Proxy    │
├──────────────────────────────────────────────────────────────────────┤
│  Storage: SQLite │ Cache (превью на диске) │ Secret store │ Logs     │
└──────────────────────────────────────────────────────────────────────┘
```

### 2.3 Транспорт

| Транспорт | Назначение | Детали |
|---|---|---|
| `http` | Deezer, iTunes, MusicBrainz, Bandcamp API, oEmbed | `net/http`, повторы с backoff, лимит частоты на хост, кэш ETag/TTL |
| `browser` | Поиск SoundCloud и Bandcamp, если нет `client_id`; авторизация Bandcamp (покупки) | headless Chromium (`go-rod`), переиспользование профиля, «человеческие» паузы |
| `proxy` | Все исходящие соединения | SOCKS5 / HTTP CONNECT; для гео-блокировок SoundCloud/Deezer в РФ — обязателен |

**Правило:** если для источника нужен браузер, приложение один раз открывает headless-сессию, кэширует результаты в SQLite и больше не дёргает сайт.

---

## 3. Модули источников: точные контракты

### 3.1 Deezer (каталог + превью) — основной источник

**Поиск**
```
GET https://api.deezer.com/search?q={query}&limit=25&index=0
GET https://api.deezer.com/search/artist?q={query}
GET https://api.deezer.com/search/album?q={query}
→ 200 application/json
{
  "data": [ { "id": 1483825282, "title": "...", "title_short": "...",
              "duration": 238, "explicit_lyrics": false, "artist": {...},
              "album": { "id":..., "title":..., "cover_xl": "..." },
              "link": "https://www.deezer.com/track/...",
              "preview": "https://cdnt-preview.dzcdn.net/api/1/1/.../xxx.mp3?hdnea=exp=...~hmac=..." } ],
  "total": 12345, "next": "...", "prev": "..."
}
```
Ограничение: ~300 результатов на запрос (пагинация `index`).

**Альбомы/исполнители:** `GET /album/{id}`, `GET /artist/{id}`, `GET /artist/{id}/albums?limit=50`, `GET /album/{id}/tracks`.

**Аудио:** `preview` → 30 с MP3 128 kbps (`audio/mpeg`, ~480 КБ). URL подписан и **живёт ограниченное время** → брать заново перед каждым воспроизведением, кэшировать только на 5–10 минут.

**Ограничения/риски Deezer:** нет полных треков; preview есть не у всех треков (проверено: у трека 302127 `preview` пуст) → UI обязан показывать «превью недоступно»; лимиты запросов без ключа; гео-блокировки в отдельных странах.

### 3.2 iTunes Search API (каталог + превью)

```
GET https://itunes.apple.com/search?term={query}&entity=song&limit=25&country=RU
GET https://itunes.apple.com/lookup?id={collectionId}&entity=song
→ 200 JSON: { "resultCount": 2, "results": [ {
      "trackId":..., "trackName":"Let Down", "artistName":"Radiohead",
      "collectionName":"...", "trackTimeMillis": 298000,
      "previewUrl":"https://audio-ssl.itunes.apple.com/itunes-assets/AudioPreview221/.../mzaf_...plus.aac.p.m4a",
      "kind":"song", "trackExplicitness":"explicit"|"notExplicit", "artworkUrl100":... } ] }
```
- 30 с AAC (m4a) → декодировать через `ffmpeg`/native.
- `trackExplicitness` → фильтр «18+» (в т.ч. русская маркировка).
- Лимит: ~20 запросов/мин на IP, кэш обязателен.
- Гео: параметр `country`; превью-файлы отдаются глобально (проверено).

### 3.3 MusicBrainz (метаданные, «склей»)

```
GET https://musicbrainz.org/ws/2/recording?query={q}&fmt=json&limit=25
GET https://musicbrainz.org/ws/2/release-group/{mbid}?inc=releases+artist-credits&fmt=json
```
- Обязательный `User-Agent: RutStream/0.1 ( contact@example.com )` — иначе 403.
- Лимит: 1 req/s.
- Даёт ISRC, год, лейбл, теги, «artist-credit» → надёжное сопоставление артистов между источниками.

### 3.4 Bandcamp (полные треки + скачивание)

**Шаг 1 — найти страницу артиста/альбома.** Публичного поиска нет → варианты:
1. Прямая ссылка, введённая пользователем (`https://<artist>.bandcamp.com/album/<slug>`) — **основной сценарий**;
2. Через `artist.bandcamp.com/music` — страница со списком альбомов (SSR HTML, проверено: 184 КБ, ссылки `/album/...`);
3. Через браузерный транспорт — если нужен поиск по каталогу Bandcamp.

**Шаг 2 — разобрать `data-tralbum`.** В HTML альбома есть атрибут `data-tralbum` с JSON (HTML-экранные сущности декодировать). Поля:
```
current: { id, album_id, band_id, title, artist, release_date, about, art_id, tracks, ... }
trackinfo[]: { id, track_id, title, track_num, duration, file{}, artist,
               streaming, is_downloadable, has_free_download, free_album_download,
               album_preorder, unreleased_track, license_type }
```
(все эти ключи подтверждены вживую на `radiohead.bandcamp.com/album/ok-computer-oknotok-1997-2017`)

**Шаг 3 — мобильный API (даёт `streaming_url` и условия продажи):**
```
GET https://bandcamp.com/api/mobile/24/tralbum_details?tralbum_id={album_id}&tralbum_type=a&band_id={band_id}
→ 200 JSON
{
  "title": "...", "artist": "...", "tracks": [ {
      "track_id":..., "title":"...", "track_num":1, "duration": 283.8,
      "streaming_url": "https://t4.bcbits.com/stream/....mp3",   // если is_streamable
      "is_streamable": true|false,
      "is_set_price": true, "price": 1.29, "currency": "EUR",
      "has_digital_download": true,
      "art_id":..., "band_id":..., "album_id":..., "band_name":..., "album_title":...,
      "track_url": "https://<artist>.bandcamp.com/track/<slug>" } ]
}
```
- Если `is_streamable=true` → `streaming_url` даёт **полный трек** → качаем и играем как обычный HTTP-поток.
- Если `is_streamable=false` → трек платный: показываем цену и кнопку «Купить» (открываем Bandcamp в браузере; покупка и скачивание — через сайт, с авторизацией пользователя).

**Шаг 4 — скачивание (где разрешено).** Для альбомов с free-download в `trackinfo[].file` присутствуют:
```
file: { "mp3-128": { "href": "...", "type": "audio/mpeg", "size": 3456789 } }
```
(для платных — пусто, что и наблюдалось). Скачивание разрешено только при `has_free_download` / `free_album_download == true`, с сохранением в папку `Artist/Album/NN - Title.mp3` и записью тегов.

### 3.5 SoundCloud (полные треки через официальный плеер)

**Метаданные — бесплатно и без ключей:**
```
GET https://soundcloud.com/oembed?format=json&url=https://soundcloud.com/<user>/<track>
→ 200 { "title":"Creep by Radiohead", "author_name":"Radiohead",
        "thumbnail_url":"https://i1.sndcdn.com/artworks-...-t500x500.jpg",
        "html":"<iframe ... src=\"https://w.sndcdn.com/player/?visual=true&url=https%3A%2F%2Fapi.soundcloud.com%2Ftracks%2F265069081&show_artwork=true\">" }
```
Из `html` извлекается **id трека** (`api.soundcloud.com/tracks/265069081`) — этого достаточно, чтобы построить embed-URL.

**Воспроизведение — официальный iframe-плеер + postMessage-команда.** SoundCloud Widget API поддерживает управление через `postMessage`:
```
→ iframe.contentWindow.postMessage("{\"method\":\"play\"}", "*")
→ {"method":"seek","value": 120000}     // мс
→ {"method":"setVolume","value": 80}
→ {"method":"pause"} / "play" / "togglePlay"
```
События плеера (ready/finish/play/pause/timeupdate) приходят в `window` через `postMessage` → приложение отображает позицию и длительность.

**Следствия для дизайна:**
- Полноценное воспроизведение **возможно** (вся длительность), но **вне нашего контроля**: свой декодер, ресемплинг, gapless, офлайн недоступны.
- Свой плеер-бар получается «виртуальным» (управление через postMessage), а звук идёт из встроенного WebView.
- Альтернатива: собственный стриминг через `api-v2` с `client_id` → тогда нужен «свой» плеер с полным контролем, но требует `client_id` (легально — зарегистрировать приложение в SoundCloud API, либо использовать официальный `client_id` веб-клиента; **решение по умолчанию — официальный embed, без client_id**).

**Поиск SoundCloud:** `api-v2` без `client_id` → `401`; HTML-поиск отдаётся без результатов. Поэтому:
- Вариант 1 (по умолчанию): поиск только по **прямой ссылке** на трек/плелист + через `oembed`;
- Вариант 2: браузерный транспорт (`rod`) → загружаем `https://soundcloud.com/search?q=…`, вытаскиваем результаты из DOM/гидратации;
- Вариант 3: пользователь вставляет свой `client_id` (настройка) → полноценный поиск по `api-v2`.

### 3.6 Сводная матрица источников

| Источник | Поиск | Метаданные | Аудио | Скачивание | Lossless | Без браузера? |
|---|---|---|---|---|---|---|
| Deezer | ✅ | ✅ | 30 с preview | ❌ | ❌ | ✅ |
| iTunes | ✅ | ✅ | 30 с preview | ❌ | ❌ | ✅ |
| MusicBrainz | ✅ | ✅✅ | ❌ | ❌ | — | ✅ |
| Bandcamp | ⚠️ (ссылка/браузер) | ✅✅ | ✅ полный трек (если streamable) | ✅ (free/purchased) | ✅ (покупка) | ✅ частично |
| SoundCloud | ⚠️ (client_id/браузер) | ✅ (oEmbed) | ✅ полный трек (embed) | ❌ | ❌ | ✅ (embed) |
| **RutTracker (вариант A)** | ✅ | ✅✅ | ✅ lossless, P2P | ✅ | ✅✅ | ✅ |

---

## 4. Каталог, поиск и рекомендации

### 4.1 Модель каталога

Единая модель, независимая от источника:

```go
type Track struct {
    ID          string      // "<source>:<nativeID>"
    Source      string
    Artist      string
    ArtistID    string
    Album       string
    AlbumID     string
    Title       string
    Duration    time.Duration
    TrackNo     int
    DiscNo      int
    Year        int
    Genre       string
    Explicit    bool
    ArtworkURL  string
    Streams     []StreamRef // варианты прослушивания: preview / full / embed
    Downloadable bool
    Lossless    bool
    NormKey     string      // нормализованный ключ для склейки между источниками
}
```

`NormKey` = `norm(artist) + "|" + norm(album) + "|" + norm(title)` — по нему разные источники **склеиваются** в один трек (например, Deezer-preview + SoundCloud-full + Bandcamp-download → одна строка в UI с тремя вариантами прослушивания). Это ключевая UX-фича: пользователь видит «источники: превью / полный трек / скачать».

### 4.2 Слияние источников (merge)

```
1. Поиск выполняется параллельно во всех доступных источниках (goroutine + errgroup).
2. Результаты нормализуются: artist/album/title → normKey; длительность — с допуском ±2 с.
3. Кластеризация: одинаковый normKey либо (совпадает artist+title и |Δduration| ≤ 2 с) → один кластер.
4. Кластер → один Track с набором Streams:
     - Deezer preview (30 с)   → preview
     - iTunes preview (30 с)   → preview (альтернатива)
     - Bandcamp streaming_url  → full
     - SoundCloud embed        → full (embed)
     - Bandcamp file            → download
5. Приоритет в UI: full > preview; lossless > lossy; источник с tracklist (Bandcamp) > одиночный.
```

### 4.3 Поиск

```
GET /api/search?q={query}&sources=deezer,itunes,mb&limit=25
→ { "results": [Cluster...], "perSource": {"deezer": 12, "itunes": 9, "mb": 4} }
```
- Дефолтные источники: Deezer + iTunes + MusicBrainz (без браузера, без ключей).
- Bandcamp/SoundCloud подключаются: по прямой ссылке, по `client_id` (SC) или по браузеру.
- Фильтры: «только с полным треком», «только lossless», «исключить 18+», «источник».
- Локальный полнотекстовый поиск по уже закэшированному каталогу (SQLite FTS5) — мгновенно.

### 4.4 Рекомендации

| Сигнал | Алгоритм |
|---|---|
| История прослушиваний | Совпадение по жанру/исполнителю из MusicBrainz-тегов |
| Похожие исполнители | MusicBrainz `similar artists`/relation, либо совпадение жанровых тегов |
| «Свежее» | последние релизы по artist-relation |
| Разделы/жанры | Классические подборки: жанр + год + «рейтинг MB/Deezer» (Deezer даёт `rank`, `nb_albums` → использовать как popularity-сигнал) |
| Плейлист пользователя | Ручной плейлист + автопродление по похожим трекам |

Deezer отдаёт `rank` (популярность, 1..1e6) и `nb_albums` — используются как popularity-метрика.

---

## 5. Плеер

### 5.1 Два режима воспроизведения

| Режим | Источник | Плеер | Возможности |
|---|---|---|---|
| **Native** (HTTP-поток) | Deezer/iTunes preview, Bandcamp `streaming_url` | наш: декодер → ресемплинг → устройство | полный контроль: seek, громкость, gapless, офлайн-кэш, нормализация |
| **Embed** | SoundCloud (iframe + postMessage) | официальный iframe-плеер | полный трек, но управление через postMessage, без офлайна и bit-perfect |

UI переключается автоматически: если у трека есть native-источник — используем его; если нет, но есть embed — показываем встроенный плеер с прозрачной плашкой «SoundCloud: официальный плеер».

### 5.2 Конвейер native-режима

```
HTTP stream (Range, если поддерживается)
   └─ io.Reader → Decoder
        ├─ MP3  (go-mp3)            — Deezer preview
        ├─ AAC/M4A (ffmpeg)         — iTunes preview
        └─ MP3  (go-mp3)            — Bandcamp streaming_url
   └─ Resampler (48 kHz) → ReplayGain → Fade → Audio device (beep: WASAPI/ALSA/Pulse/CoreAudio)
```
- Буфер: 2–4 с (превью короткие, полный буфер не нужен).
- Seek: превью Deezer — перезапрос URL и старт с 0 (30 с всё равно); Bandcamp full — seek по времени, если CDN поддерживает Range (проверяется в Phase 0).
- Кэш превью на диск: опционально, с TTL 7 дней и лимитом (например, 2 ГБ), для повторного прослушивания без сети.

### 5.3 Плейлист и очередь

- Очередь треков, режимы: «воспроизвести всё», «перемешать», «повтор один».
- Автопереход к следующему треку с тем же исполнителем/альбомом (сначала full, потом preview).
- Сохранение плейлистов в SQLite, экспорт M3U.

### 5.4 Ограничения, которые показываем честно

| Ситуация | Что видит пользователь |
|---|---|
| Только 30-сек превью | Бейдж «превью 30 с» + подсказка «полный трек недоступен в этом источнике» + список источников с полным треком, если найден |
| Нет preview | «Превью недоступно», кнопка «Открыть на <source>» |
| Bandcamp платный | Цена, кнопка «Купить» (открывает сайт), «послушать превью» если есть |
| SoundCloud embed | Плашка «официальный плеер SoundCloud» |

---

## 6. Скачивание (только там, где разрешено)

### 6.1 Правила

- Скачивание реализовано **только для Bandcamp** и только когда `has_free_download`/`free_album_download == true` либо трек уже куплен пользователем (авторизация в его браузере).
- Форматы: `mp3-128` (free), `mp3-320`/`mp3-64` (если артист разрешил), покупки — включая **FLAC/WAV/ALAC** (lossless доступен только за покупку).
- Для Deezer/iTunes/SoundCloud кнопки «скачать» нет вовсе.

### 6.2 Поведение

```
1. Пользователь: правый клик по треку → «Скачать (если доступно)»
2. Приложение проверяет file{} трека:
     - есть file → скачать с прогрессом, записать в ~/Music/RutStream/Artist/Album/NN - Title.mp3
     - нет file, но has_free_download → открыть страницу покупки в браузере
3. После скачивания: записать метаданные (ID3: title/artist/album/track/year/genre/artwork)
   и зарегистрировать трек в локальной библиотеке (офлайн-воспроизведение).
```

### 6.3 Локальная библиотека

- Проигранные/скачанные треки сохраняются в SQLite → раздел «Мой архив» с офлайн-воспроизведением.
- Формат хранения: `Artist/Album/Year/NN - Title.ext` + `.nfo`-подобный sidecar в SQLite.

---

## 7. Хранение и БД

```sql
CREATE TABLE track (
  id TEXT PRIMARY KEY,           -- "deezer:302127"
  source TEXT, artist TEXT, artist_id TEXT, album TEXT, album_id TEXT,
  title TEXT, duration_ms INTEGER, track_no INTEGER, disc_no INTEGER,
  year INTEGER, genre TEXT, explicit INTEGER, artwork_url TEXT,
  norm_key TEXT,                 -- для склейки
  downloadable INTEGER, lossless INTEGER, updated_at INTEGER
);
CREATE INDEX track_norm ON track(norm_key);
CREATE INDEX track_artist ON track(norm_title);

CREATE TABLE stream_ref (
  id TEXT PRIMARY KEY, track_id TEXT, kind TEXT,   -- preview|full|embed|download
  source TEXT, url TEXT, mime TEXT, expires_at INTEGER, note TEXT
);

CREATE TABLE album (id TEXT PRIMARY KEY, source TEXT, title TEXT, artist TEXT, year INTEGER, artwork_url TEXT, track_count INTEGER);
CREATE TABLE artist (id TEXT PRIMARY KEY, source TEXT, name TEXT, norm_name TEXT, image_url TEXT, bio TEXT);
CREATE TABLE cluster (id TEXT PRIMARY KEY, norm_key TEXT, track_id TEXT);   -- склейка источников
CREATE TABLE playlist (id TEXT PRIMARY KEY, name TEXT, created_at INTEGER);
CREATE TABLE playlist_item (playlist_id TEXT, position INTEGER, track_id TEXT, PRIMARY KEY(playlist_id, position));
CREATE TABLE history (track_id TEXT, played_at INTEGER, ms_played INTEGER);
CREATE TABLE kv (k TEXT PRIMARY KEY, v TEXT, updated_at INTEGER);          -- кэш ответов, client_id и т.п.
```

Кэш HTTP-ответов: `kv` + файловый кэш превью в `%APPDATA%/RutStream/cache/preview/`.

---

## 8. Настройки

| Группа | Параметры |
|---|---|
| Источники | Включение/выключение каждого источника; `SoundCloud client_id` (необязательно); режим поиска Bandcamp (ссылка/браузер) |
| Сеть | Прокси SOCKS5/HTTP (хост, порт, логин/пароль), таймауты, «использовать браузерный транспорт для X» |
| Качество | Приоритет: full > preview; фильтр 18+; предпочитать lossless; авто-«искать полный трек, если есть превью» |
| Плеер | Устройство вывода, громкость, gapless, ReplayGain, кроссфейд |
| Хранилище | Папка для загрузок, лимит кэша превью, приватный режим, очистка истории |
| О приложении | Версия, логи, экспорт/импорт настроек |

---

## 9. План работ (6–12 дней)

| Этап | Дни | Содержание | Результат |
|---|---|---|---|
| **P0. Спайк** | 1 | Проверить: Deezer search+preview в Go, iTunes preview, Bandcamp `tralbum_details` + `streaming_url` для streamable-альбома, SoundCloud oEmbed + embed-плеер в Wails | Рабочий прототип: поиск → превью → звук; отчёт по 4 источникам |
| **P1. Ядро** | 2–3 | Каркас Go+Wails, интерфейс `Source`, HTTP-транспорт с кэшем и rate-limit, SQLite+FTS, модель `Track`/`Cluster` | Каталог с поиском по Deezer+iTunes |
| **P2. Источники** | 2–3 | Bandcamp-адаптер (страница + mobile API + streaming), SoundCloud-адаптер (oEmbed + embed), MusicBrainz (метаданные и склейка) | 4 источника, слияние кластеров |
| **P3. Плеер** | 2 | Native-конвейер (mp3/aac → resample → beep), режим embed, плейлист/очередь, бейджи «превью 30 с» | Играбельный трек из любого источника |
| **P4. Скачивание + библиотека** | 1–2 | Bandcamp download, теги, локальная библиотека, офлайн | Скачивание там, где разрешено |
| **P5. Полировка** | 1 | Прокси, приватный режим, логи, установщики, README | Релиз v0.1 |

**Параллелизация:** P1 → (P2 ∥ P3) → P4 → P5.

**Факультативно (2–3 дня):** браузерный транспорт для поиска SoundCloud/Bandcamp; авторизация Bandcamp для покупок; расширенные рекомендации.

---

## 10. Критерии приёмки

| ID | Критерий |
|---|---|
| AC-1 | Поиск «radiohead» возвращает ≥ 10 результатов ≤ 3 с (без браузера) |
| AC-2 | Превью Deezer воспроизводится ≤ 2 с после клика, звук 128 kbps, без артефактов |
| AC-3 | Превью iTunes (AAC/m4a) декодируется и играет |
| AC-4 | Трек с Bandcamp `streaming_url` играет **полностью** (native) |
| AC-5 | Трек SoundCloud играет полностью через embed; UI показывает плашку «официальный плеер» |
| AC-6 | Один и тот же трек из 2+ источников сливается в одну карточку с перечнем вариантов |
| AC-7 | Все превью/потоки явно помечены бейджем «30 с» / «полный трек» / «lossless» |
| AC-8 | Скачивание доступно ровно для тех треков, где источник это разрешает; для остальных кнопки нет |
| AC-9 | Скачанный трек проигрывается офлайн; метаданные и обложка корректны |
| AC-10 | При выключенной сети приложение открывается, показывает кэш и историю, играет локальную библиотеку |
| AC-11 | Весь трафик идёт через настроенный SOCKS5-прокси (проверяется логом/тестом) |
| AC-12 | Приватный режим: за сессию не остаётся ни истории, ни кэша, ни логов с URL треков |
| AC-13 | Приложение не падает при 401/403/429/5xx и нестандартном ответе источника (показ «источник недоступен», retry) |

---

## 11. Риски

| ID | Риск | Вероятность | Влияние | Митигация |
|---|---|---|---|---|
| L-1 | Источник заблокирован гео (SoundCloud/Deezer в РФ) | Высокая | Высокое | Прокси SOCKS5 обязателен в настройках; проверка доступности при старте; явная диагностика |
| L-2 | `client_id` SoundCloud протухает/меняется | Средняя | Среднее | Не завязываться на него: embed-плеер работает без него; `client_id` — опция |
| L-3 | Bandcamp меняет разметку `data-tralbum` | Средняя | Среднее | Парсер изолирован + golden-тесты на сохранённых HTML; fallback на mobile API (JSON, стабильнее) |
| L-4 | Лимиты/антифлуд Deezer/iTunes/MB | Средняя | Среднее | Rate-limiter, кэш, backoff, отслеживание 429 |
| L-5 | Подписанный `preview` URL протухает | Высокая | Низкое | Обновлять URL перед каждым воспроизведением, кэш ≤10 мин |
| L-6 | Пользователь ждёт lossless/полных треков, а получает 30 с | Высокая | Высокое | Честный UI + бейджи + приоритет источников с полным треком; **и явное предупреждение в README/UI: «для lossless — вариант A (P2P)»** |
| L-7 | Юридические претензии к обходу ToS | Низкая | Высокое | R-1…R-5: только публичные API, официальный embed, никакого массового скачивания |
| L-8 | Bandcamp-гео-блок на странице альбома | Средняя | Среднее | Проверка ответа; при стене — переход на mobile API по прямой ссылке |
| L-9 | Качество каталога ниже, чем у стриминговых сервисов РФ | Средняя | Среднее | Слияние с MusicBrainz, локальный FTS-поиск, кэш |
| L-10 | Рост сложности из-за 5 разных контрактов | Средняя | Среднее | Единый интерфейс `Source` + слой склейки; каждый адаптер ≤ 300 строк |

---

## 12. Правовые и этические аспекты

- Приложение использует **публичные HTTP-эндпоинты** и **официальный плеер**; обхода DRM нет.
- Формат Bandcamp-аудио (`.mp3`/`.flac` по прямым ссылкам) — это публичные файлы, которые Bandcamp сам отдаёт браузеру; скачивание ограничено теми треками, где артист разрешил free-download.
- Deezer/iTunes: только 30-секундные превью, которые сервисы отдают для этого purpose.
- SoundCloud: воспроизведение через официальный встроенный плеер; скачивание не реализовано.
- Пользователь отвечает за соблюдение ToS сервисов и законодательства своей страны. Приложение не содержит контента, не продаёт и не распространяет его.

---

## 13. Сравнение с вариантом A (P2P)

| Критерий | Вариант A (P2P, rutracker) | Вариант B (этот документ) |
|---|---|---|
| Каталог | ✅ rutracker-разделы + рекомендации | ✅ Deezer/iTunes/MB + склейка; SC/BC частично |
| Качество | ✅✅ lossless (FLAC 24/96, DSD) | ❌ lossless только за покупку на Bandcamp |
| Полные треки | ✅ всегда (если есть сиды) | ⚠️ SC/BC — да; Deezer/iTunes — 30 с |
| Полная дискография | ✅ одной раздачей | ❌ |
| Скачивание | ✅ трек/альбом/дискография | ⚠️ только Bandcamp (free/купленное) |
| Скорость старта | 2–10 с (зависит от пиров) | < 1 с (HTTP) |
| Износ SSD | 0 в режиме RAM | 0 (кроме кэша превью, опционально) |
| Риски | announce за CF, ratio, аккаунт, приватный трекер | 401/403 у сервисов, лимиты, гео-блок, ToS |
| Срок | 37–51 день | **6–12 дней** |
| Устойчивость к отсутствию контента | средняя (0 сидеров) | средняя (источник недоступен) |
| Цензура | минимальная (архивы, underground) | средняя (глобальные сервисы модерируют экстремист. контент) |

**Рекомендация:** реализовать вариант B как **независимый, быстрый и безопасный модуль**, а вариант A — как его расширение (адаптер `RutTrackerSource`), если Phase 0 варианта A подтвердит работоспособность announce. Тогда пользователь получает: «быстрый каталог и превью всегда» + «lossless-дискографии, когда есть раздача с сидами».

---

## 14. Как добавить вариант A (P2P) в этот код

Проект спроектирован так, что добавление P2P — это **один новый пакет + один адаптер**, без изменений в UI/каталоге/плеере:

```
internal/source/rutracker/     ← новый адаптер (реализует интерфейс Source)
  ├── client.go        поиск/разбор темы (из основного ТЗ §6)
  ├── torrent.go       обёртка anacrolix/torrent (основной ТЗ §7)
  ├── mediaindex.go    построение track plan (основной ТЗ §5, §8)
  └── stream.go        реализация Source.Stream → io.ReadCloser (reader по нужным piece'ам)

Изменения в существующих модулях:
  internal/source/manager.go   — регистрация адаптера (1 строка)
  internal/player/native.go    — уже умеет играть io.Reader → подходит без изменений
  internal/catalog/merge.go    — добавить норм-ключ rutracker (artist|album|title)
  internal/ui/search.go        — источник появляется автоматически из Capabilities()
```

**Что при этом НЕ меняется:** UI, каталог, склейка, плейлист, настройки прокси, локальная библиотека, сборка/установщики.

**Оценка:** +8–12 дней поверх варианта B (упрощённый вход: «поиск по треку → лучшая раздача → стрим»), полный вариант A — по основному ТЗ.

---

## 15. Приложения

### Приложение A. Проверенные примеры ответов

**Deezer — поиск**
```json
{"data":[{"id":1483825282,"readable":true,"title":"Nothing Else Matters (Remastered 2021)",
  "title_short":"Nothing Else Matters","isrc":"QMKHM1900008","duration":238,
  "explicit_lyrics":false,"link":"https://www.deezer.com/track/1483825282",
  "preview":"https://cdnt-preview.dzcdn.net/api/1/1/b/9/c/0/….mp3?hdnea=exp=…~hmac=…"}],
 "total":1}
```

**iTunes — поиск**
```json
{"resultCount":2,"results":[{"wrapperType":"track","kind":"song","trackId":…,
  "trackName":"Let Down","artistName":"Radiohead","collectionName":"…",
  "trackTimeMillis":298000,"trackExplicitness":"notExplicit",
  "previewUrl":"https://audio-ssl.itunes.apple.com/itunes-assets/AudioPreview221/v4/…/mzaf_….plus.aac.p.m4a",
  "artworkUrl100":"https://is1-ssl.mzstatic.com/image/thumb/…/200x200bb.jpg"}]}
```

**Bandcamp — mobile API**
```json
{"title":"OK Computer OKNOTOK 1997 2017","artist":"Radiohead","band_id":3957198221,
 "tracks":[{"track_id":…,"title":"Airbag (Remastered)","track_num":1,"duration":283.8,
   "streaming_url":"","is_streamable":false,"is_set_price":false,"price":1.29,
   "has_digital_download":true,"art_id":…,"album_id":3293551680,
   "band_name":"Radiohead","album_title":"OK Computer OKNOTOK 1997 2017",
   "track_url":"https://radiohead.bandcamp.com/track/…"}]}
```

**SoundCloud — oEmbed**
```json
{"version":1.0,"type":"rich","provider_name":"SoundCloud","title":"Creep by Radiohead",
 "author_name":"Radiohead","author_url":"https://soundcloud.com/radiohead",
 "thumbnail_url":"https://i1.sndcdn.com/artworks-exnFAx4ErkK2-0-t500x500.jpg",
 "html":"<iframe … src=\"https://w.sndcdn.com/player/?visual=true&url=https%3A%2F%2Fapi.soundcloud.com%2Ftracks%2F265069081&show_artwork=true\"></iframe>"}
```

### Приложение B. Чек-лист ручного теста MVP

```
[ ] Поиск «radiohead» → результаты из Deezer + iTunes, слияние в кластеры
[ ] Превью Deezer играет 30 с, бейдж «превью 30 с»
[ ] Превью iTunes (AAC) декодируется и играет
[ ] Прямая ссылка на Bandcamp-альбом → треклист из data-tralbum
[ ] Bandcamp-альбом со streamable-треком → полный трек играет
[ ] Платный Bandcamp-трек → цена, кнопка «Купить», превью если есть
[ ] Прямая ссылка на SoundCloud-трек → oEmbed-метаданные, embed-плеер играет полный трек
[ ] Склейка: трек, найденный в Deezer и SoundCloud, → одна карточка с двумя вариантами
[ ] Скачивание Bandcamp free-download → файл + теги + офлайн-воспроизведение
[ ] Прокси SOCKS5 → весь трафик через прокси (проверка логом)
[ ] Приватный режим → после закрытия БД и кэша пусто
[ ] Оффлайн → приложение открывается, локальные треки играют
[ ] Источник вернул 401/403/429 → понятное сообщение, без падения
```

### Приложение C. Источники и референсы

- Живые проверки API: `api.deezer.com`, `itunes.apple.com/search`, `musicbrainz.org/ws/2`, `bandcamp.com/api/mobile/24`, `soundcloud.com/oembed` (21.10.2026).
- Bandcamp: формат `data-tralbum` и поля `trackinfo[]` подтверждены на реальной странице `radiohead.bandcamp.com/album/ok-computer-oknotok-1997-2017`.
- Go-библиотеки: `github.com/wailsapp/wails/v2`, `github.com/faiface/beep`, `github.com/hajimehoshi/go-mp3`, `github.com/go-rod/rod`, `modernc.org/sqlite`, `github.com/PuerkitoBio/goquery`, `golang.org/x/net/proxy`, `golang.org/x/net/html/charset`.
- Документ согласуется с `ТЗ_Rutracker_Music_Streamer.md` (вариант A) — особенно §5 (поиск трека в торренте), §13 (транспорт/прокси), §23 (альтернативы).

---

## Приложение D. Итог для заказчика

1. **Вариант B реализуем за 6–12 дней** и сразу даёт: рабочий каталог, поиск, воспроизведение (полные треки там, где источник их отдаёт), плейлист и офлайн-библиотеку скачанного.
2. **Ключевой честный вывод:** без P2P **не будет** lossless-каталога и полных дискографий — это 80 % исходной ценности задачи. Вариант B закрывает другую половину: быстрый, стабильный, «без рисков» доступ к каталогу и к нецензурируемому контенту SoundCloud/Bandcamp.
3. **Проект не тупиковый:** архитектура допускает P2P-расширение одним адаптером (+8–12 дней, §14) без переписывания UI и плеера.
4. **Рекомендуемая последовательность:** (1) реализовать вариант B — получить рабочее приложение за неделю; (2) параллельно провести Phase 0 варианта A (3–5 дней); (3) при успехе — встроить P2P-адаптер и получить lossless-дискографии.
