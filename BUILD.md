# Сборка и запуск RutStream

## 1. Требования

| Компонент | Версия | Зачем |
|---|---|---|
| Go | 1.24+ (проверено на 1.27.3) | сборка |
| ffmpeg | 6/7 (опционально) | AAC/ALAC/APE/WavPack/DSD, которые не умеет встроенный декодер |
| Браузер | Chrome или Edge | только для ручного импорта cookie (см. §4) |

## 2. Сборка

```powershell
# Go (если не установлен в системе)
$env:GOPATH="$env:TEMP\opencode\gopath"
$env:GOMODCACHE="$env:GOPATH\pkg\mod"
$env:GOCACHE="$env:GOPATH\build-cache"
$go="$env:TEMP\opencode\go\bin\go.exe"   # либо просто `go`

cd C:\Users\mixa7\Documents\RutStream\rutstream

# Основное приложение
& $go build -ldflags "-s -w" -o bin\rutstream.exe ./cmd/rutstream
# Стенд проверок Phase 0
& $go build -ldflags "-s -w" -o bin\spike.exe ./cmd/spike
```

> **Важно: `-ldflags "-s -w"` обязателен.** Без удаления таблицы символов
> Windows Smart App Control / Code Integrity может отклонить исполняемый файл
> (см. §5). Собранные с этими флагами бинарники запускаются штатно.

## 3. Переменные окружения

| Переменная | Назначение |
|---|---|
| `RUTSTREAM_USER` / `RUTSTREAM_PASS` | логин и пароль rutracker (для входа по форме) |
| `RUTSTREAM_COOKIE` | готовая cookie `bb_session` (альтернатива логину) |
| `RUTSTREAM_PROXY` | `socks5://host:port` или `http://host:port` — для сайта и для P2P-пиров |
| `RUTSTREAM_DATA` | каталог данных (по умолчанию `%APPDATA%\RutStream`) |

## 4. Авторизация на rutracker

Форум закрыт Cloudflare-challenge для «не-браузерных» клиентов, поэтому
предусмотрены два пути:

**Путь A — импорт cookie (рекомендуется).**
1. Войдите на rutracker в обычном браузере.
2. Откройте DevTools → Application → Cookies → `rutracker.org` → `bb_session`.
3. Скопируйте значение и передайте его:
   ```powershell
   $env:RUTSTREAM_COOKIE = "0-xxxxxxxx-xxxxxxxxxxxx"
   ```

**Путь B — вход по логину/паролю.**
```powershell
$env:RUTSTREAM_USER = "ваш_логин"
$env:RUTSTREAM_PASS = "ваш_пароль"
```
Если страница логина отдаёт challenge — используйте путь A.

## 5. Windows Smart App Control (важно)

На машине разработки включён **Smart App Control**
(`HKLM\SYSTEM\CurrentControlSet\Control\CI\Policy\VerifiedAndReputablePolicyState = 1`).
Он блокирует запуск **новых неподписанных** исполняемых файлов:

```
Code Integrity determined that a process ... attempted to load ...\probe2.exe
that did not meet the Enterprise ... policy
```

Наблюдаемое поведение:
- бинарники, **уже запускавшиеся** ранее, продолжают работать (их хеш попал в кэш репутации);
- **любая новая сборка** (даже «hello world») блокируется;
- блокировка касается и `go test` (запуск тест-бинаря).

Это политика безопасности ОС, и обходить её не следует. Варианты:

1. **Отключить Smart App Control** (нужны права администратора):
   «Параметры Windows» → «Безопасность Windows» → «Защита от вирусов и угроз» →
   «Параметры защиты от вирусов и угроз» → «Управление приложениями и браузером» →
   «Управление приложениями» → «Управление приложениями Smart App Control» → «Выключить».
   Либо из PowerShell **от администратора**:
   ```powershell
   Set-MpPreference -EnableSmartAppControl Off   # не все сборки Windows это поддерживают
   ```
2. **Подписать приложение** доверенным сертификатом — тогда оно запускается
   на любой машине с SAC (см. §6).
3. Собирать и тестировать в среде без этой политики (WSL/Docker/CI).

## 6. Подпись приложения (для релиза)

```powershell
# Самоподписанный сертификат (для личного использования).
# Импорт сертификата в «Доверенные корневые центры» требует прав администратора.
$cert = New-SelfSignedCertificate -Subject "CN=RutStream" -Type CodeSigningCert `
        -CertStoreLocation Cert:\CurrentUser\My

signtool sign /sha1 $cert.Thumbprint /fd SHA256 /tr http://timestamp.digicert.com `
        /td SHA256 bin\rutstream.exe
```

Для публичного релиза — сертификат от публичного CA (тогда SAC доверяет приложению
автоматически) либо упаковка в MSIX с подписью.

## 7. Проверочный стенд (Phase 0)

```powershell
.\bin\spike.exe local           # H4: из раздачи 24 МБ скачивается только нужный файл (~2 МБ)
.\bin\spike.exe announce http://bt2.t-ru.org/ann   # H3: трекер отвечает bencode
.\bin\spike.exe feed 1760       # живой Atom-фид раздела (без авторизации)
.\bin\spike.exe search "beatles" 1698   # поиск (нужна сессия)
.\bin\spike.exe topic 6924752   # разбор темы
.\bin\spike.exe files 6924752   # список файлов раздачи
```

## 8. Тесты

```powershell
& $go test -ldflags "-s -w" ./...
```

Если сборка тестов блокируется политикой — запускайте уже собранный
тест-бинарь:

```powershell
& $go test -c -ldflags "-s -w" -o bin\rt.test.exe ./internal/rutracker
.\bin\rt.test.exe "-test.v=true"
```
