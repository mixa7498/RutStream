<#
.SYNOPSIS
    Публикация RutStream на GitHub: репозиторий, релиз с подписанным EXE и SHA-256.

.DESCRIPTION
    Скрипт:
      1. проверяет, что gh (GitHub CLI) установлен и авторизован;
      2. создаёт репозиторий (или добавляет remote, если он уже есть);
      3. отправляет ветку с тегами;
      4. собирает и подписывает EXE;
      5. создаёт релиз с EXE и файлом SHA-256.

.EXAMPLE
    powershell -ExecutionPolicy Bypass -File tools\publish.ps1 -Name RutStream -Visibility public
#>
[CmdletBinding()]
param(
    [string]$Name = "RutStream",
    [ValidateSet("public", "private")]
    [string]$Visibility = "public",
    [string]$Version = "v0.1.0",
    [string]$Notes = "",
    [switch]$SkipRelease
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot

function Write-Step($msg) { Write-Host "==> $msg" -ForegroundColor Cyan }

# --- 1. GitHub CLI ---------------------------------------------------------
if (-not (Get-Command gh -ErrorAction SilentlyContinue)) {
    Write-Error "Не найден gh (GitHub CLI). Установите: winget install --id GitHub.cli --exact"
}
$auth = gh auth status 2>&1
if ($LASTEXITCODE -ne 0) {
    Write-Error "gh не авторизован. Выполните один раз: gh auth login --web"
}
Write-Step "GitHub CLI авторизован: $((gh api user --jq .login))"

# --- 2. Репозиторий --------------------------------------------------------
Push-Location $root
try {
    $existing = git remote get-url origin 2>$null
    if ($existing) {
        Write-Step "Remote origin уже есть: $existing"
    } else {
        Write-Step "Создание репозитория $Name ($Visibility)"
        gh repo create $Name --$Visibility --source=. --remote=origin `
            --description "Музыка с rutracker.org через BitTorrent: каталог, потоковое воспроизведение и загрузка" | Out-Null
    }

    # --- 3. Отправка ветки и тегов -----------------------------------------
    git add -A
    if (git status --porcelain) {
        Write-Step "Есть незакоммиченные изменения — коммитим"
        git commit -m "Публикация $Version"
    }
    Write-Step "Отправка в origin"
    git push -u origin HEAD
    if (git tag | Select-String -SimpleMatch $Version) {
        git push origin $Version
    } else {
        git tag -a $Version -m $Version
        git push origin $Version
    }
} finally {
    Pop-Location
}

if ($SkipRelease) {
    Write-Step "Релиз пропущен (ключ -SkipRelease)"
    exit 0
}

# --- 4. Сборка и подпись ----------------------------------------------------
$proj = Join-Path $root "rutstream"
$exe = Join-Path $proj "bin\rutstream.exe"
Write-Step "Сборка"
Push-Location $proj
try {
    go build -ldflags "-s -w" -o bin\rutstream.exe ./cmd/rutstream
} finally {
    Pop-Location
}

Write-Step "Подпись"
$signScript = Join-Path $root "tools\sign.ps1"
powershell -ExecutionPolicy Bypass -File $signScript -Exe $exe | Out-Null

$hash = (Get-FileHash -Algorithm SHA256 $exe).Hash.ToLower()
$hashFile = "$exe.sha256"
"$hash  rutstream.exe" | Set-Content -Path $hashFile -Encoding ASCII
Write-Step "SHA-256: $hash"

# --- 5. Релиз --------------------------------------------------------------
if (-not $Notes) {
    $Notes = @"
Сборка $Version.

Приложение для музыки с rutracker.org: каталог, потоковое воспроизведение
и загрузка через BitTorrent. Качается только выбранный трек, а не раздача целиком.

Проверка подписи файла:

    (Get-FileHash -Algorithm SHA256 rutstream.exe).Hash.ToLower() -eq "$hash"

При первом запуске приложение само откроет Edge с отладкой — один раз пройдите
в нём проверку Cloudflare. Подробности — в README.md.
"@
}

Write-Step "Создание релиза $Version"
Push-Location $root
try {
    gh release create $Version $exe $hashFile --title "RutStream $Version" --notes $Notes
} finally {
    Pop-Location
}

Write-Step "Готово: https://github.com/$((gh api user --jq .login))/$Name/releases/tag/$Version"
