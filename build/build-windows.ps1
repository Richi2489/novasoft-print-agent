# NovaSoft Print Agent — build script para Windows amd64.
#
# Uso:
#   .\build\build-windows.ps1                    # version default 0.1.0
#   .\build\build-windows.ps1 -Version "0.1.1"   # override
#
# Salida:
#   dist\windows\novasoft-agent.exe

param(
    [string]$Version = "0.2.1"
)

$ErrorActionPreference = "Stop"

$RepoRoot = Split-Path -Parent $PSScriptRoot
$OutputDir = Join-Path $RepoRoot "dist\windows"
$OutputExe = Join-Path $OutputDir "novasoft-agent.exe"

# Crear dist\windows si no existe.
if (-not (Test-Path $OutputDir)) {
    New-Item -ItemType Directory -Force -Path $OutputDir | Out-Null
}

Write-Host "=== NovaSoft Print Agent build ===" -ForegroundColor Cyan
Write-Host "Version:     $Version"
Write-Host "Output:      $OutputExe"
Write-Host ""

# Env para cross-compile reproducible:
#   GOOS=windows GOARCH=amd64   — target Windows 64-bit.
#   CGO_ENABLED=0               — sin dependencias C; el exe no necesita
#                                  MSVC runtime ni nada extra en el
#                                  cliente. Portable single-file.
$env:GOOS = "windows"
$env:GOARCH = "amd64"
$env:CGO_ENABLED = "0"

# ldflags:
#   -X main.Version=$Version   — inyecta version al binary.
#   -s -w                      — strip symbol table + DWARF. Reduce
#                                  size ~30%. No afecta stack traces
#                                  operativos (tenemos logs).
$ldflags = "-X main.Version=$Version -s -w"

Push-Location $RepoRoot
try {
    & go build -ldflags "$ldflags" -o "$OutputExe" ./cmd/agent
    if ($LASTEXITCODE -ne 0) {
        Write-Host "" -ForegroundColor Red
        Write-Host "✗ Build fallo con exit code $LASTEXITCODE" -ForegroundColor Red
        exit $LASTEXITCODE
    }
} finally {
    Pop-Location
}

# Reporte de tamaño del binary.
$fileInfo = Get-Item $OutputExe
$sizeMB = [math]::Round($fileInfo.Length / 1MB, 2)

Write-Host ""
Write-Host "✓ Build exitoso" -ForegroundColor Green
Write-Host "  Archivo: $OutputExe"
Write-Host "  Tamano:  $sizeMB MB"
Write-Host ""
Write-Host "Proximos pasos:" -ForegroundColor Cyan
Write-Host "  1. cd $OutputDir"
Write-Host "  2. .\novasoft-agent.exe version"
Write-Host "  3. .\novasoft-agent.exe pair  (usa el codigo del wizard)"
Write-Host "  4. .\novasoft-agent.exe run"
