; NovaSoft Print Agent — instalador de doble clic (Inno Setup 6).
;
; Lo que hace, sin que el cajero abra una terminal:
;   1. Pide el código de emparejamiento y lo VALIDA contra el servidor en
;      esa misma pantalla (código mal escrito, usado o vencido → lo dice y
;      deja corregir). Si el equipo ya estaba emparejado se puede dejar vacío.
;   2. Quita cualquier instalación previa del servicio, esté donde esté
;      (manual de v0.4.0 en C:\NovaSoftAgent, o la v0.3.0 con icono de bandeja).
;   3. Copia el agente a C:\Program Files\NovaSoft\PrintAgent, registra el
;      servicio de Windows (arranque automático, reinicio ante fallo) y lo
;      arranca.
;
; Instalación silenciosa para soporte:
;   NovaSoftAgentSetup.exe /VERYSILENT /CODE=ABCD-1234-WXYZ
;
; Compilar: .\build\build-installer.ps1 -Version v0.4.1
;
; Datos que NO se borran al desinstalar (sobreviven a reinstalaciones):
;   C:\ProgramData\NovaSoftAgent\config.json   (emparejamiento)
;   C:\ProgramData\NovaSoftAgent\logs\agent.log

#ifndef MyAppVersion
  #define MyAppVersion "0.0.0-dev"
#endif

#define MyAppName "NovaSoft Print Agent"
#define MyAppExeName "novasoft-agent.exe"

[Setup]
; AppId: el MISMO que usó el instalador de v0.3.0. No cambiarlo nunca: es lo
; que hace que esta versión reemplace a la anterior en vez de instalarse al lado.
AppId={{A8E3F2B1-7C4D-4E9F-9A6B-1D3F5E8C2A4B}
AppName={#MyAppName}
AppVersion={#MyAppVersion}
AppVerName={#MyAppName} {#MyAppVersion}
AppPublisher=NovaSoft
AppPublisherURL=https://novasoft.mx
AppSupportURL=https://novasoft.mx
AppUpdatesURL=https://github.com/Richi2489/novasoft-print-agent/releases
; Ruta fija: el servicio guarda la ruta del .exe en el registro; si el
; usuario la eligiera y luego moviera la carpeta, el servicio no arrancaría.
DefaultDirName={autopf}\NovaSoft\PrintAgent
DisableDirPage=yes
DisableProgramGroupPage=yes
LicenseFile=..\..\LICENSE.txt
; Nombre SIN versión: el botón del POS apunta a
; releases/latest/download/NovaSoftAgentSetup.exe y debe ser estable.
OutputDir=..\..\dist\installer
OutputBaseFilename=NovaSoftAgentSetup
Compression=lzma2/ultra
SolidCompression=yes
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
MinVersion=10.0
PrivilegesRequired=admin
WizardStyle=modern
SetupIconFile=novasoft.ico
UninstallDisplayIcon={app}\novasoft.ico
UninstallDisplayName={#MyAppName}
; El servicio lo paramos nosotros (PrepareToInstall); que Inno no ofrezca
; "cerrar aplicaciones" sobre un servicio.
CloseApplications=no
RestartApplications=no
SetupLogging=yes
; El único toque a HKCU es BORRAR el autoarranque de la bandeja de v0.3.0,
; que vivía en el usuario que la instaló (el mismo que corre este instalador).
UsedUserAreasWarning=no
VersionInfoVersion={#MyAppVersion}
VersionInfoCompany=NovaSoft
VersionInfoProductName={#MyAppName}
VersionInfoProductVersion={#MyAppVersion}
VersionInfoDescription=Instalador de {#MyAppName}

[Languages]
Name: "spanish"; MessagesFile: "compiler:Languages\Spanish.isl"

[Messages]
spanish.FinishedHeadingLabel=¡Listo! La impresora quedó conectada
spanish.FinishedLabelNoIcons=[name] quedó instalado y corriendo como servicio de Windows: arranca solo con el equipo y se reinicia solo si falla.%n%nRegresa a NovaSoft: en unos segundos verás la impresora emparejada y podrás imprimir una prueba.
spanish.FinishedLabel=[name] quedó instalado y corriendo como servicio de Windows: arranca solo con el equipo y se reinicia solo si falla.%n%nRegresa a NovaSoft: en unos segundos verás la impresora emparejada y podrás imprimir una prueba.

[Files]
Source: "..\..\dist\windows\{#MyAppExeName}"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\..\LICENSE.txt"; DestDir: "{app}"; Flags: ignoreversion
Source: "novasoft.ico"; DestDir: "{app}"; Flags: ignoreversion

[InstallDelete]
; Restos de v0.3.0 (icono de bandeja), que ya no existe.
Type: files; Name: "{app}\novasoft-tray.exe"

[Registry]
; v0.3.0 arrancaba el icono de bandeja con el inicio de sesión.
Root: HKCU; Subkey: "Software\Microsoft\Windows\CurrentVersion\Run"; ValueName: "NovaSoftAgentTray"; Flags: deletevalue dontcreatekey

[UninstallRun]
Filename: "{app}\{#MyAppExeName}"; Parameters: "service uninstall"; Flags: runhidden waituntilterminated; RunOnceId: "QuitarServicio"

[UninstallDelete]
Type: dirifempty; Name: "{app}"
Type: dirifempty; Name: "{autopf}\NovaSoft"

[Code]
var
  PaginaCodigo: TInputQueryWizardPage;
  Emparejado: Boolean;        // el pair de ESTA instalación ya se hizo
  CodigoPendiente: String;    // modo silencioso: se empareja en PrepareToInstall

function RutaConfig: String;
begin
  Result := ExpandConstant('{commonappdata}\NovaSoftAgent\config.json');
end;

function YaEstabaEmparejado: Boolean;
begin
  Result := FileExists(RutaConfig);
end;

// Contrato con cmd/agent/main.go (exitPair*). No renumerar por separado.
function MensajeDePair(Codigo: Integer): String;
begin
  case Codigo of
    2: Result := 'El código no existe. Revisa que esté bien escrito (son 12 letras y números, como ABCD-1234-WXYZ).';
    3: Result := 'Ese código ya se usó. En NovaSoft genera uno nuevo (Configuración → Impresoras → Agregar impresora).';
    4: Result := 'El código venció (duran 10 minutos). En NovaSoft genera uno nuevo y escríbelo aquí.';
    5: Result := 'No hay conexión con NovaSoft. Revisa que este equipo tenga internet y vuelve a intentar.';
  else
    Result := 'No se pudo emparejar (código de error ' + IntToStr(Codigo) + '). Vuelve a intentar; si se repite, contacta a soporte NovaSoft.';
  end;
end;

function CodigoLimpio(S: String): String;
begin
  S := Uppercase(Trim(S));
  StringChangeEx(S, '-', '', True);
  StringChangeEx(S, ' ', '', True);
  Result := S;
end;

// Corre `pair` con el agente extraído a {tmp}. El config se guarda en
// %PROGRAMDATA%, así que no importa desde dónde corra el .exe.
function Emparejar(Codigo: String; var Mensaje: String): Boolean;
var
  ResultCode: Integer;
  Exe: String;
begin
  ExtractTemporaryFile('{#MyAppExeName}');
  Exe := ExpandConstant('{tmp}\{#MyAppExeName}');
  Log('Emparejando con el código capturado');
  if not Exec(Exe, 'pair ' + Codigo, '', SW_HIDE, ewWaitUntilTerminated, ResultCode) then
  begin
    Mensaje := 'No se pudo ejecutar el agente: ' + SysErrorMessage(ResultCode);
    Result := False;
    exit;
  end;
  Log('pair terminó con código ' + IntToStr(ResultCode));
  Result := ResultCode = 0;
  if not Result then
    Mensaje := MensajeDePair(ResultCode);
end;

procedure InitializeWizard;
var
  Descripcion: String;
begin
  Descripcion :=
    'En NovaSoft ve a Configuración → Impresoras → Agregar impresora, ' +
    'ponle nombre y copia el código que aparece.';
  if YaEstabaEmparejado then
    Descripcion := Descripcion + #13#10#13#10 +
      'Este equipo ya está emparejado. Deja el campo vacío para conservar ' +
      'el emparejamiento actual, o escribe un código nuevo para cambiarlo.';

  PaginaCodigo := CreateInputQueryPage(wpLicense,
    'Código de emparejamiento',
    'Conecta este equipo con tu restaurante en NovaSoft',
    Descripcion);
  PaginaCodigo.Add('Código (por ejemplo ABCD-1234-WXYZ):', False);
  PaginaCodigo.Values[0] := ExpandConstant('{param:CODE|}');
end;

function NextButtonClick(CurPageID: Integer): Boolean;
var
  Codigo, Mensaje, TextoBoton: String;
begin
  Result := True;
  if CurPageID <> PaginaCodigo.ID then
    exit;

  Codigo := CodigoLimpio(PaginaCodigo.Values[0]);
  if Codigo = '' then
  begin
    if YaEstabaEmparejado then
      exit; // conserva el emparejamiento actual
    MsgBox('Escribe el código que te da NovaSoft para continuar.', mbError, MB_OK);
    Result := False;
    exit;
  end;
  if Length(Codigo) <> 12 then
  begin
    MsgBox('El código tiene 12 letras y números (por ejemplo ABCD-1234-WXYZ). Revisa que esté completo.', mbError, MB_OK);
    Result := False;
    exit;
  end;

  if WizardSilent then
  begin
    CodigoPendiente := Codigo;
    exit;
  end;

  TextoBoton := WizardForm.NextButton.Caption;
  WizardForm.NextButton.Caption := 'Validando…';
  WizardForm.NextButton.Enabled := False;
  WizardForm.BackButton.Enabled := False;
  try
    Emparejado := Emparejar(Codigo, Mensaje);
  finally
    WizardForm.NextButton.Caption := TextoBoton;
    WizardForm.NextButton.Enabled := True;
    WizardForm.BackButton.Enabled := True;
  end;

  if not Emparejado then
  begin
    MsgBox(Mensaje, mbError, MB_OK);
    Result := False;
  end;
end;

// Antes de copiar archivos: emparejar (modo silencioso) y quitar cualquier
// servicio previo — si está corriendo, su .exe está bloqueado.
function PrepareToInstall(var NeedsRestart: Boolean): String;
var
  ResultCode: Integer;
  Mensaje: String;
begin
  Result := '';

  if WizardSilent and not Emparejado then
  begin
    if CodigoPendiente = '' then
      CodigoPendiente := CodigoLimpio(ExpandConstant('{param:CODE|}'));
    if CodigoPendiente <> '' then
    begin
      if not Emparejar(CodigoPendiente, Mensaje) then
      begin
        Result := Mensaje;
        exit;
      end;
      Emparejado := True;
    end
    else if not YaEstabaEmparejado then
    begin
      Result := 'Falta el código de emparejamiento: usa /CODE=ABCD-1234-WXYZ.';
      exit;
    end;
  end;

  ExtractTemporaryFile('{#MyAppExeName}');
  // `service uninstall` actúa por nombre de servicio, no por ruta: quita
  // también una instalación manual en otra carpeta. Si no había servicio,
  // falla sin consecuencias.
  Exec(ExpandConstant('{tmp}\{#MyAppExeName}'), 'service uninstall', '',
    SW_HIDE, ewWaitUntilTerminated, ResultCode);
  Log('service uninstall previo: ' + IntToStr(ResultCode));
  // Icono de bandeja de v0.3.0, si seguía abierto.
  Exec(ExpandConstant('{sys}\taskkill.exe'), '/F /IM novasoft-tray.exe', '',
    SW_HIDE, ewWaitUntilTerminated, ResultCode);
end;

procedure CurStepChanged(CurStep: TSetupStep);
var
  ResultCode: Integer;
  Exe: String;
begin
  if CurStep <> ssPostInstall then
    exit;
  Exe := ExpandConstant('{app}\{#MyAppExeName}');

  WizardForm.StatusLabel.Caption := 'Registrando el servicio de Windows…';
  if not Exec(Exe, 'service install', '', SW_HIDE, ewWaitUntilTerminated, ResultCode) or (ResultCode <> 0) then
  begin
    Log('service install falló: ' + IntToStr(ResultCode));
    SuppressibleMsgBox('No se pudo registrar el servicio de Windows (error ' + IntToStr(ResultCode) + ').' + #13#10#13#10 +
      'Vuelve a ejecutar el instalador. Si se repite, contacta a soporte NovaSoft.', mbError, MB_OK, IDOK);
    exit;
  end;

  WizardForm.StatusLabel.Caption := 'Arrancando el servicio…';
  if not Exec(Exe, 'service start', '', SW_HIDE, ewWaitUntilTerminated, ResultCode) or (ResultCode <> 0) then
  begin
    Log('service start falló: ' + IntToStr(ResultCode));
    SuppressibleMsgBox('El servicio quedó instalado pero no arrancó (error ' + IntToStr(ResultCode) + ').' + #13#10#13#10 +
      'Revisa que la impresora térmica esté conectada y con su driver instalado, y reinicia el equipo. ' +
      'El detalle queda en C:\ProgramData\NovaSoftAgent\logs\agent.log.', mbError, MB_OK, IDOK);
  end;
end;
