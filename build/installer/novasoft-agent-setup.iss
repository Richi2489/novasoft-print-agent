; NovaSoft Print Agent - Inno Setup script.
;
; Compila NovaSoftAgentSetup-{version}.exe que instala el servicio
; Windows + tray icon con un wizard "Next, Next, Finish" estandar.
;
; Compilacion local:
;   1. Instalar Inno Setup 6+ desde https://jrsoftware.org/isdl.php
;   2. Buildear los binarios primero:
;        .\build\build-windows.ps1 -Version "0.3.0-pre"
;   3. Compilar el setup:
;        & "C:\Program Files (x86)\Inno Setup 6\iscc.exe" `
;            /DMyAppVersion=0.3.0-pre `
;            .\build\installer\novasoft-agent-setup.iss
;   4. Output: dist\installer\NovaSoftAgentSetup-0.3.0-pre.exe
;
; En CI (.github/workflows/release.yml de Fase 5):
;   - Tag v0.3.0 dispara el workflow.
;   - Workflow corre build-windows.ps1 con la version del tag.
;   - Workflow corre iscc con /DMyAppVersion del tag.
;   - Sube NovaSoftAgentSetup-{version}.exe como release asset.
;
; Layout post-install:
;   C:\Program Files\NovaSoft\PrintAgent\
;     novasoft-agent.exe           (servicio core, todos los subcomandos)
;     novasoft-tray.exe            (tray icon, inicia con HKCU\Run)
;     LICENSE.txt
;
; Datos persistentes (NO tocados por uninstall — sobreviven reinstall):
;   C:\ProgramData\NovaSoft\config.json
;   C:\ProgramData\NovaSoft\agent.log

#ifndef MyAppVersion
  #define MyAppVersion "0.3.0-pre"
#endif

#define MyAppName "NovaSoft Print Agent"
#define MyAppPublisher "NovaSoft"
#define MyAppURL "https://novasoft.mx"
#define MyAppExeName "novasoft-agent.exe"
#define MyTrayExeName "novasoft-tray.exe"

[Setup]
; AppId es el UUID unico que identifica esta app para Inno Setup.
; NUNCA cambiar entre versiones — Windows usa este key para resolver
; "esta version reemplaza a la anterior" vs "instalar lado a lado".
; Si cambia, los upgrades dejan de funcionar y conviven dos installs
; del agent al mismo tiempo.
AppId={{A8E3F2B1-7C4D-4E9F-9A6B-1D3F5E8C2A4B}
AppName={#MyAppName}
AppVersion={#MyAppVersion}
AppVerName={#MyAppName} {#MyAppVersion}
AppPublisher={#MyAppPublisher}
AppPublisherURL={#MyAppURL}
AppSupportURL={#MyAppURL}/soporte
AppUpdatesURL=https://github.com/Richi2489/novasoft-print-agent/releases

DefaultDirName={autopf}\NovaSoft\PrintAgent
; DisableDirPage=auto -> se muestra solo si el dir default no existe;
; en upgrades ya existe, se omite y va directo al install.
DisableDirPage=auto

; No creamos shortcuts en Start Menu — la app es un servicio + tray,
; no necesita entry en menu de aplicaciones.
DisableProgramGroupPage=yes
DisableReadyPage=no
DisableFinishedPage=no

LicenseFile=..\..\LICENSE.txt

OutputDir=..\..\dist\installer
; OutputBaseFilename SIN sufijo de version para que la URL del wizard
; (releases/latest/download/NovaSoftAgentSetup.exe) sea estable entre
; versiones. La version del binario vive en AppVersion + en el nombre
; del GitHub Release, no en el filename.
OutputBaseFilename=NovaSoftAgentSetup

Compression=lzma2/ultra
SolidCompression=yes

; 64-bit only — los binarios Go se buildean con GOARCH=amd64.
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible

; Requerido admin para registrar servicio Windows.
PrivilegesRequired=admin

WizardStyle=modern
SetupIconFile=..\..\cmd\tray\icons\online.ico
UninstallDisplayIcon={app}\{#MyAppExeName}
UninstallDisplayName={#MyAppName}

; Sin Internet check — el agent maneja conexion despues de instalar.
; AppendDefaultDirName=no — usar el path tal cual sin agregar AppName.

VersionInfoVersion={#MyAppVersion}
VersionInfoCompany={#MyAppPublisher}
VersionInfoProductName={#MyAppName}
VersionInfoProductVersion={#MyAppVersion}

[Languages]
; Spanish primero (cliente target). English como fallback para devs.
Name: "spanish"; MessagesFile: "compiler:Languages\Spanish.isl"
Name: "english"; MessagesFile: "compiler:Default.isl"

[Tasks]
; Tareas opcionales que el wizard muestra como checkboxes en la pagina
; "Tareas adicionales". Default checked porque son lo que un usuario
; tipico quiere.

Name: "startserviceatinstall"; \
  Description: "Iniciar el servicio NovaSoft Print Agent automaticamente"; \
  GroupDescription: "Tareas adicionales:"

Name: "starttrayatlogin"; \
  Description: "Lanzar el tray icon al iniciar sesion Windows"; \
  GroupDescription: "Tareas adicionales:"

[Files]
; Source paths son relativos al directorio del .iss (build\installer\).
; ignoreversion = sobrescribir aunque la version del archivo sea igual
; o menor — necesario para upgrades donde el binary no cambio version
; pero si tiene fixes que recompilamos.
Source: "..\..\dist\windows\{#MyAppExeName}"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\..\dist\windows\{#MyTrayExeName}"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\..\LICENSE.txt"; DestDir: "{app}"; Flags: ignoreversion

[Registry]
; Auto-arranque del tray en login del usuario actual.
; HKCU = solo este usuario. Si la maquina tiene multiples usuarios y
; queremos tray para todos, cambiar a HKLM\Software\Microsoft\Windows\CurrentVersion\Run.
; Por v0.3.0 mantenemos per-user — el caso comun es 1 usuario por POS.
;
; Flags: uninsdeletevalue — se borra cuando uninstall corre.
Root: HKCU; \
  Subkey: "Software\Microsoft\Windows\CurrentVersion\Run"; \
  ValueType: string; \
  ValueName: "NovaSoftAgentTray"; \
  ValueData: """{app}\{#MyTrayExeName}"""; \
  Flags: uninsdeletevalue; \
  Tasks: starttrayatlogin

[Run]
; Post-install. Estos corren EN ORDEN, despues de copiar archivos.
; runhidden = sin consola; statusmsg = lo que se muestra en el wizard
; durante este paso.

; 1. Registrar el servicio Windows. Falla si ya esta registrado, asi
;    que ignoreoutput + skipif para idempotencia en re-installs.
Filename: "{app}\{#MyAppExeName}"; \
  Parameters: "install"; \
  Flags: runhidden waituntilterminated; \
  StatusMsg: "Registrando servicio Windows..."

; 2. Iniciar el servicio (si el usuario aprobo la tarea).
Filename: "{app}\{#MyAppExeName}"; \
  Parameters: "start"; \
  Flags: runhidden waituntilterminated; \
  StatusMsg: "Iniciando servicio..."; \
  Tasks: startserviceatinstall

; 3. Lanzar el tray ahora mismo (sin esperar al proximo login).
;    runasoriginaluser = ejecuta como el user que lanzo setup, NO
;    como el admin que recibio la elevacion UAC. Sin esto, el tray
;    correria como SYSTEM y no veria el desktop del user.
;    nowait = no bloquear el wizard esperando al tray.
;    skipifsilent = solo en install interactivo, no en /SILENT.
;    postinstall = se muestra como checkbox "Lanzar ahora" en Finish.
Filename: "{app}\{#MyTrayExeName}"; \
  Description: "Lanzar tray icon ahora"; \
  Flags: nowait postinstall skipifsilent runasoriginaluser; \
  Tasks: starttrayatlogin

[UninstallRun]
; Cleanup al desinstalar. Orden inverso:
;   1. Matar tray si esta corriendo (no es servicio, no responde a stop).
;   2. Detener servicio.
;   3. Desregistrar servicio.
;
; runhidden = sin consola.
; RunOnceId = ID unico para que Inno no corra el mismo step dos veces
; si el uninstall se reintenta.

Filename: "taskkill.exe"; \
  Parameters: "/F /IM {#MyTrayExeName}"; \
  Flags: runhidden; \
  RunOnceId: "killtray"

Filename: "{app}\{#MyAppExeName}"; \
  Parameters: "stop"; \
  Flags: runhidden waituntilterminated; \
  RunOnceId: "stopservice"

Filename: "{app}\{#MyAppExeName}"; \
  Parameters: "uninstall"; \
  Flags: runhidden waituntilterminated; \
  RunOnceId: "uninstallservice"

[UninstallDelete]
; %PROGRAMDATA%\NovaSoft\ NO se borra automaticamente — preserva el
; config.json (pairing token) y los logs para que un reinstall
; recupere el estado sin re-emparejar. Si el admin quiere wipe total,
; lo hace manualmente.
;
; Solo borramos el directorio de install si quedo vacio post-uninstall.
Type: dirifempty; Name: "{app}"
Type: dirifempty; Name: "{autopf}\NovaSoft"

[Code]
// Inno Setup Pascal — bloque opcional para customizar el wizard.
// v0.3.0 no tiene logica custom — solo el dummy block para reservar
// el slot por si futuros fases lo necesitan (validacion de runtime,
// migracion de configs, etc.).
