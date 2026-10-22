#!/bin/sh
# Публикация RutStream на GitHub: репозиторий, тег, релиз с подписанным бинарником.
#
# Требуется GitHub CLI (https://cli.github.com) с авторизацией:
#   gh auth login --web
#
# Использование:
#   tools/publish.sh [версия]        # по умолчанию берётся последний тег

set -e

cd "$(dirname "$0")/.."

version="${1:-}"
if [ -z "$version" ]; then
    version="$(git describe --tags --abbrev=0)"
fi

if ! command -v gh >/dev/null 2>&1; then
    echo "Не найден GitHub CLI. Установите: https://cli.github.com" >&2
    exit 1
fi

echo "==> Сборка и подпись"
VERSION="$version" ./build.sh

echo "==> Коммит и тег $version"
git add -A
if ! git diff --cached --quiet; then
    git commit -m "Релиз $version"
fi
if git rev-parse "$version" >/dev/null 2>&1; then
    echo "    тег $version уже есть — пропускаем"
else
    git tag -a "$version" -m "$version"
fi
git push origin HEAD
git push origin "$version"

echo "==> Релиз"
repo="$(gh repo view --json nameWithOwner --jq .nameWithOwner)"
gh release create "$version" \
    rutstream/bin/rutstream.exe \
    rutstream/bin/rutstream.exe.sha256 \
    rutstream/bin/rutstream-cert.cer \
    --repo "$repo" \
    --title "RutStream $version" \
    --notes "Сборка $version.

Проверка целостности (cmd, без PowerShell):

    certutil -hashfile rutstream.exe SHA256

Сверьте вывод с файлом rutstream.exe.sha256.

Подробности — в README.md."

echo
echo "Готово: https://github.com/$repo/releases/tag/$version"
