<#
.SYNOPSIS
  Синхронизирует исходники проекта в WSL (Ubuntu) для сборки и запуска тестов.

.DESCRIPTION
  На машине с Windows включён Smart App Control, который блокирует запуск
  собранных исполняемых файлов (включая тест-бинари Go). Поэтому разработка
  и проверка выполняются в WSL, где политика Windows не действует.

  Скрипт копирует проект через tar-пайп (диски Windows в WSL не смонтированы).

.EXAMPLE
  powershell -ExecutionPolicy Bypass -File tools\sync-wsl.ps1
#>
param(
    [string]$Distro = "Ubuntu",
    [string]$Remote = "/home/cholobobo/rutstream"
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot

Write-Host "Синхронизация $root -> $Distro`:$Remote"
cmd /c "tar -cf - --exclude=bin --exclude=.git -C `"$root`" . | wsl.exe -d $Distro -e sh -lc `"mkdir -p $Remote && tar -C $Remote -xf -`"" | Out-Null
if ($LASTEXITCODE -ne 0) { throw "не удалось скопировать проект в WSL" }
Write-Host "Готово."

Write-Host "Сборка и тесты в WSL:"
cmd /c "wsl.exe -d $Distro -e sh -lc `"cd $Remote && export PATH=\$HOME/go/bin:\$PATH GOPATH=\$HOME/gopath GOMODCACHE=\$HOME/gomod GOCACHE=\$HOME/.cache/go-build GOPROXY=file://\$HOME/gomod/cache/download GOSUMDB=off GOFLAGS=-mod=mod CGO_ENABLED=0 && go build -tags nodevice ./... && go test -tags nodevice ./...`"" | Out-Null
if ($LASTEXITCODE -ne 0) { Write-Host "Сборка или тесты завершились с ошибкой." } else { Write-Host "Тесты пройдены." }
