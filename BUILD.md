# Сборка и публикация RutStream

RutStream собирается в **один самодостаточный `.exe`**: внутри интерфейс,
локальный сервер, плеер и все ресурсы. Никаких PowerShell-скриптов и внешних
сборочных инструментов не требуется.

## 1. Требования

| Компонент | Версия | Зачем |
|---|---|---|
| Windows | 10/11 x64 | целевая платформа |
| WebView2 Runtime | любая | окно приложения (в Windows 11 уже есть) |
| Go | 1.27+ | только для сборки из исходников |
| Edge или Chrome | любая | скрытый headless-браузер для проверки Cloudflare |
| Git Bash | — | запуск `build.sh` (или используйте `make`) |

Готовый `rutstream.exe` лежит в [Releases](https://github.com/mixa7498/RutStream/releases) —
собирать ничего не нужно.

## 2. Сборка

```sh
./build.sh              # собрать и подписать
./build.sh --no-sign    # только собрать
```

или:

```sh
make sign               # сборка + подпись
make build              # только сборка
make test               # тесты
make vet                # статический анализ
```

Результат — `rutstream/bin/rutstream.exe`. Это один файл; рядом появляются
только материалы подписи (`rutstream-cert.cer`, `rutstream-key.pfx`), без которых
приложение всё равно работает.

Особенности сборки:

* `-H windowsgui` — при двойном щелчке консоль не открывается. При запуске из
  cmd/PowerShell приложение само подключается к родительской консоли, поэтому
  команды `check`, `search` и другие по-прежнему печатают вывод.
* `-s -w` — обязательны: с полной таблицей символов Windows Code Integrity
  может отклонить файл.
* Версия передаётся через `-X main.version=…` и попадает в журнал.

Кросс-сборка из Linux/macOS:

```sh
make windows
```

## 3. Тесты

```sh
cd rutstream
go test ./...
go test -race ./...
go build -tags nodevice ./...   # без звуковой карты (CI, Linux)
```

## 4. Авторизация

Пароль приложению не нужен. Сессия вводится **в окне приложения**:

1. Войдите на `rutracker.org` в обычном браузере.
2. `F12` → **Application** → **Cookies** → `https://rutracker.org` → строка
   `bb_session` → скопируйте **Value** (вида `0-12345678-AbCdEf…`).
3. В приложении нажмите **«Сессия»**, вставьте только это значение и сохраните.

`bb_session` помечена HttpOnly, поэтому в `document.cookie` её нет. Сессия
хранится в `%APPDATA%\RutStream\session.cookie` и никуда не отправляется.
Для автоматизации можно задать `RUTSTREAM_COOKIE` — значение `bb_session`
(для совместимости принимается и полная строка cookie).

## 5. Проверка доступа

```sh
rutstream.exe check
```

Команда показывает зеркало, наличие `bb_session` и `cf_clearance`, авторизацию,
чтение темы, поиск и состояние скрытого браузера. Журнал приложения:
`%APPDATA%\RutStream\logs\rutstream.log`.

## 6. Как работает обход Cloudflare

Cloudflare проверяет не только cookie, но и TLS/JS-отпечаток клиента. Поэтому:

1. Пока сайт отвечает напрямую, приложение работает обычным HTTP-клиентом —
   браузер не запускается вообще.
2. При первом challenge приложение незаметно запускает **headless Edge**
   (`--headless=new`) с отдельным профилем в `%APPDATA%\RutStream\browser`.
   Окна и значка в панели задач нет.
3. Браузеру подставляется обычный User-Agent (без `HeadlessChrome`), получает
   `cf_clearance`, cookie переносятся в HTTP-клиент. Браузер закрывается вместе
   с приложением.

Отладка:

* `RUTSTREAM_BROWSER_VISIBLE=1` — показать окно браузера (например, если сайт
  просит капчу).
* `RUTSTREAM_NO_BROWSER=1` — вообще не запускать браузер.
* `RUTSTREAM_CDP_PORT=9222` — порт отладки.

## 7. Подпись

```sh
./build.sh
```

`build.sh` создаёт самоподписанный сертификат и подписывает копию бинарника
(работающий файл Windows подписать не даёт). Установка сертификата — один раз,
из cmd от администратора:

```bat
certutil -addstore Root "rutstream\bin\rutstream-cert.cer"
certutil -addstore TrustedPublisher "rutstream\bin\rutstream-cert.cer"
```

Для подписи сертификатом публичного CA:

```sh
rutstream.exe sign --pfx cert.pfx --password "…" rutstream.exe
```

Smart App Control доверяет только цепочке публичного CA; самоподпись снимает
предупреждение «неизвестный издатель» там, где сертификат установлен вручную.
Для open-source проекта допустим вариант без подписи: релиз + SHA-256.

## 8. Публикация на GitHub

```sh
gh auth login --web     # один раз
tools/publish.sh v0.2.0
```

Скрипт собирает и подписывает exe, коммитит изменения, ставит тег, отправляет
ветку и тег, создаёт GitHub Release с `rutstream.exe`, `rutstream.exe.sha256`
и `rutstream-cert.cer`.

Проверка скачанного файла без PowerShell:

```bat
certutil -hashfile rutstream.exe SHA256
```

Сверьте вывод с содержимым `rutstream.exe.sha256` из релиза.

## 9. Диагностика

* Приложение не запускается — проверьте, установлен ли WebView2 Runtime:
  <https://developer.microsoft.com/microsoft-edge/webview2/>
* Нет авторизации — обновите cookie в окне «Сессия»; проверьте `rutstream.exe check`.
* Поиск/темы не открываются — посмотрите `%APPDATA%\RutStream\logs\rutstream.log`;
  при необходимости запустите с `RUTSTREAM_BROWSER_VISIBLE=1`.
* Smart App Control блокирует exe — подпишите его или отключите политику в
  «Параметры Windows → Безопасность Windows».
