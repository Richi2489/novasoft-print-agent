// Package launchd — el agente como LaunchAgent de macOS (equivalente al
// servicio de Windows).
//
// Un LaunchAgent vive en ~/Library/LaunchAgents y lo arranca launchd en
// cuanto el usuario inicia sesión (RunAtLoad) y lo vuelve a levantar si
// el proceso muere (KeepAlive). Se eligió LaunchAgent (por usuario) y no
// LaunchDaemon (de sistema, requiere root) porque:
//   - no pide contraseña de administrador para instalarse;
//   - el config ya vive por usuario en ~/Library/Application Support;
//   - en una caja de restaurante el Mac siempre tiene la sesión abierta.
//
// Contrapartida: si el Mac reinicia y NADIE inicia sesión, no imprime.
// Se documenta en docs/INSTALAR-MAC.md (activar inicio de sesión
// automático).
//
// La generación del plist es pura (testeable en cualquier OS); lo que
// llama a launchctl sólo tiene sentido en macOS.
package launchd

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// Label — identificador del job en launchd. Cambiarlo rompe
// instalaciones existentes (habría que desinstalar con el viejo).
const Label = "mx.novasoft.printagent"

// EnvMarker — variable que el plist le pone al proceso para que `run`
// sepa que lo arrancó launchd (sin consola: log sólo a archivo).
const EnvMarker = "NOVASOFT_AGENT_LAUNCHD"

// PlistPath — ~/Library/LaunchAgents/mx.novasoft.printagent.plist
func PlistPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", Label+".plist"), nil
}

// IsLaunchd indica si este proceso lo arrancó nuestro LaunchAgent.
func IsLaunchd() bool {
	return os.Getenv(EnvMarker) == "1"
}

func esc(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

// Plist genera el contenido del LaunchAgent.
//
//	exe      ruta absoluta del binario
//	stdLog   archivo para stdout/stderr del proceso (sólo pánicos o
//	         errores antes de abrir el log propio; el log normal lo rota
//	         el agente en logs/agent.log)
func Plist(exe, stdLog string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>` + Label + `</string>
	<key>ProgramArguments</key>
	<array>
		<string>` + esc(exe) + `</string>
		<string>run</string>
	</array>
	<key>EnvironmentVariables</key>
	<dict>
		<key>` + EnvMarker + `</key>
		<string>1</string>
	</dict>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
	<key>ThrottleInterval</key>
	<integer>10</integer>
	<key>ProcessType</key>
	<string>Interactive</string>
	<key>StandardOutPath</key>
	<string>` + esc(stdLog) + `</string>
	<key>StandardErrorPath</key>
	<string>` + esc(stdLog) + `</string>
</dict>
</plist>
`
}

func uid() string { return strconv.Itoa(os.Getuid()) }

func domain() string { return "gui/" + uid() }

func target() string { return domain() + "/" + Label }

// launchctl corre launchctl y devuelve salida combinada.
func launchctl(args ...string) (string, error) {
	out, err := exec.Command("launchctl", args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// Install escribe el plist y lo carga (arranca el agente ya).
// Idempotente: si ya estaba cargado lo descarga primero.
func Install(exe, stdLog string) (string, error) {
	path, err := PlistPath()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("creando %s: %w", filepath.Dir(path), err)
	}
	if err := os.MkdirAll(filepath.Dir(stdLog), 0o700); err != nil {
		return "", fmt.Errorf("creando %s: %w", filepath.Dir(stdLog), err)
	}
	_ = unload(path)
	if err := os.WriteFile(path, []byte(Plist(exe, stdLog)), 0o644); err != nil {
		return "", fmt.Errorf("escribiendo %s: %w", path, err)
	}
	if err := load(path); err != nil {
		return path, err
	}
	return path, nil
}

// Uninstall descarga el job y borra el plist.
func Uninstall() error {
	path, err := PlistPath()
	if err != nil {
		return err
	}
	_ = unload(path)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("borrando %s: %w", path, err)
	}
	return nil
}

// Start carga el plist (si no lo estaba) y fuerza el arranque.
func Start() error {
	path, err := PlistPath()
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("el agente no está instalado (%s no existe) — corre 'novasoft-agent service install'", path)
	}
	if st := Query(); !st.Loaded {
		return load(path)
	}
	if out, err := launchctl("kickstart", "-k", target()); err != nil {
		return fmt.Errorf("launchctl kickstart: %v %s", err, out)
	}
	return nil
}

// Stop descarga el job (KeepAlive lo relanzaría si sólo se mata).
// Vuelve a arrancar al iniciar sesión o con `service start`.
func Stop() error {
	path, err := PlistPath()
	if err != nil {
		return err
	}
	return unload(path)
}

// Status — lo que sabemos de launchd.
type Status struct {
	Installed bool   // existe el plist
	Loaded    bool   // launchd lo conoce
	Running   bool   // hay proceso vivo
	PID       int    //
	Raw       string // salida de launchctl print (diagnóstico)
}

// Query consulta a launchd. Nunca falla: en el peor caso Loaded=false.
func Query() Status {
	var st Status
	if path, err := PlistPath(); err == nil {
		if _, err := os.Stat(path); err == nil {
			st.Installed = true
		}
	}
	out, err := launchctl("print", target())
	if err != nil {
		return st
	}
	st.Loaded = true
	st.Raw = out
	st.Running, st.PID = parsePrint(out)
	return st
}

// parsePrint extrae "state = running" y "pid = 123" de `launchctl print`.
func parsePrint(out string) (bool, int) {
	running := false
	pid := 0
	for _, line := range strings.Split(out, "\n") {
		l := strings.TrimSpace(line)
		if strings.HasPrefix(l, "state = ") {
			running = strings.TrimSpace(strings.TrimPrefix(l, "state = ")) == "running"
		}
		if strings.HasPrefix(l, "pid = ") && pid == 0 {
			pid, _ = strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(l, "pid = ")))
		}
	}
	return running, pid
}

// load: `launchctl bootstrap gui/<uid> plist` (macOS 10.10+), con
// fallback a `launchctl load -w` (API vieja, sigue funcionando).
func load(path string) error {
	out, err := launchctl("bootstrap", domain(), path)
	if err == nil {
		return nil
	}
	out2, err2 := launchctl("load", "-w", path)
	if err2 == nil {
		return nil
	}
	return fmt.Errorf("no pude cargar el LaunchAgent: bootstrap: %v %s; load: %v %s", err, out, err2, out2)
}

func unload(path string) error {
	if _, err := launchctl("bootout", target()); err == nil {
		return nil
	}
	_, err := launchctl("unload", "-w", path)
	return err
}
