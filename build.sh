#!/bin/sh
# Сборка и подпись RutStream.
#
# Одна команда, без PowerShell и без внешних инструментов:
#
#   ./build.sh              — собрать и подписать (Windows)
#   ./build.sh --no-sign    — только собрать
#
# Требуется Go в PATH. Если Go установлен, но не найден — добавьте
# export PATH="/c/Program Files/Go/bin:$PATH" в ~/.bashrc.

set -e

cd "$(dirname "$0")/rutstream"

sign=1
for arg in "$@"; do
    case "$arg" in
        --no-sign) sign=0 ;;
    esac
done

version="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo 0.2.0)}"

# -H windowsgui: при двойном щелчке не открывается консоль.
# Версия попадает в журнал и в окно приложения.
echo "==> Сборка $version"
go build -ldflags "-s -w -H windowsgui -X main.version=$version" -o bin/rutstream.exe ./cmd/rutstream
echo "    bin/rutstream.exe готов"

if [ "$sign" -eq 0 ]; then
    exit 0
fi

# Подписать работающий файл нельзя (Windows держит его открытым), поэтому
# подписываем копию — процесс rutstream.exe к этому моменту уже завершён.
# Если сертификат уже создан, переиспользуем его: иначе каждый релиз получал
# бы новый отпечаток, и установленный у пользователей сертификат переставал
# бы подходить.
echo "==> Подпись"
cp bin/rutstream.exe bin/rutstream.tmp
if [ -f bin/rutstream-key.pfx ]; then
    ./bin/rutstream.exe sign --pfx bin/rutstream-key.pfx bin/rutstream.tmp
else
    ./bin/rutstream.exe sign bin/rutstream.tmp
fi
mv -f bin/rutstream.tmp bin/rutstream.exe

# SHA-256 для проверки скачанного файла.
if command -v sha256sum >/dev/null 2>&1; then
    (cd bin && sha256sum rutstream.exe > rutstream.exe.sha256)
fi

cat <<'EOF'

Готово: rutstream/bin/rutstream.exe

Это один самодостаточный файл: интерфейс, сервер и плеер внутри.
Просто запустите его двойным щелчком.

Сертификат (необязательно, только чтобы Windows не показывала предупреждение):
запустите cmd от администратора и выполните

  certutil -addstore Root "rutstream\bin\rutstream-cert.cer"
  certutil -addstore TrustedPublisher "rutstream\bin\rutstream-cert.cer"
EOF
