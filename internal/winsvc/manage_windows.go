//go:build windows

package winsvc

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/eventlog"
	"golang.org/x/sys/windows/svc/mgr"
)

// Acciones de recuperación. Equivalen exactamente a:
//
//	sc failure NovaSoftPrintAgent reset= 0 actions= restart/5000/restart/5000/restart/30000
//
// Es decir: primer fallo → reintenta a los 5 s; segundo → 5 s; tercero y
// posteriores → cada 30 s. resetPeriod = 0 significa que el contador de
// fallos NUNCA se resetea, así que un agent que lleva meses arriba y se
// cae de madrugada igual entra en el ritmo de 30 s en vez de volver al
// principio. Lo configuramos desde Go (SetRecoveryActions) para que no
// dependa de que alguien acuerde correr sc.exe a mano después de
// instalar.
var recoveryActions = []mgr.RecoveryAction{
	{Type: mgr.ServiceRestart, Delay: 5 * time.Second},
	{Type: mgr.ServiceRestart, Delay: 5 * time.Second},
	{Type: mgr.ServiceRestart, Delay: 30 * time.Second},
}

// recoveryResetPeriod = 0 → "sin reseteo" (sc failure reset= 0).
const recoveryResetPeriod = 0

// waitTimeout — cuánto esperamos a que el SCM confirme un cambio de
// estado en start/stop antes de reportar timeout.
const waitTimeout = 30 * time.Second

// IsElevated indica si el proceso corre con el token de administrador
// levantado. Ojo: un usuario que ES administrador pero corre una consola
// normal tiene el grupo Administradores marcado "solo para denegar", y
// IsMember devuelve false — que es justo lo que queremos, porque sin
// elevar tampoco puede tocar el SCM.
func IsElevated() bool {
	var sid *windows.SID
	err := windows.AllocateAndInitializeSid(
		&windows.SECURITY_NT_AUTHORITY,
		2,
		windows.SECURITY_BUILTIN_DOMAIN_RID,
		windows.DOMAIN_ALIAS_RID_ADMINS,
		0, 0, 0, 0, 0, 0,
		&sid,
	)
	if err != nil {
		return false
	}
	defer windows.FreeSid(sid)

	// Token(0) = token efectivo del proceso actual.
	member, err := windows.Token(0).IsMember(sid)
	return err == nil && member
}

// adminRequiredError construye el mensaje que ve el operador cuando le
// faltan permisos. Es deliberadamente explícito: quien instala esto en
// un restaurante no sabe qué es UAC ni qué significa "Access is denied".
func adminRequiredError(action string) error {
	exe, err := ExePath()
	if err != nil || exe == "" {
		exe = "novasoft-agent.exe"
	}
	dir := filepath.Dir(exe)

	return fmt.Errorf(`se requieren permisos de administrador para %s el servicio de Windows.

Qué hacer:
  1. Cierra esta ventana de PowerShell.
  2. Haz click en el menú Inicio y escribe: PowerShell
  3. Click DERECHO sobre "Windows PowerShell" → "Ejecutar como administrador".
  4. Acepta el aviso azul de Windows (Control de cuentas de usuario) con "Sí".
  5. En la ventana nueva ejecuta estos dos comandos:

       cd "%s"
       .\%s service %s

Si el paso 3 no te ofrece "Ejecutar como administrador", tu usuario de
Windows no es administrador de este equipo: pide a quien lo administre
que ejecute la instalación`, action, dir, filepath.Base(exe), action)
}

// wrapSCMError traduce los errores crípticos de la API de Windows a algo
// accionable. ERROR_ACCESS_DENIED es el que ve casi todo el mundo.
func wrapSCMError(action string, err error) error {
	if errors.Is(err, windows.ERROR_ACCESS_DENIED) || errors.Is(err, os.ErrPermission) {
		return adminRequiredError(action)
	}
	return err
}

// ExePath devuelve la ruta absoluta del binario en ejecución. El SCM
// guarda esta ruta en el registro, así que si después mueves el .exe el
// servicio deja de arrancar — por eso SETUP.md insiste en dejarlo en una
// ruta fija (C:\NovaSoftAgent\).
func ExePath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	// EvalSymlinks resuelve accesos directos/junctions para que en el
	// registro quede la ruta real.
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return filepath.Abs(exe)
}

// connect abre el SCM con permisos de escritura, traduciendo el
// "Access is denied" a instrucciones en español.
func connect(action string) (*mgr.Mgr, error) {
	if !IsElevated() {
		return nil, adminRequiredError(action)
	}
	m, err := mgr.Connect()
	if err != nil {
		return nil, wrapSCMError(action, err)
	}
	return m, nil
}

// Install crea el servicio con arranque automático y acciones de
// recuperación, y registra la fuente del Event Log.
//
// Cuenta: LocalSystem. Es la única que no depende de la contraseña de un
// usuario (que caduca, que cambia, y que habría que teclear en cada
// restaurante). El precio es que LocalSystem tiene otro %APPDATA%, por
// eso el config vive en %PROGRAMDATA% — ver el doc del paquete config.
func Install() error {
	exe, err := ExePath()
	if err != nil {
		return fmt.Errorf("no pude determinar la ruta del ejecutable: %w", err)
	}

	m, err := connect("install")
	if err != nil {
		return err
	}
	defer m.Disconnect()

	// ¿Ya existe? Mejor decirlo claro que fallar con "service already
	// exists" y dejar al operador adivinando.
	if s, err := m.OpenService(Name); err == nil {
		s.Close()
		return fmt.Errorf(`el servicio %q ya está instalado.

Si quieres reinstalarlo (por ejemplo tras actualizar el .exe):
  .\%s service uninstall
  .\%s service install`, Name, filepath.Base(exe), filepath.Base(exe))
	}

	// El SCM arranca el binario con el subcomando "run"; el propio
	// binario detecta que viene del SCM (svc.IsWindowsService) y elige
	// el modo servicio en lugar del interactivo.
	s, err := m.CreateService(Name, exe, mgr.Config{
		DisplayName:  DisplayName,
		Description:  Description,
		StartType:    mgr.StartAutomatic,
		ErrorControl: mgr.ErrorNormal,
		// LocalSystem: sin contraseña que gestionar ni que caduque.
		ServiceStartName: "LocalSystem",
	}, "run")
	if err != nil {
		return wrapSCMError("install", err)
	}
	defer s.Close()

	// Acciones de recuperación. Si esto falla el servicio queda
	// instalado pero sin auto-reinicio, que es justo el problema que
	// veníamos a resolver — así que revertimos para no dejar una
	// instalación a medias que aparente estar bien.
	if err := s.SetRecoveryActions(recoveryActions, recoveryResetPeriod); err != nil {
		_ = s.Delete()
		return fmt.Errorf("configurando acciones de recuperación: %w", err)
	}

	// Por defecto Windows solo aplica las acciones de recuperación
	// cuando el proceso muere de forma anormal (crash, taskkill). Con
	// esto también las aplica cuando el servicio termina reportando un
	// código de error — el caso de "no encuentro la impresora" o "no hay
	// config": queremos que reintente igual.
	if err := s.SetRecoveryActionsOnNonCrashFailures(true); err != nil {
		_ = s.Delete()
		return fmt.Errorf("habilitando recuperación ante fallos no-crash: %w", err)
	}

	// Fuente del Event Log. No es fatal si falla: el agent igual escribe
	// su log a archivo.
	if err := eventlog.InstallAsEventCreate(Name, eventlog.Error|eventlog.Warning|eventlog.Info); err != nil {
		if !strings.Contains(strings.ToLower(err.Error()), "already exists") {
			fmt.Fprintf(os.Stderr, "⚠️  no pude registrar la fuente del Event Log: %v\n", err)
		}
	}

	return nil
}

// Uninstall detiene el servicio si hace falta y lo borra, junto con la
// fuente del Event Log.
func Uninstall() error {
	m, err := connect("uninstall")
	if err != nil {
		return err
	}
	defer m.Disconnect()

	s, err := m.OpenService(Name)
	if err != nil {
		return fmt.Errorf("el servicio %q no está instalado", Name)
	}
	defer s.Close()

	// Si está corriendo hay que pararlo antes: si no, Windows lo marca
	// "pendiente de eliminación" y no desaparece hasta el próximo
	// reinicio, lo que confunde muchísimo al reinstalar.
	status, err := s.Query()
	if err == nil && status.State != svc.Stopped {
		if _, err := s.Control(svc.Stop); err != nil {
			return fmt.Errorf("deteniendo el servicio antes de borrarlo: %w", err)
		}
		if err := waitForState(s, svc.Stopped); err != nil {
			return err
		}
	}

	if err := s.Delete(); err != nil {
		return wrapSCMError("uninstall", err)
	}

	if err := eventlog.Remove(Name); err != nil {
		if !strings.Contains(strings.ToLower(err.Error()), "cannot find") {
			fmt.Fprintf(os.Stderr, "⚠️  no pude quitar la fuente del Event Log: %v\n", err)
		}
	}
	return nil
}

// Start arranca el servicio y espera a que reporte RUNNING.
func Start() error {
	m, err := connect("start")
	if err != nil {
		return err
	}
	defer m.Disconnect()

	s, err := m.OpenService(Name)
	if err != nil {
		return fmt.Errorf("el servicio %q no está instalado — ejecuta primero 'service install'", Name)
	}
	defer s.Close()

	if status, err := s.Query(); err == nil && status.State == svc.Running {
		return nil // idempotente
	}
	if err := s.Start(); err != nil {
		return wrapSCMError("start", err)
	}
	return waitForState(s, svc.Running)
}

// Stop detiene el servicio y espera a que reporte STOPPED.
func Stop() error {
	m, err := connect("stop")
	if err != nil {
		return err
	}
	defer m.Disconnect()

	s, err := m.OpenService(Name)
	if err != nil {
		return fmt.Errorf("el servicio %q no está instalado", Name)
	}
	defer s.Close()

	status, err := s.Query()
	if err == nil && status.State == svc.Stopped {
		return nil // idempotente
	}
	if _, err := s.Control(svc.Stop); err != nil {
		return wrapSCMError("stop", err)
	}
	return waitForState(s, svc.Stopped)
}

// Info es la foto del servicio que consume `service status`.
type Info struct {
	Installed  bool
	State      svc.State
	ProcessID  uint32
	StartType  uint32
	BinaryPath string
	Account    string
	Recovery   []mgr.RecoveryAction
}

// Query lee el estado del servicio. A diferencia del resto de comandos
// NO exige elevación: consultar es un derecho de cualquier usuario y
// `service status` es lo primero que va a correr alguien de soporte por
// teléfono. Por eso conectamos con permisos de solo lectura.
func Query() (*Info, error) {
	h, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT|windows.SC_MANAGER_ENUMERATE_SERVICE)
	if err != nil {
		return nil, fmt.Errorf("abriendo el SCM: %w", err)
	}
	m := &mgr.Mgr{Handle: h}
	defer m.Disconnect()

	name, err := windows.UTF16PtrFromString(Name)
	if err != nil {
		return nil, err
	}
	sh, err := windows.OpenService(m.Handle, name, windows.SERVICE_QUERY_STATUS|windows.SERVICE_QUERY_CONFIG)
	if err != nil {
		return &Info{Installed: false}, nil
	}
	s := &mgr.Service{Name: Name, Handle: sh}
	defer s.Close()

	info := &Info{Installed: true}

	if status, err := s.Query(); err == nil {
		info.State = status.State
		info.ProcessID = status.ProcessId
	}
	if cfg, err := s.Config(); err == nil {
		info.StartType = cfg.StartType
		info.BinaryPath = cfg.BinaryPathName
		info.Account = cfg.ServiceStartName
	}
	if ra, err := s.RecoveryActions(); err == nil {
		info.Recovery = ra
	}
	return info, nil
}

// waitForState hace polling hasta que el servicio alcanza want o se
// agota waitTimeout. El SCM es asíncrono: Start()/Control() vuelven en
// cuanto se acepta la petición, no cuando se completó.
func waitForState(s *mgr.Service, want svc.State) error {
	deadline := time.Now().Add(waitTimeout)
	for time.Now().Before(deadline) {
		status, err := s.Query()
		if err != nil {
			return fmt.Errorf("consultando estado: %w", err)
		}
		if status.State == want {
			return nil
		}
		time.Sleep(300 * time.Millisecond)
	}
	return fmt.Errorf("el servicio no alcanzó el estado %s en %v — revisa el log del agent y el Visor de eventos",
		StateName(want), waitTimeout)
}

// StateName traduce el estado del SCM a algo legible en español.
func StateName(s svc.State) string {
	switch s {
	case svc.Stopped:
		return "DETENIDO"
	case svc.StartPending:
		return "ARRANCANDO"
	case svc.StopPending:
		return "DETENIÉNDOSE"
	case svc.Running:
		return "CORRIENDO"
	case svc.ContinuePending:
		return "REANUDANDO"
	case svc.PausePending:
		return "PAUSÁNDOSE"
	case svc.Paused:
		return "PAUSADO"
	default:
		return fmt.Sprintf("DESCONOCIDO(%d)", s)
	}
}

// StartTypeName traduce el tipo de arranque del SCM.
func StartTypeName(t uint32) string {
	switch t {
	case mgr.StartAutomatic:
		return "Automático"
	case mgr.StartManual:
		return "Manual"
	case mgr.StartDisabled:
		return "Deshabilitado"
	// mgr no exporta constantes para estos dos (son de drivers, no de
	// servicios de usuario), así que usamos las de windows.
	case windows.SERVICE_BOOT_START:
		return "Boot"
	case windows.SERVICE_SYSTEM_START:
		return "System"
	default:
		return fmt.Sprintf("desconocido(%d)", t)
	}
}

// RecoveryActionName traduce una acción de recuperación.
func RecoveryActionName(a mgr.RecoveryAction) string {
	switch a.Type {
	case mgr.NoAction:
		return "no hacer nada"
	case mgr.ServiceRestart:
		return fmt.Sprintf("reiniciar el servicio tras %v", a.Delay)
	case mgr.ComputerReboot:
		return fmt.Sprintf("reiniciar el equipo tras %v", a.Delay)
	case mgr.RunCommand:
		return fmt.Sprintf("ejecutar comando tras %v", a.Delay)
	default:
		return fmt.Sprintf("acción desconocida(%d)", a.Type)
	}
}
