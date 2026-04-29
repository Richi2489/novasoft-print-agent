# NovaSoft Print Agent - build script for Windows amd64.
#
# Usage:
#   .\build\build-windows.ps1                    # default version 0.3.0-pre
#   .\build\build-windows.ps1 -Version "0.3.1"   # override
#
# Output:
#   dist\windows\novasoft-agent.exe
#
# This file is ASCII-only on purpose. Spanish accents in comments caused
# PowerShell 5.1 (Windows default) parser errors when the file was saved
# without BOM or with mixed line endings. Keep it ASCII so the script is
# robust across encoding configurations.

param(
    [string]$Version = "0.3.0-pre"
)

$ErrorActionPreference = "Stop"

$RepoRoot = Split-Path -Parent $PSScriptRoot
$OutputDir = Join-Path $RepoRoot "dist\windows"
$OutputExe = Join-Path $OutputDir "novasoft-agent.exe"

if (-not (Test-Path $OutputDir)) {
    New-Item -ItemType Directory -Force -Path $OutputDir | Out-Null
}

Write-Host "=== NovaSoft Print Agent build ===" -ForegroundColor Cyan
Write-Host "Version:     $Version"
Write-Host "Output:      $OutputExe"
Write-Host ""

# Cross-compile env:
#   GOOS=windows GOARCH=amd64   target Windows 64-bit.
#   CGO_ENABLED=0               no C deps; portable single-file exe.
$env:GOOS = "windows"
$env:GOARCH = "amd64"
$env:CGO_ENABLED = "0"

# ldflags:
#   -X main.Version=$Version    inject build version.
#   -s -w                       strip symbol table + DWARF (~30% smaller).
$ldflags = "-X main.Version=$Version -s -w"

Push-Location $RepoRoot
try {
    & go build -ldflags "$ldflags" -o "$OutputExe" ./cmd/agent
    if ($LASTEXITCODE -ne 0) {
        Write-Host ""
        Write-Host "Build failed with exit code $LASTEXITCODE" -ForegroundColor Red
        exit $LASTEXITCODE
    }
} finally {
    Pop-Location
}

$fileInfo = Get-Item $OutputExe
$sizeMB = [math]::Round($fileInfo.Length / 1MB, 2)

Write-Host ""
Write-Host "Build OK" -ForegroundColor Green
Write-Host "  File: $OutputExe"
Write-Host "  Size: $sizeMB MB"
Write-Host ""
Write-Host "Next steps:" -ForegroundColor Cyan
Write-Host "  1. cd $OutputDir"
Write-Host "  2. .\novasoft-agent.exe version"
Write-Host "  3. .\novasoft-agent.exe pair    (use the wizard code)"
Write-Host "  4. .\novasoft-agent.exe install (register Windows service)"
Write-Host "  5. .\novasoft-agent.exe start"
