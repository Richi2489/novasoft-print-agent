# NovaSoft Print Agent — build para macOS (desde Windows, cross-compile).
#
# Uso:
#   .\build\build-mac.ps1                     # version default v0.5.0
#   .\build\build-mac.ps1 -Version "v0.5.1"
#
# Salida:
#   dist\mac\novasoft-agent-mac-intel   Intel, macOS 10.13 High Sierra o más
#                                       nuevo (Mojave 10.14 incluido).
#                                       Compilado con Go 1.20.14. PRINCIPAL.
#   dist\mac\novasoft-agent-mac-arm64   Apple Silicon, macOS 12+ (Go del repo).
#   dist\mac\NovaSoftAgent-mac\         paquete: bin\ + install-mac.sh +
#                                       uninstall-mac.sh + INSTALAR-MAC.md
#   dist\mac\NovaSoftAgent-mac.zip
#
# Por qué Go 1.20 para Intel: el go.mod pide Go 1.26 y el runtime de Go
# exige macOS 12+ desde Go 1.25 (1.23/1.24: 11+; 1.21/1.22: 10.15+). Sólo
# Go 1.20 arranca en 10.13/10.14 (la Mac mini 2014 de la feria tiene
# Mojave 10.14.6). El código no usa nada de Go >= 1.21, así que se compila
# TAL CUAL sobre una COPIA temporal con el go.mod rebajado (go 1.20 y
# x/image v0.12.0, x/text v0.13.0, x/sys v0.12.0, que lo soportan). El
# repo no se toca. Un binario Go 1.20 corre igual en macOS más nuevos (y
# en Apple Silicon vía Rosetta 2).
# Requiere internet la primera vez (descarga go1.20.14 y esos módulos).
#
# Verificar el toolchain:  go version -m dist\mac\novasoft-agent-mac-intel

param(
    [string]$Version = "v0.5.0"
)

$ErrorActionPreference = "Stop"

$RepoRoot = Split-Path -Parent $PSScriptRoot
$DistMac  = Join-Path $RepoRoot "dist\mac"
$Paquete  = Join-Path $DistMac "NovaSoftAgent-mac"
$BinDir   = Join-Path $Paquete "bin"
$ldflags  = "-X main.Version=$Version -s -w"

if (Test-Path $Paquete) { Remove-Item -Recurse -Force $Paquete }
New-Item -ItemType Directory -Force -Path $BinDir | Out-Null

function Build-Darwin([string]$Arch, [string]$Out, [string]$Dir, [string]$Toolchain) {
    $env:GOOS = "darwin"
    $env:GOARCH = $Arch
    $env:CGO_ENABLED = "0"
    if ($Toolchain) { $env:GOTOOLCHAIN = $Toolchain }
    Push-Location $Dir
    try {
        & go build -trimpath -ldflags "$ldflags" -o "$Out" ./cmd/agent
        if ($LASTEXITCODE -ne 0) { throw "go build darwin/$Arch fallo ($LASTEXITCODE)" }
    } finally {
        Pop-Location
        Remove-Item Env:GOOS, Env:GOARCH, Env:CGO_ENABLED -ErrorAction SilentlyContinue
        Remove-Item Env:GOTOOLCHAIN -ErrorAction SilentlyContinue
    }
    Write-Host "  OK $Out"
}

Write-Host "=== NovaSoft Print Agent — build macOS $Version ===" -ForegroundColor Cyan

# 1. Apple Silicon (toolchain del repo, macOS 12+).
$Arm = Join-Path $DistMac "novasoft-agent-mac-arm64"
Build-Darwin "arm64" $Arm $RepoRoot ""

# 2. Intel con Go 1.20.14 (macOS 10.13+) sobre una copia temporal.
$Intel = Join-Path $DistMac "novasoft-agent-mac-intel"
$Tmp = Join-Path $env:TEMP ("novasoft-agent-go120-" + [guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Force -Path $Tmp | Out-Null
try {
    foreach ($d in @("cmd", "internal", "pkg")) {
        Copy-Item -Recurse (Join-Path $RepoRoot $d) (Join-Path $Tmp $d)
    }
    $gomod = "module github.com/Richi2489/novasoft-print-agent`n`ngo 1.20`n`nrequire (`n`tgithub.com/alexbrainman/printer v0.0.0-20200912035444-f40f26f0bdeb`n`tgolang.org/x/image v0.12.0`n`tgolang.org/x/sys v0.12.0`n`tgolang.org/x/text v0.13.0`n)`n"
    [System.IO.File]::WriteAllText((Join-Path $Tmp "go.mod"), $gomod)
    Push-Location $Tmp
    try {
        $env:GOTOOLCHAIN = "go1.20.14"
        & go mod tidy
        if ($LASTEXITCODE -ne 0) { throw "go mod tidy (go1.20) fallo" }
    } finally {
        Pop-Location
        Remove-Item Env:GOTOOLCHAIN -ErrorAction SilentlyContinue
    }
    Build-Darwin "amd64" $Intel $Tmp "go1.20.14"
} finally {
    Remove-Item -Recurse -Force $Tmp -ErrorAction SilentlyContinue
}
$env:GOTOOLCHAIN = "go1.20.14"
$tc = (& go version $Intel)
Remove-Item Env:GOTOOLCHAIN -ErrorAction SilentlyContinue
if ($tc -notmatch "go1\.20\.") { throw "el binario Intel no salio con Go 1.20: $tc" }
Write-Host "  $tc"

Copy-Item $Intel (Join-Path $BinDir "novasoft-agent-mac-intel")
Copy-Item $Arm   (Join-Path $BinDir "novasoft-agent-mac-arm64")

# 3. Scripts y guía. Los .sh se copian con LF (bash en macOS falla con CRLF).
foreach ($f in @("install-mac.sh", "uninstall-mac.sh")) {
    $txt = [System.IO.File]::ReadAllText((Join-Path $RepoRoot "build\mac\$f"))
    [System.IO.File]::WriteAllText((Join-Path $Paquete $f), $txt.Replace("`r`n", "`n"))
}
Copy-Item (Join-Path $RepoRoot "docs\INSTALAR-MAC.md") (Join-Path $Paquete "INSTALAR-MAC.md")

# 4. Zip. NO se usa Compress-Archive: en PowerShell 5.1 guarda las rutas
# con "\" y el Archive Utility de macOS crea archivos llamados
# "NovaSoftAgent-mac\install-mac.sh" en vez de la carpeta. Se arma a mano
# con "/". .NET marca el zip como hecho en DOS, así que no lleva permisos
# Unix: por eso la guía usa "bash install-mac.sh" y el script hace
# chmod +x al binario que copia.
Add-Type -AssemblyName System.IO.Compression
Add-Type -AssemblyName System.IO.Compression.FileSystem
$Zip = Join-Path $DistMac "NovaSoftAgent-mac.zip"
if (Test-Path $Zip) { Remove-Item -Force $Zip }
$fs = [System.IO.File]::Open($Zip, [System.IO.FileMode]::CreateNew)
$za = New-Object System.IO.Compression.ZipArchive($fs, [System.IO.Compression.ZipArchiveMode]::Create)
try {
    Get-ChildItem -Recurse -File $Paquete | ForEach-Object {
        $rel = "NovaSoftAgent-mac/" + $_.FullName.Substring($Paquete.Length + 1).Replace("\", "/")
        $entry = $za.CreateEntry($rel, [System.IO.Compression.CompressionLevel]::Optimal)
        $out = $entry.Open()
        try {
            $bytes = [System.IO.File]::ReadAllBytes($_.FullName)
            $out.Write($bytes, 0, $bytes.Length)
        } finally { $out.Dispose() }
    }
} finally {
    $za.Dispose()
    $fs.Dispose()
}

Write-Host ""
Write-Host "Listo:" -ForegroundColor Green
Get-ChildItem $DistMac -File | ForEach-Object { Write-Host ("  {0,-30} {1,6:N1} MB" -f $_.Name, ($_.Length / 1MB)) }
