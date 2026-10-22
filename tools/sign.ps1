<#
.SYNOPSIS
    Подпись собранного EXE сертификатом для подписи кода.

.DESCRIPTION
    Скрипт создаёт (или находит) сертификат для подписи кода в хранилище
    текущего пользователя и подписывает им бинарник.

    Два режима:
      * самоподписанный сертификат — подпись снимает предупреждение
        «неизвестный издатель» на машинах, где сертификат установлен в
        «Доверенные корневые центры сертификации». Для распространения
        программы публично Smart App Control такой подписи недостаточно:
        нужен сертификат публичного удостоверяющего центра (см. BUILD.md).
      * свой PFX — если сертификат уже куплен (публичный CA).

.PARAMETER Exe
    Путь к подписываемому файлу.

.PARAMETER CertName
    Имя (CN) сертификата в хранилище CurrentUser\My.

.PARAMETER PfxPath
    PFX-файл с паролем (для сертификата публичного CA). Если задан,
    сертификат импортируется в хранилище и используется он.

.PARAMETER Password
    Пароль PFX (в интерактивном режиме спросится).

.EXAMPLE
    powershell -ExecutionPolicy Bypass -File tools\sign.ps1 -Create

.EXAMPLE
    powershell -ExecutionPolicy Bypass -File tools\sign.ps1 -PfxPath cert.pfx
#>
[CmdletBinding()]
param(
    [string]$Exe = "rutstream\bin\rutstream.exe",
    [string]$CertName = "RutStream",
    [string]$PfxPath = "",
    [string]$Password = "",
    [switch]$Create
)

$ErrorActionPreference = "Stop"

function Write-Step($msg) { Write-Host "==> $msg" -ForegroundColor Cyan }

if (-not (Test-Path $Exe)) {
    Write-Error "Файл не найден: $Exe (сначала соберите: go build -ldflags '-s -w' -o bin\rutstream.exe ./cmd/rutstream)"
}

$store = "Cert:\CurrentUser\My"
$cert = $null

if ($PfxPath) {
    Write-Step "Импорт сертификата из $PfxPath"
    if (-not (Test-Path $PfxPath)) { Write-Error "PFX не найден: $PfxPath" }
    if ($Password) {
        $cert = Import-PfxCertificate -FilePath $PfxPath -CertStoreLocation $store -Password (ConvertTo-SecureString $Password -AsPlainText -Force)
    } else {
        $cert = Import-PfxCertificate -FilePath $PfxPath -CertStoreLocation $store -Exportable:$false
    }
    Write-Step "Импортирован: $($cert.Subject)"
} else {
    $cert = Get-ChildItem $store | Where-Object {
        $_.Subject -like "CN=$CertName*" -and $_.HasPrivateKey -and $_.NotAfter -gt (Get-Date)
    } | Select-Object -First 1
    if (-not $cert -and $Create) {
        Write-Step "Создание самоподписанного сертификата для подписи кода"
        $cert = New-SelfSignedCertificate `
            -Type CodeSigningCert `
            -Subject "CN=$CertName, O=RutStream" `
            -CertStoreLocation $store `
            -KeyAlgorithm RSA `
            -KeyLength 3072 `
            -HashAlgorithm SHA256 `
            -KeyExportPolicy Exportable `
            -KeyUsage DigitalSignature `
            -NotAfter (Get-Date).AddYears(5)
        Write-Step "Создан: $($cert.Thumbprint)"
    }
    if (-not $cert) {
        Write-Error "Сертификат «$CertName» не найден. Создайте его ключом -Create или укажите -PfxPath."
    }
}

Write-Step "Подпись: $Exe"
# Метка времени: подпись остаётся действительной после истечения сертификата.
$timestamp = "http://timestamp.digicert.com"
$sig = Set-AuthenticodeSignature -FilePath $Exe -Certificate $cert `
    -HashAlgorithm SHA256 -TimestampServer $timestamp

if ($sig.Status -ne "Valid") {
    Write-Warning "Статус подписи: $($sig.Status) — $($sig.StatusMessage)"
} else {
    Write-Step "Подпись действительна" -ForegroundColor Green
}
Write-Host "Издатель:    $($sig.SignerCertificate.Subject)"
Write-Host "Действует до: $($sig.SignerCertificate.NotAfter)"
Write-Host "Отпечаток:   $($sig.SignerCertificate.Thumbprint)"

# Публичная часть сертификата — её ставят на другие машины, чтобы
# приложение запускалось без предупреждения.
$cerPath = Join-Path (Split-Path -Parent $Exe) "rutstream-cert.cer"
Export-Certificate -Cert $cert -FilePath $cerPath -Type CERT -Force | Out-Null
Write-Step "Сертификат для установки на другие машины: $cerPath"

Write-Host ""
Write-Host "Установка сертификата (PowerShell от администратора):" -ForegroundColor Yellow
Write-Host "  Import-Certificate -FilePath `"$cerPath`" -CertStoreLocation Cert:\LocalMachine\Root"
Write-Host "  Import-Certificate -FilePath `"$cerPath`" -CertStoreLocation Cert:\LocalMachine\TrustedPublisher"
Write-Host ""
Write-Host "Проверка подписи: Get-AuthenticodeSignature `"$Exe`" | Format-List *"
