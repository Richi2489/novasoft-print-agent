# NovaSoft Print Agent — compila el agente y el instalador de doble clic.
#
# Uso:
#   .\build\build-installer.ps1 -Version v0.4.1
#
# Salida:
#   dist\windows\novasoft-agent.exe
#   dist\installer\NovaSoftAgentSetup.exe
#   dist\SHA256SUMS.txt                     (de los dos, para el release)
#
# Requiere Go y Inno Setup 6 (ISCC.exe). Busca ISCC en las dos rutas en las
# que lo deja el instalador oficial (por usuario y por máquina).

param(
    [Parameter(Mandatory = $true)][string]$Version
)

$ErrorActionPreference = "Stop"
$RepoRoot = Split-Path -Parent $PSScriptRoot

# 1. Agente (mismo script de siempre, con la versión inyectada).
& (Join-Path $PSScriptRoot "build-windows.ps1") -Version $Version
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

# 2. Instalador. Inno quiere la versión sin la "v" (VersionInfoVersion).
$iscc = @(
    "$env:LOCALAPPDATA\Programs\Inno Setup 6\ISCC.exe",
    "${env:ProgramFiles(x86)}\Inno Setup 6\ISCC.exe",
    "$env:ProgramFiles\Inno Setup 6\ISCC.exe"
) | Where-Object { Test-Path $_ } | Select-Object -First 1
if (-not $iscc) { throw "No encontré ISCC.exe — instala Inno Setup 6 (winget install JRSoftware.InnoSetup)." }

$numero = $Version.TrimStart("v")
& $iscc "/DMyAppVersion=$numero" (Join-Path $PSScriptRoot "installer\novasoft-agent-setup.iss")
if ($LASTEXITCODE -ne 0) { throw "ISCC falló con código $LASTEXITCODE" }

# 3. Sumas para el release.
$agente = Join-Path $RepoRoot "dist\windows\novasoft-agent.exe"
$setup = Join-Path $RepoRoot "dist\installer\NovaSoftAgentSetup.exe"
$sumas = foreach ($f in @($agente, $setup)) {
    "{0}  {1}" -f (Get-FileHash $f -Algorithm SHA256).Hash.ToLower(), (Split-Path $f -Leaf)
}
$sumas | Set-Content -Encoding ascii (Join-Path $RepoRoot "dist\SHA256SUMS.txt")

Write-Host ""
Write-Host "OK  $setup" -ForegroundColor Green
$sumas | ForEach-Object { Write-Host "    $_" }
