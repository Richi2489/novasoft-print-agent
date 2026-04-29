// Package paths centraliza las rutas del sistema de archivos donde el
// agent guarda config + logs.
//
// Decisión de ubicación (v0.3.0):
//   Windows:  %PROGRAMDATA%\NovaSoft\
//   macOS:    /Library/Application Support/NovaSoft/
//   Linux:    /var/lib/novasoft/  (o $NOVASOFT_DATA_DIR si está set)
//
// Por qué %PROGRAMDATA% en lugar de %APPDATA% (cambio desde v0.2.x):
// el servicio Windows corre como LocalSystem; LocalSystem no tiene
// acceso al %APPDATA% del usuario logueado. %PROGRAMDATA% es legible
// y escribible por cualquier sesión + por el servicio. Single source
// de truth tanto para CLI interactivo como para el servicio.
//
// Migración: config.Load() detecta config legacy en
// %APPDATA%\NovaSoftPrintAgent\config.json y la copia automáticamente.
package paths

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

const (
	// AppName aparece en directorios de instalación, registry, y servicios.
	AppName = "NovaSoft"

	// LegacyAppName era la convención v0.2.x — un solo subdirectorio
	// dedicado al agent en lugar de "NovaSoft" como umbrella.
	LegacyAppName = "NovaSoftPrintAgent"
)

// DataDir devuelve el directorio donde el agent persiste config + logs.
// El directorio puede no existir todavía; el caller lo crea con MkdirAll
// antes de escribir.
func DataDir() (string, error) {
	switch runtime.GOOS {
	case "windows":
		programData := os.Getenv("PROGRAMDATA")
		if programData == "" {
			// Fallback para Windows raros sin PROGRAMDATA seteado.
			programData = `C:\ProgramData`
		}
		return filepath.Join(programData, AppName), nil
	case "darwin":
		return filepath.Join("/Library", "Application Support", AppName), nil
	default:
		// Linux/BSD: respeta NOVASOFT_DATA_DIR si está, sino /var/lib.
		if override := os.Getenv("NOVASOFT_DATA_DIR"); override != "" {
			return override, nil
		}
		return filepath.Join("/var", "lib", "novasoft"), nil
	}
}

// LegacyConfigPath devuelve la ruta donde v0.2.x guardaba el config.
// Solo aplica en Windows y macOS — en Linux no hubo despliegues v0.2.x.
// Vacío si no aplica al SO actual.
func LegacyConfigPath() string {
	switch runtime.GOOS {
	case "windows":
		appdata := os.Getenv("APPDATA")
		if appdata == "" {
			return ""
		}
		return filepath.Join(appdata, LegacyAppName, "config.json")
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		return filepath.Join(home, "Library", "Application Support", LegacyAppName, "config.json")
	default:
		return ""
	}
}

// EnsureDataDir crea el DataDir si no existe. Permisos 0o755 — los
// archivos individuales (config.json) se escriben con 0o600 en Unix.
// En Windows estos modes son no-op pero los dejamos por consistencia.
func EnsureDataDir() (string, error) {
	dir, err := DataDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("creando %s: %w", dir, err)
	}
	return dir, nil
}
