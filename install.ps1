# Instalador de MKD para Windows.
#
#   irm https://raw.githubusercontent.com/Yak036/mkd/main/install.ps1 | iex
#
# Instala el último mkd.exe en %LOCALAPPDATA%\mkd y lo añade al PATH del usuario.
# No requiere permisos de administrador.
#
# Variables de entorno:
#   MKD_VERSION       versión a instalar (por defecto: la última)
#   MKD_INSTALL_DIR   directorio destino (por defecto %LOCALAPPDATA%\mkd)
#   MKD_REPO          repositorio origen (por defecto Yak036/mkd)

[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$Repo    = if ($env:MKD_REPO) { $env:MKD_REPO } else { 'Yak036/mkd' }
$Version = $env:MKD_VERSION
$Bin     = 'mkd.exe'

function Write-Info($Message) { Write-Host ":: $Message" -ForegroundColor Cyan }
function Write-Warn($Message) { Write-Host "!! $Message" -ForegroundColor Yellow }
function Fail($Message) { Write-Host "xx $Message" -ForegroundColor Red; exit 1 }

# --- Arquitectura -------------------------------------------------------------
$arch = switch ($env:PROCESSOR_ARCHITECTURE) {
    'AMD64' { 'amd64' }
    'ARM64' { 'arm64' }
    'x86'   { Fail 'Windows de 32 bits no está soportado.' }
    default { Fail "arquitectura no soportada: $env:PROCESSOR_ARCHITECTURE" }
}
Write-Info "plataforma detectada: windows/$arch"

# --- TLS moderno (Windows PowerShell 5.1 puede venir con TLS 1.0) ------------
try {
    [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
} catch {
    Write-Warn 'no se pudo forzar TLS 1.2; continuando'
}

# --- Última versión -----------------------------------------------------------
if (-not $Version) {
    Write-Info 'consultando la última versión…'
    try {
        $release = Invoke-RestMethod -UseBasicParsing `
            -Uri "https://api.github.com/repos/$Repo/releases/latest"
        $Version = $release.tag_name
    } catch {
        Fail "no se pudo consultar GitHub: $($_.Exception.Message)"
    }
}
$Version = $Version.TrimStart('v')
Write-Info "versión: v$Version"

# --- Descarga -----------------------------------------------------------------
$assetName = "mkd_windows_$arch.zip"
$url       = "https://github.com/$Repo/releases/download/v$Version/$assetName"
$tempZip   = Join-Path ([IO.Path]::GetTempPath()) $assetName
$tempDir   = Join-Path ([IO.Path]::GetTempPath()) ("mkd-" + [Guid]::NewGuid().ToString('N'))

Write-Info "descargando $url"
try {
    Invoke-WebRequest -UseBasicParsing -Uri $url -OutFile $tempZip
} catch {
    Fail "descarga fallida (¿existe v$Version para windows/$arch?): $($_.Exception.Message)"
}

# Verificar checksum si está disponible en la release.
try {
    $sumsUrl = "https://github.com/$Repo/releases/download/v$Version/checksums.txt"
    $sums = (Invoke-WebRequest -UseBasicParsing -Uri $sumsUrl).Content
    $expected = ($sums -split "`n" | Where-Object { $_ -match [regex]::Escape($assetName) } | ForEach-Object { ($_ -split '\s+')[0] })
    if ($expected) {
        $actual = (Get-FileHash -Algorithm SHA256 -Path $tempZip).Hash.ToLower()
        if ($actual -ne $expected.ToLower()) {
            Remove-Item $tempZip -Force -ErrorAction SilentlyContinue
            Fail "checksum no coincide (esperado $expected, obtenido $actual)"
        }
        Write-Info 'checksum verificado'
    }
} catch {
    Write-Warn 'no se pudo verificar el checksum; continuando'
}

# --- Instalación --------------------------------------------------------------
$DestDir = if ($env:MKD_INSTALL_DIR) { $env:MKD_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'mkd' }

New-Item -ItemType Directory -Force -Path $tempDir | Out-Null
try {
    Expand-Archive -Path $tempZip -DestinationPath $tempDir -Force
    $exe = Join-Path $tempDir $Bin
    if (-not (Test-Path $exe)) { Fail "el zip no contiene $Bin" }

    New-Item -ItemType Directory -Force -Path $DestDir | Out-Null
    $target = Join-Path $DestDir $Bin

    # Si el binario está en uso (mkd abierto), Windows bloquea la escritura.
    if (Test-Path $target) {
        try {
            Remove-Item $target -Force
        } catch {
            Fail "no se pudo reemplazar $target (¿tienes mkd abierto?)"
        }
    }
    Copy-Item $exe $target -Force
} finally {
    Remove-Item $tempZip -Force -ErrorAction SilentlyContinue
    Remove-Item $tempDir -Recurse -Force -ErrorAction SilentlyContinue
}

Write-Info "instalado en $target"

# --- PATH del usuario ---------------------------------------------------------
$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
if (-not $userPath) { $userPath = '' }
$entries = $userPath -split ';' | Where-Object { $_ }
if ($entries -notcontains $DestDir) {
    $newPath = if ($userPath) { "$userPath;$DestDir" } else { $DestDir }
    [Environment]::SetEnvironmentVariable('Path', $newPath, 'User')
    Write-Info "añadido al PATH del usuario: $DestDir"
    Write-Warn 'abre una terminal NUEVA para que el PATH tenga efecto'
} else {
    Write-Info "$DestDir ya estaba en el PATH"
}

# Disponible en la sesión actual también.
if (($env:Path -split ';') -notcontains $DestDir) {
    $env:Path = "$env:Path;$DestDir"
}

# --- Comprobación -------------------------------------------------------------
try {
    $ver = & $target --version
    Write-Info $ver
    Write-Info 'listo. Configura tu proveedor:  mkd config set deepinfra --key sk-... --model ...'
} catch {
    Write-Warn 'el binario se instaló pero no responde a --version'
}
