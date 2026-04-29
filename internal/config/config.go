// Package config — persistencia JSON del emparejamiento del agent.
//
// Formato del archivo:
//   {
//     "agent_id": "uuid...",
//     "agent_token": "secret-token",
//     "websocket_url": "wss://api.novasoft.mx/ws/printing",
//     "backend_url": "https://api.novasoft.mx",
//     "printer_name": ""
//   }
//
// El archivo se escribe con permisos 0600 (solo propietario en Unix; en
// Windows hereda ACL del directorio padre). El token que guarda es
// equivalente a una credencial — cualquiera con acceso al archivo puede
// imprimir como este agent.
//
// Ubicación (v0.3.0+): %PROGRAMDATA%\NovaSoft\config.json (Windows).
// La ruta cambió desde v0.2.x para que el servicio Windows (LocalSystem)
// pueda leer el config — ver internal/paths para racional. Load()
// migra automáticamente el config legacy de %APPDATA%\NovaSoftPrintAgent
// si existe.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"

	"github.com/Richi2489/novasoft-print-agent/internal/paths"
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

// Path devuelve la ruta absoluta del archivo de config según el OS.
// No crea el directorio — Save() lo hace.
func Path() (string, error) {
	dir, err := paths.DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

// Load lee el archivo de config. Si no existe en la ubicación oficial
// (v0.3.0+), intenta migrar desde la ubicación legacy v0.2.x. Si tampoco
// existe ahí, devuelve ErrNotConfigured.
//
// La migración solo aplica cuando un usuario interactivo corre el agent
// (las variables APPDATA y HOME apuntan a su perfil real). El servicio
// LocalSystem no puede ver el %APPDATA% del usuario; en ese caso la
// migración es no-op y el servicio reporta ErrNotConfigured hasta que
// alguien corra `pair` interactivo o el setup.exe migre la config.
func Load() (*Config, error) {
	path, err := Path()
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path)
	if err == nil {
		cfg, err := parseConfig(data, path)
		if err != nil {
			return nil, err
		}
		// Auto-fix de URL Railway legacy aún en configs ya migrados —
		// cubre RichiLap (que migró en testing v0.3.0 antes del fix)
		// y cualquier otro caso donde un cliente pre-fix tenga el
		// config en %PROGRAMDATA% con URL stale.
		if maybeFixLegacyBackendURL(cfg) {
			if err := Save(cfg); err != nil {
				log.Printf("⚠ no pude persistir el rewrite de backend URL: %v", err)
			}
		}
		return cfg, nil
	}
	if !os.IsNotExist(err) {
		return nil, fmt.Errorf("leyendo config: %w", err)
	}

	// Path nuevo no existe — probar migración desde v0.2.x.
	cfg, migrated, err := tryMigrateLegacy()
	if err != nil {
		// Error real al leer/parsear legacy — propagamos.
		return nil, err
	}
	if !migrated {
		// Ni nuevo ni legacy — no hay config.
		return nil, ErrNotConfigured
	}
	return cfg, nil
}

// maybeFixLegacyBackendURL aplica el rewrite Railway → api.novasoft.mx
// si el cfg viene apuntando al URL Railway directo. Devuelve true si
// modificó el cfg (caller debe Save). String-match exacto: URLs custom
// (preview deploys, tests internos) son preservadas.
func maybeFixLegacyBackendURL(cfg *Config) bool {
	if cfg.BackendURL == LegacyRailwayBackendURL {
		log.Printf("✓ config: backend URL actualizado de Railway directo a %s", CurrentBackendURL)
		cfg.BackendURL = CurrentBackendURL
		return true
	}
	return false
}

// parseConfig deserializa los bytes del archivo y valida campos
// obligatorios. El path se usa solo para mensajes de error.
func parseConfig(data []byte, path string) (*Config, error) {
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("config corrupta en %s: %w", path, err)
	}
	if cfg.AgentToken == "" {
		// Token vacío = config inservible. websocket_url puede estar
		// vacío en configs migradas — el agent reconstruye el endpoint
		// desde BackendURL.
		return nil, ErrNotConfigured
	}
	return &cfg, nil
}

// LegacyRailwayBackendURL es la URL directa que clientes v0.2.x tenían
// hardcoded como DefaultBackendURL. F-002 sprint agregó TrustedHostMiddleware
// al backend que rechaza esta URL con HTTP 400 — los clientes v0.2.x se
// quedan en retry loop infinito post-upgrade si no se rewrites.
const LegacyRailwayBackendURL = "https://novasoft-backend-production.up.railway.app"

// CurrentBackendURL es el dominio canónico (paso por TrustedHostMiddleware).
// Debe coincidir con DefaultBackendURL en cmd/agent/main.go.
const CurrentBackendURL = "https://api.novasoft.mx"

// tryMigrateLegacy busca un config v0.2.x en la ruta vieja y lo copia a
// la ruta nueva (v0.3.0+). Devuelve (cfg, true, nil) si migró
// exitosamente, (nil, false, nil) si no hay config legacy, o
// (nil, false, err) si encontró el archivo pero no pudo procesarlo.
//
// Side-effect importante: si el config legacy tiene BackendURL =
// LegacyRailwayBackendURL, se reescribe a CurrentBackendURL antes de
// guardar. Esto repara automáticamente el upgrade-path roto desde
// F-002 (TrustedHostMiddleware rechaza la URL Railway directa).
//
// Si la migración a la ruta nueva falla (ej. permission denied porque
// %PROGRAMDATA% no es writable como el usuario actual), devolvemos
// (cfg, true, nil) — al menos el caller puede operar con el config
// leído, aunque no quede persistido en la ubicación nueva.
func tryMigrateLegacy() (*Config, bool, error) {
	legacy := paths.LegacyConfigPath()
	if legacy == "" {
		return nil, false, nil
	}
	data, err := os.ReadFile(legacy)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("leyendo config legacy %s: %w", legacy, err)
	}
	cfg, err := parseConfig(data, legacy)
	if err != nil {
		return nil, false, err
	}

	// Auto-fix de URL Railway legacy → api.novasoft.mx (helper
	// compartido con Load para que clientes ya migrados también
	// reciban el rewrite).
	maybeFixLegacyBackendURL(cfg)

	// Best-effort: copiar a la ubicación nueva. Si falla (ej. el usuario
	// interactivo no es admin y %PROGRAMDATA%\NovaSoft\ no existe ni
	// es escribible), seguimos retornando el cfg leído del legacy.
	if err := Save(cfg); err != nil {
		log.Printf("⚠ migración config: leí %s pero no pude guardar en ubicación nueva: %v", legacy, err)
		return cfg, true, nil
	}

	// Borrar el legacy ahora que está duplicado en el nuevo path.
	// Si el remove falla (ej. archivo abierto), no es crítico — el
	// próximo Load() lee el nuevo path y nunca toca el legacy.
	if err := os.Remove(legacy); err != nil {
		log.Printf("⚠ migración config: copié pero no pude borrar legacy %s: %v", legacy, err)
	} else {
		log.Printf("✓ config migrado de %s a la ubicación nueva", legacy)
	}
	return cfg, true, nil
}

// Save escribe el config con permisos 0600 (solo propietario en Unix).
// Crea el directorio si hace falta. El archivo nuevo reemplaza
// atómicamente al anterior vía rename para evitar corrupción si el
// proceso muere en medio de la escritura.
func Save(cfg *Config) error {
	if cfg == nil {
		return fmt.Errorf("Save: cfg es nil")
	}
	path, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
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

	// Fija permisos antes de escribir — en Windows son no-op pero en
	// Unix garantiza que los datos secretos no queden world-readable
	// ni siquiera por un instante.
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
func Delete() error {
	path, err := Path()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
