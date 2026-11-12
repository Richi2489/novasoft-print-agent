//go:build windows

// Partes de la CLI exclusivas de Windows: servicio del SCM y textos que
// mencionan novasoft-agent.exe / PowerShell. Movidas tal cual desde
// main.go cuando se agregó el agente de macOS (os_unix.go): el
// comportamiento en Windows no cambia.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"

	"github.com/Richi2489/novasoft-print-agent/internal/config"
	"github.com/Richi2489/novasoft-print-agent/internal/logging"
	"github.com/Richi2489/novasoft-print-agent/internal/winsvc"
)

func usage() {
	fmt.Fprintln(os.Stderr, `NovaSoft Print Agent `+Version+`

Uso:
  novasoft-agent.exe [-backend=URL] <comando>

Comandos:
  pair       Empareja con un restaurante usando un código del wizard
             (pair ABCD-1234-WXYZ, o sin código para teclearlo).
  run       Arranca el loop principal en esta consola — escucha jobs.
  service    Administra el servicio de Windows (ver abajo).
  status     Muestra la config actual y las impresoras detectadas.
  unpair     Borra la config local.
  version    Imprime la versión.

Subcomandos de service (install/uninstall/start/stop requieren
PowerShell "Ejecutar como administrador"):
  service install     Instala el servicio con arranque automático y
                      reinicio ante fallo. Es la forma recomendada.
  service uninstall   Detiene y elimina el servicio.
  service start       Arranca el servicio.
  service stop        Detiene el servicio.
  service status      Estado, cuenta, recuperación y ruta del log.

Ejemplos:
  novasoft-agent.exe pair
  novasoft-agent.exe service install
  novasoft-agent.exe service status
  novasoft-agent.exe -backend=https://api.novasoft.mx run
`)
}

// cmdRun decide entre modo servicio y modo consola.
//
// El SCM arranca el binario con el mismo subcomando `run`, así que la
// única diferencia la marca svc.IsWindowsService(). Mantener un solo
// subcomando evita el clásico bug de instalar el servicio apuntando a un
// modo y probar a mano el otro.
func cmdRun() error {
	if winsvc.IsService() {
		return runAsService()
	}
	return runInteractive()
}

// runAsService entrega el control al SCM. Sin consola: todo va al log de
// archivo y los hitos al Event Log de Windows.
func runAsService() error {
	// El log a archivo es obligatorio aquí: sin él el servicio sería una
	// caja negra. Si no se puede abrir, mejor fallar y que el SCM lo
	// reporte que correr a ciegas.
	logPath, closer, err := logging.Setup(false)
	if err != nil {
		return fmt.Errorf("no pude abrir el log en %s: %w", logPath, err)
	}
	if closer != nil {
		defer closer.Close()
	}

	log.Printf("=== NovaSoft Print Agent %s — modo servicio ===", Version)
	return winsvc.Run(func(ctx context.Context) error {
		return runAgent(ctx)
	})
}

// cmdService despacha los subcomandos de administración del servicio.
func cmdService(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("falta el subcomando — usa: service install|uninstall|start|stop|status")
	}

	switch args[0] {
	case "install":
		return cmdServiceInstall()

	case "uninstall":
		if err := winsvc.Uninstall(); err != nil {
			return err
		}
		fmt.Printf("✓ Servicio %q desinstalado.\n", winsvc.Name)
		fmt.Println()
		fmt.Println("La configuración y los logs NO se borraron. Si quieres")
		fmt.Println("dejar el equipo limpio del todo:")
		dir, _ := config.DataDir()
		fmt.Println("  novasoft-agent.exe unpair    (borra el token)")
		fmt.Printf("  y elimina a mano: %s\n", dir)
		return nil

	case "start":
		if err := winsvc.Start(); err != nil {
			return err
		}
		fmt.Printf("✓ Servicio %q arrancado.\n", winsvc.Name)
		logPath, _ := logging.Path()
		fmt.Printf("  Log: %s\n", logPath)
		return nil

	case "stop":
		if err := winsvc.Stop(); err != nil {
			return err
		}
		fmt.Printf("✓ Servicio %q detenido.\n", winsvc.Name)
		return nil

	case "status":
		return cmdServiceStatus()

	default:
		return fmt.Errorf("subcomando de service desconocido: %s — usa: install|uninstall|start|stop|status", args[0])
	}
}

// cmdServiceInstall instala el servicio, migrando antes el config si
// venía de una versión que lo guardaba en %APPDATA%.
//
// El orden importa: la migración tiene que correr AQUÍ, con el usuario
// que hizo el `pair` (y su %APPDATA%), porque una vez instalado el
// servicio corre como LocalSystem y ya no vería ese directorio.
func cmdServiceInstall() error {
	// 1. Migración de config vieja → %PROGRAMDATA%.
	migrated, err := config.MigrateLegacy()
	if err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  no pude migrar la config vieja: %v\n", err)
	} else if migrated {
		newPath, _ := config.Path()
		fmt.Println("✓ Config migrada desde una versión anterior del agente")
		fmt.Printf("  a %s (no hace falta volver a emparejar).\n\n", newPath)
	}

	// 2. Aviso temprano si todavía no hay emparejamiento. No abortamos:
	// instalar primero y emparejar después es un orden válido.
	pairedOK := true
	if _, err := config.Load(); err != nil {
		pairedOK = false
	}

	// 3. Crear el servicio.
	if err := winsvc.Install(); err != nil {
		return err
	}

	exe, _ := winsvc.ExePath()
	logPath, _ := logging.Path()
	cfgPath, _ := config.Path()

	fmt.Printf("✓ Servicio %q instalado.\n\n", winsvc.Name)
	fmt.Printf("  Nombre:      %s (%s)\n", winsvc.Name, winsvc.DisplayName)
	fmt.Printf("  Ejecutable:  %s run\n", exe)
	fmt.Println("  Cuenta:      LocalSystem")
	fmt.Println("  Arranque:    Automático (con el equipo, sin necesidad de iniciar sesión)")
	fmt.Println("  Recuperación: reinicio a los 5 s, 5 s y luego cada 30 s")
	fmt.Printf("  Config:      %s\n", cfgPath)
	fmt.Printf("  Log:         %s\n", logPath)
	fmt.Println()

	if !pairedOK {
		fmt.Println("⚠️  Todavía no hay emparejamiento. Antes de arrancar el servicio:")
		fmt.Println("      novasoft-agent.exe pair")
		fmt.Println()
	}

	fmt.Println("Para arrancarlo ahora:")
	fmt.Println("  novasoft-agent.exe service start")
	return nil
}

// cmdServiceStatus imprime el estado del servicio para soporte.
// No requiere permisos de administrador — es lo primero que se pide por
// teléfono cuando un restaurante reporta que dejó de imprimir.
func cmdServiceStatus() error {
	info, err := winsvc.Query()
	if err != nil {
		return err
	}

	logPath, _ := logging.Path()
	cfgPath, _ := config.Path()

	fmt.Printf("Version:       %s\n", Version)
	fmt.Printf("Servicio:      %s\n", winsvc.Name)
	fmt.Println()

	if !info.Installed {
		fmt.Println("Estado:        NO INSTALADO")
		fmt.Println()
		fmt.Println("Para instalarlo, en PowerShell como administrador:")
		fmt.Println("  novasoft-agent.exe service install")
		fmt.Println("  novasoft-agent.exe service start")
	} else {
		fmt.Printf("Estado:        %s\n", winsvc.StateName(info.State))
		if info.ProcessID != 0 {
			fmt.Printf("PID:           %d\n", info.ProcessID)
		}
		fmt.Printf("Arranque:      %s\n", winsvc.StartTypeName(info.StartType))
		fmt.Printf("Cuenta:        %s\n", info.Account)
		fmt.Printf("Ejecutable:    %s\n", info.BinaryPath)
		if len(info.Recovery) == 0 {
			fmt.Println("Recuperación:  (ninguna configurada) ⚠️")
		} else {
			fmt.Println("Recuperación:")
			for i, a := range info.Recovery {
				fmt.Printf("  fallo %d:     %s\n", i+1, winsvc.RecoveryActionName(a))
			}
			fmt.Println("  siguientes:  como el último")
		}
	}

	fmt.Println()
	fmt.Printf("Config:        %s\n", cfgPath)
	if _, err := config.Load(); err != nil {
		if errors.Is(err, config.ErrNotConfigured) {
			fmt.Println("               (no emparejado — ejecuta 'pair')")
		} else {
			fmt.Printf("               error: %v\n", err)
		}
	} else {
		fmt.Println("               (emparejado ✓)")
	}
	fmt.Printf("Log:           %s\n", logPath)
	if st, err := os.Stat(logPath); err == nil {
		fmt.Printf("               (%d KB, última escritura %s)\n",
			st.Size()/1024, st.ModTime().Format("2006-01-02 15:04:05"))
	} else {
		fmt.Println("               (todavía no existe)")
	}
	fmt.Println()
	fmt.Println("Eventos de arranque/parada/errores fatales: Visor de eventos")
	fmt.Printf("de Windows → Registros de Windows → Aplicación → origen %q.\n", winsvc.Name)
	return nil
}

// printServiceLine — línea "Servicio:" de `status`.
func printServiceLine() {
	if info, qErr := winsvc.Query(); qErr == nil {
		if info.Installed {
			fmt.Printf("Servicio:      instalado, %s\n", winsvc.StateName(info.State))
		} else {
			fmt.Println("Servicio:      no instalado (ver 'service install')")
		}
		fmt.Println()
	}
}

// printPostPairHint — siguiente paso tras emparejar.
func printPostPairHint() {
	fmt.Println("Ahora instala el servicio para que arranque solo:")
	fmt.Println("  novasoft-agent.exe service install")
	fmt.Println("  novasoft-agent.exe service start")
}
