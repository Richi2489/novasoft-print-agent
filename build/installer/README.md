# Inno Setup installer build

Genera `NovaSoftAgentSetup-{version}.exe`, el instalador que el cliente
final ejecuta con doble click. Wizard estándar Windows: Welcome →
License → Install Location → Tasks → Install → Finish.

## Build local (raro — normalmente lo hace CI)

```powershell
# 1. Instalar Inno Setup 6+ (one-shot, requiere admin):
#    https://jrsoftware.org/isdl.php
#    Acepta el default install path C:\Program Files (x86)\Inno Setup 6\

# 2. Buildear los binarios primero — el .iss los embed.
.\build\build-windows.ps1 -Version "0.3.0-pre"

# 3. Compilar el installer.
& "C:\Program Files (x86)\Inno Setup 6\iscc.exe" `
    /DMyAppVersion=0.3.0-pre `
    .\build\installer\novasoft-agent-setup.iss
```

Output: `dist\installer\NovaSoftAgentSetup-0.3.0-pre.exe`.

## Lo que hace el installer

### Install
1. Copia `novasoft-agent.exe` y `novasoft-tray.exe` a
   `C:\Program Files\NovaSoft\PrintAgent\`.
2. Corre `novasoft-agent.exe install` → registra servicio Windows
   (DisplayName: "NovaSoft Print Agent", StartType: Automatic).
3. Corre `novasoft-agent.exe start` (si la tarea está checkeada).
4. Agrega `HKCU\Software\Microsoft\Windows\CurrentVersion\Run\NovaSoftAgentTray`
   apuntando al tray exe (si la tarea está checkeada).
5. Lanza el tray inmediatamente como el usuario interactivo (no como
   admin elevado).

### Uninstall
1. `taskkill /F /IM novasoft-tray.exe` — el tray no escucha SIGINT.
2. `novasoft-agent.exe stop` — detiene el servicio.
3. `novasoft-agent.exe uninstall` — desregistra el servicio.
4. Borra archivos en `Program Files\NovaSoft\PrintAgent\`.
5. Borra el value de HKCU Run (auto via `uninsdeletevalue` flag).
6. **NO toca** `C:\ProgramData\NovaSoft\` — preserva `config.json` y
   `agent.log` para que un reinstall recupere el estado.

## Config retention

`%PROGRAMDATA%\NovaSoft\config.json` sobrevive uninstall a propósito.
Si el admin reinstala el agent (upgrade o reinstall después de
problemas), el agent retoma el pairing existente sin re-empareja.

Para wipe total manual:
```powershell
Remove-Item -Recurse "C:\ProgramData\NovaSoft"
```

## CI compilation

`.github/workflows/release.yml` (Fase 5) corre en runner Windows con
Chocolatey: `choco install innosetup -y` → `iscc.exe` con el version
del tag. El setup.exe compilado se sube como release asset al tag
correspondiente.

## AppId GUID (no cambiar)

El AppId `{A8E3F2B1-7C4D-4E9F-9A6B-1D3F5E8C2A4B}` es la identidad
de Windows del producto. Cambiarlo rompe upgrades — Windows ve la
nueva versión como un producto distinto y el desinstalador del viejo
queda huérfano. Mantener forever.
