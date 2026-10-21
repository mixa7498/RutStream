# Сборка и запуск RutStream

> **Важно про окружение этой машины.** Включён **Windows Smart App Control**
> (`VerifiedAndReputablePolicyState = 1`): он блокирует запуск новых
> неподписанных исполняемых файлов — включая `go test` и даже `compile.exe`.
> Политику ОС обходить нельзя, поэтому **разработка и проверки выполняются в WSL**,
> а Windows-сборка используется только как артефакт для запуска у пользователя
> (после снятия блокировки или с подписью от публичного CA).

## 1. Требования

| Компонент | Версия | Зачем |
|---|---|---|
| Go | 1.24+ (проверено 1.27.3) | сборка |
| WSL2 (Ubuntu) | — | среда сборки и запуска тестов на этой машине |
| ffmpeg | 6/7 (опционально) | AAC/ALAC/APE/WavPack/DSD — их не умеет встроенный декодер |
| Браузер | Chrome или Edge | только для ручного импорта cookie (см. §5) |

## 2. Сборка и тесты в WSL (основной цикл)

```powershell
# автоматически: синхронизация проекта + сборка + тесты
powershell -ExecutionPolicy Bypass -File tools\sync-wsl.ps1
```

Вручную:

```powershell
$proj = "C:\Users\mixa7\Documents\RutStream\rutstream"

# 1) копируем исходники в WSL через tar-пайп (диски Windows в WSL не смонтированы)
cmd /c "tar -cf - --exclude=bin --exclude=.git -C ""$proj"" . | wsl.exe -d Ubuntu -e sh -lc ""mkdir -p /home/cholobobo/rutstream && tar -C /home/cholobobo/rutstream -xf -"""

# 2) сборка и тесты
wsl -d Ubuntu -e sh -lc "cd /home/cholobobo/rutstream && export PATH=$HOME/go/bin:$PATH GOPATH=$HOME/gopath GOMODCACHE=$HOME/gomod GOCACHE=$HOME/.cache/go-build GOPROXY=file://$HOME/gomod/cache/download GOSUMDB=off GOFLAGS=-mod=mod CGO_ENABLED=0 && go build -tags nodevice ./... && go test -tags nodevice ./..."
```

Подготовка окружения WSL (однократно):

```bash
# Go
curl -sSLo /tmp/go.tgz https://go.dev/dl/go1.27.3.linux-amd64.tar.gz
tar -C ~ -xzf /tmp/go.tgz && rm /tmp/go.tgz
export PATH=$HOME/go/bin:$PATH

# кэш модулей: на Windows это %TEMP%\opencode\gopath\pkg\mod\cache\download —
# его можно использовать как file-based GOPROXY
```

Тег `nodevice` отключает зависимость от звуковой карты (`oto` требует ALSA/ALSA-dev).
Без него сборка на Windows и macOS работает как есть.

## 3. Сборка приложения для Windows

```powershell
$env:GOPATH="$env:TEMP\opencode\gopath"; $env:GOMODCACHE="$env:GOPATH\pkg\mod"; $env:GOCACHE="$env:GOPATH\build-cache"
$go="$env:TEMP\opencode\go\bin\go.exe"      # либо просто `go`

cd C:\Users\mixa7\Documents\RutStream\rutstream
& $go build -ldflags "-s -w" -o bin\rutstream.exe ./cmd/rutstream
& $go build -ldflags "-s -w" -o bin\spike.exe     ./cmd/spike
```

> **Важно: `-ldflags "-s -w"` обязателен.** С полной таблицей символов Code Integrity
> отклоняет файл («did not meet the Enterprise signing level requirements»).

> На этой машине компилятор Go тоже блокируется политикой, поэтому Windows-сборку
> выполняйте на машине без Smart App Control либо в WSL с `GOOS=windows`:
> ```bash
> GOOS=windows GOARCH=amd64 go build -ldflags "-s -w" -o rutstream.exe ./cmd/rutstream
> ```

## 4. Переменные окружения

| Переменная | Назначение |
|---|---|
| `RUTSTREAM_USER` / `RUTSTREAM_PASS` | логин и пароль rutracker (вход по форме) |
| `RUTSTREAM_COOKIE` | готовая cookie `bb_session` (альтернатива логину) |
| `RUTSTREAM_PROXY` | `socks5://host:port` или `http://host:port` — и для сайта, и для P2P-пиров |
| `RUTSTREAM_DATA` | каталог данных (по умолчанию `%APPDATA%\RutStream`) |

## 5. Авторизация на rutracker

Форум закрыт Cloudflare-challenge для «не-браузерных» клиентов, поэтому два пути:

**Путь A — импорт cookie (рекомендуется).**
1. Войдите на rutracker в обычном браузере.
2. DevTools → Application → Cookies → `rutracker.org` → `bb_session` → скопируйте значение.
3. ```powershell
   $env:RUTSTREAM_COOKIE = "0-xxxxxxxx-xxxxxxxxxxxx"
   ```

**Путь B — вход по логину/паролю.**
```powershell
$env:RUTSTREAM_USER = "ваш_логин"
$env:RUTSTREAM_PASS = "ваш_пароль"
```
Если страница логина отдаёт challenge — используйте путь A.

## 6. Smart App Control: что делать, чтобы приложение запускалось

Диагностика (журнал Code Integrity):
```powershell
Get-WinEvent -LogName "Microsoft-Windows-CodeIntegrity/Operational" -MaxEvents 5 |
  Select-Object TimeCreated, Message
Get-ItemProperty "HKLM:\SYSTEM\CurrentControlSet\Control\CI\Policy" |
  Select-Object VerifiedAndReputablePolicyState
```

Варианты:

1. **Отключить Smart App Control** (нужны права администратора):
   «Параметры Windows» → «Безопасность Windows» → «Параметры защиты от вирусов и угроз» →
   «Управление приложениями и браузером» → «Управление приложениями» →
   «Управление приложениями Smart App Control» → «Выключить».
2. **Подписать приложение сертификатом публичного CA** — тогда SAC доверяет приложению
   автоматически на любой машине (нужна покупка OV/EV code signing сертификата).
   Самоподпись **не подходит**: SAC требует «Enterprise signing level», то есть цепочку
   до CA из программы Microsoft Trusted Root.
3. Проверять и тестировать в среде без этой политики (WSL/CI).

Самоподпись (полезна для «доверенного» запуска в профиле пользователя, но не для SAC):
```powershell
$cert = New-SelfSignedCertificate -Subject "CN=RutStream" -Type CodeSigningCert `
        -CertStoreLocation Cert:\CurrentUser\My -KeyExportPolicy Exportable
certutil -user -addstore Root "$env:TEMP\rutstream.cer"
Set-AuthenticodeSignature -FilePath bin\rutstream.exe -Certificate $cert -HashAlgorithm SHA256
```

## 7. Проверочный стенд (Phase 0)

```powershell
.\bin\spike.exe local           # H4: из раздачи 24 МБ скачивается только нужный файл (~2 МБ)
.\bin\spike.exe announce http://bt2.t-ru.org/ann   # H3: трекер отвечает bencode
.\bin\spike.exe feed 1760       # живой Atom-фид раздела (без авторизации)
.\bin\spike.exe search "beatles" 1698   # поиск (нужна сессия)
.\bin\spike.exe topic 6924752   # разбор темы
.\bin\spike.exe files 6924752   # список файлов раздачи
```

## 8. Приложение

```powershell
$env:RUTSTREAM_COOKIE = "<bb_session>"
.\bin\rutstream.exe forums                       # дерево разделов музыки
.\bin\rutstream.exe search beatles 1698          # поиск раздач
.\bin\rutstream.exe show <topicID>               # метаданные и треклист
.\bin\rutstream.exe plan <topicID>               # что и сколько скачается
.\bin\rutstream.exe play <topicID> 7             # потоковое воспроизведение трека №7
.\bin\rutstream.exe download <topicID> 7         # скачать трек целиком
.\bin\rutstream.exe history                      # история прослушиваний
```
