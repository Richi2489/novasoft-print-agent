// Package config — persistencia JSON del emparejamiento del agent.
//
// Formato del archivo:
//
//	{
//	  "agent_id": "uuid...",
//	  "agent_token": "secret-token",
//	  "websocket_url": "wss://api.novasoft.mx/ws/printing",
//	  "backend_url": "https://api.novasoft.mx",
//	  "printer_name": ""
//	}
//
// El token que guarda es equivalente a una credencial — cualquiera con
// acceso al archivo puede imprimir como este agent.
//
// # Ubicación en Windows: por máquina, no por usuario
//
// Desde v0.4.0 el agent puede correr como servicio de Windows bajo la
// cuenta LocalSystem. Esa cuenta tiene su propio %APPDATA%
// (C:\Windows\system32\config\systemprofile\AppData\Roaming), distinto
// al del usuario que corrió `pair`. Si dejáramos el config en %APPDATA%
// el servicio nunca encontraría config.json y el agent quedaría
// inservible.
//
// Por eso en Windows el config vive en un directorio de máquina:
//
//	%PROGRAMDATA%\NovaSoftAgent\config.json
//
// que SYSTEM y el usuario ven igual. Las rutas viejas se migran
// automáticamente la primera vez que se lee — ver MigrateLegacy. Un agent
// ya emparejado NO tiene que volver a emparejarse al actualizar:
//
//	%PROGRAMDATA%\NovaSoft\config.json          (v0.3.0, servicio + bandeja)
//	%APPDATA%\NovaSoftPrintAgent\config.json    (v0.2.x)
//
// La de v0.3.0 importa: es la que tiene La Capital. Sin migrarla, el
// instalador v0.4.x encontraba "no emparejado" y pedía un código nuevo,
// que crea OTRA impresora en el backend (las cajas que la tenían elegida
// quedaban apuntando a la vieja).
//
// En macOS/Linux no hay servicio de Windows, así que se conservan las
// rutas por usuario de siempre.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Config es el estado persistente del agent emparejado.
type Config struct {
	AgentID      string `json:"agent_id"`
	AgentToken   string `json:"agent_token"`
	WebsocketURL string `json:"websocket_url"`
	BackendURL   string `json:"backend_url"`
	// PrinterName es opcional — si vacío, el agent auto-selecciona la
	// primera impresora térmica detectada. Útil para pinear a un device
	// específico cuando hay varias conectadas.
	PrinterName string `json:"printer_name,omitempty"`
}

// ErrNotConfigured se retorna cuando no hay config guardada.
// main.go la detecta para mostrar un mensaje amigable.
var ErrNotConfigured = errors.New("no hay configuración — ejecuta 'pair' primero")

// utf8BOM — el marcador que el Bloc de notas y PowerShell anteponen al
// guardar "como UTF-8". encoding/json NO lo tolera y falla con
// "invalid character 'ï'", un error que no le dice nada a nadie.
//
// Nosotros nunca lo escribimos, pero sí pasa que alguien de soporte abre
// config.json para fijar printer_name a mano y lo vuelve a guardar con
// BOM. Tolerarlo cuesta una línea y evita un ticket.
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// decode parsea el JSON del config tolerando el BOM. path solo se usa
// para el mensaje de error.
func decode(data []byte, path string) (*Config, error) {
	var cfg Config
	if err := json.Unmarshal(bytes.TrimPrefix(data, utf8BOM), &cfg); err != nil {
		return nil, fmt.Errorf("config corrupta en %s: %w", path, err)
	}
	return &cfg, nil
}

// DataDir devuelve el directorio base de datos del agent (config + logs).
// No lo crea — Save() / EnsureDataDir() lo hacen.
//
//   - Windows: %PROGRAMDATA%\NovaSoftAgent   (por máquina — ver doc del paquete)
//   - macOS:   ~/Library/Application Support/NovaSoftPrintAgent
//   - Linux:   $XDG_CONFIG_HOME/novasoft-print-agent  (o ~/.config/…)
func DataDir() (string, error) {
	switch runtime.GOOS {
	case "windows":
		programData := os.Getenv("PROGRAMDATA")
		if programData == "" {
			return "", fmt.Errorf("PROGRAMDATA no definida — ambiente Windows roto")
		}
		return filepath.Join(programData, "NovaSoftAgent"), nil
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, "Library", "Application Support", "NovaSoftPrintAgent"), nil
	default: // linux, bsd, etc.
		if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
			return filepath.Join(xdg, "novasoft-print-agent"), nil
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, ".config", "novasoft-print-agent"), nil
	}
}

// EnsureDataDir crea el directorio de datos si no existe.
func EnsureDataDir() (string, error) {
	dir, err := DataDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("creando %s: %w", dir, err)
	}
	return dir, nil
}

// Path devuelve la ruta absoluta del archivo de config según el OS.
// No crea el directorio — Save() lo hace.
func Path() (string, error) {
	dir, err := DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

// LegacyPath devuelve la ruta donde vivía el config en v0.2.x
// (%APPDATA%\NovaSoftPrintAgent\config.json en Windows). Devuelve ""
// en plataformas donde la ruta nunca cambió.
func LegacyPath() string {
	if runtime.GOOS != "windows" {
		return ""
	}
	appdata := os.Getenv("APPDATA")
	if appdata == "" {
		return ""
	}
	return filepath.Join(appdata, "NovaSoftPrintAgent", "config.json")
}

// LegacyV030Path es donde v0.3.0 (servicio + icono de bandeja) guardaba el
// config: %PROGRAMDATA%\NovaSoft\config.json. Al ser de máquina, también lo
// ve el servicio corriendo como LocalSystem.
func LegacyV030Path() string {
	if runtime.GOOS != "windows" {
		return ""
	}
	programData := os.Getenv("PROGRAMDATA")
	if programData == "" {
		return ""
	}
	return filepath.Join(programData, "NovaSoft", "config.json")
}

// LegacyPaths son las rutas viejas en orden de preferencia (la más nueva
// primero).
func LegacyPaths() []string {
	var out []string
	for _, p := range []string{LegacyV030Path(), LegacyPath()} {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// legacyRailwayBackendURL — URL directa a Railway que algunos configs
// viejos guardaron; el backend la rechaza (TrustedHostMiddleware).
const legacyRailwayBackendURL = "https://novasoft-backend-production.up.railway.app"
const currentBackendURL = "https://api.novasoft.mx"

// MigrateLegacy copia el config de la ruta vieja (%APPDATA%) a la nueva
// (%PROGRAMDATA%) si —y solo si— la nueva todavía no existe y la vieja
// sí. Devuelve true si migró algo.
//
// Es idempotente y best-effort: cualquier error se devuelve al caller
// pero Load() lo ignora a propósito, porque un fallo de migración no
// debe romper un agent que ya está bien configurado.
//
// NO borra el archivo viejo: si el operador hace rollback a una versión
// anterior del binario, el agent sigue funcionando sin re-emparejar.
//
// Importante: esto solo puede funcionar cuando lo corre el usuario que
// hizo el `pair` (LocalSystem tiene otro %APPDATA%). Por eso
// `service install` la invoca explícitamente antes de crear el servicio.
func MigrateLegacy() (bool, error) {
	newPath, err := Path()
	if err != nil {
		return false, err
	}

	// ¿Ya hay config en la ruta nueva? Entonces manda esa — no pisamos.
	if _, err := os.Stat(newPath); err == nil {
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, fmt.Errorf("revisando %s: %w", newPath, err)
	}

	var primerError error
	for _, legacy := range LegacyPaths() {
		if legacy == newPath {
			continue
		}
		migrated, err := migrateFrom(legacy)
		if migrated {
			return true, nil
		}
		if err != nil && primerError == nil {
			primerError = err
		}
	}
	return false, primerError
}

// migrateFrom copia un config viejo válido a la ruta nueva.
func migrateFrom(legacy string) (bool, error) {
	data, err := os.ReadFile(legacy)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil // nada que migrar en esta ruta
		}
		return false, fmt.Errorf("leyendo config viejo %s: %w", legacy, err)
	}

	// Validamos antes de copiar: no tiene sentido migrar basura.
	cfg, err := decode(data, legacy)
	if err != nil {
		return false, err
	}
	if cfg.AgentToken == "" {
		return false, nil // incompleto — que el usuario haga pair de nuevo
	}
	if cfg.BackendURL == "" || cfg.BackendURL == legacyRailwayBackendURL {
		cfg.BackendURL = currentBackendURL
	}
	if cfg.WebsocketURL == "" {
		// Load() exige el campo (señal de escritura completa); el agent no
		// lo usa desde el pivot a SSE.
		cfg.WebsocketURL = strings.TrimRight(cfg.BackendURL, "/") + "/printing/agents/stream"
	}

	if err := Save(cfg); err != nil {
		newPath, _ := Path()
		return false, fmt.Errorf("migrando config a %s: %w", newPath, err)
	}
	return true, nil
}

// Load lee el archivo de config. Si no existe, intenta migrar desde la
// ruta vieja (%APPDATA%) antes de rendirse; así un agent emparejado con
// una versión anterior sigue funcionando sin re-emparejar.
//
// Si no hay nada en ninguna de las dos rutas devuelve ErrNotConfigured
// para que el caller pueda distinguir "no emparejado" de "archivo
// corrupto".
func Load() (*Config, error) {
	path, err := Path()
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("leyendo config: %w", err)
		}
		// Puede ser un agent emparejado con una versión anterior (v0.3.0
		// en %PROGRAMDATA%\NovaSoft, o v0.2.x en %APPDATA%).
		// Best-effort: si la migración falla seguimos reportando
		// ErrNotConfigured, que es el mensaje accionable.
		migrated, mErr := MigrateLegacy()
		if mErr != nil || !migrated {
			return nil, ErrNotConfigured
		}
		if data, err = os.ReadFile(path); err != nil {
			return nil, ErrNotConfigured
		}
	}

	cfg, err := decode(data, path)
	if err != nil {
		return nil, err
	}
	if cfg.AgentToken == "" || cfg.WebsocketURL == "" {
		// Config existe pero incompleta — probablemente una escritura parcial
		// de un crash. Mejor tratar como no-configurado.
		return nil, ErrNotConfigured
	}
	return cfg, nil
}

// Save escribe el config. Crea el directorio si hace falta. El archivo
// nuevo reemplaza atómicamente al anterior vía rename para evitar
// corrupción si el proceso muere en medio de la escritura.
//
// En Windows el directorio vive bajo %PROGRAMDATA%, cuyo ACL por
// defecto da control total a SYSTEM y Administradores y solo lectura al
// resto de usuarios. Escribir ahí requiere permisos de administrador —
// por eso `pair` se documenta como "PowerShell como administrador".
func Save(cfg *Config) error {
	if cfg == nil {
		return fmt.Errorf("Save: cfg es nil")
	}
	path, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("creando dir: %w", err)
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("serializando: %w", err)
	}

	// Escritura atómica: tmp → rename. Evita que un crash deje config.json
	// truncado a 0 bytes y rompa el próximo Load.
	tmp, err := os.CreateTemp(filepath.Dir(path), "config-*.tmp")
	if err != nil {
		return fmt.Errorf("creando tmp: %w", err)
	}
	tmpPath := tmp.Name()

	// Fija permisos antes de escribir — en Windows son no-op (ahí manda
	// el ACL heredado de %PROGRAMDATA%) pero en Unix garantiza que los
	// datos secretos no queden world-readable ni siquiera por un instante.
	if runtime.GOOS != "windows" {
		_ = tmp.Chmod(0o600)
	}

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("escribiendo tmp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("cerrando tmp: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

// Delete borra el archivo de config. Idempotente — no falla si ya no existe.
// También limpia los archivos de las rutas viejas para que un `unpair` no
// deje un config zombie que una futura migración vuelva a resucitar.
func Delete() error {
	path, err := Path()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, legacy := range LegacyPaths() {
		if legacy == path {
			continue
		}
		if err := os.Remove(legacy); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}
