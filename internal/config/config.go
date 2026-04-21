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
// El archivo se escribe con permisos 0600 (solo propietario). El token
// que guarda es equivalente a una credencial — cualquiera con acceso al
// archivo puede imprimir como este agent.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
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
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

// configDir resuelve el directorio de config por OS siguiendo las
// convenciones estándar:
//   - Windows: %APPDATA%\NovaSoftPrintAgent
//   - macOS:   ~/Library/Application Support/NovaSoftPrintAgent
//   - Linux:   ~/.config/novasoft-print-agent  (o $XDG_CONFIG_HOME)
func configDir() (string, error) {
	switch runtime.GOOS {
	case "windows":
		appdata := os.Getenv("APPDATA")
		if appdata == "" {
			return "", fmt.Errorf("APPDATA no definida — ambiente Windows roto")
		}
		return filepath.Join(appdata, "NovaSoftPrintAgent"), nil
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

// Load lee el archivo de config. Si no existe, devuelve ErrNotConfigured
// para que el caller pueda distinguir "no emparejado" de "archivo
// corrupto".
func Load() (*Config, error) {
	path, err := Path()
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotConfigured
		}
		return nil, fmt.Errorf("leyendo config: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("config corrupta en %s: %w", path, err)
	}
	if cfg.AgentToken == "" || cfg.WebsocketURL == "" {
		// Config existe pero incompleta — probablemente una escritura parcial
		// de un crash. Mejor tratar como no-configurado.
		return nil, ErrNotConfigured
	}
	return &cfg, nil
}

// Save escribe el config con permisos 0600 (solo propietario). Crea el
// directorio si hace falta. El archivo nuevo reemplaza atómicamente al
// anterior vía rename para evitar corrupción si el proceso muere en
// medio de la escritura.
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
