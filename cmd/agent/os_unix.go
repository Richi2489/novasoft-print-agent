//go:build !windows

// CLI de macOS (y Linux): sin servicio de Windows. `run` corre en primer
// plano; en macOS lo deja corriendo solo un LaunchAgent de launchd
// (`service install`, o el script install-mac.sh que lo llama).
package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"

	"github.com/Richi2489/novasoft-print-agent/internal/config"
	"github.com/Richi2489/novasoft-print-agent/internal/launchd"
	"github.com/Richi2489/novasoft-print-agent/internal/logging"
)

func usage() {
	fmt.Fprintln(os.Stderr, `NovaSoft Print Agent `+Version+` (`+runtime.GOOS+`)

Uso:
  novasoft-agent [-backend=URL] <comando>

Comandos:
  pair         Empareja con un restaurante usando un código de NovaSoft
               (pair ABCD-1234-WXYZ, o sin código para teclearlo).
  printer list               Lista las impresoras (colas CUPS) del Mac.
  printer set <impresora>    Fija la impresora: nombre de cola CUPS
                             (ej. POS_80) o impresora de red por IP
                             (ej. 192.168.1.50:9100 o tcp://192.168.1.50).
  printer clear              Quita la impresora fija (elige sola).
  test-print [impresora]     Imprime un ticket de prueba local.
  run          Arranca el agente en esta Terminal (Ctrl-C para salir).
  service      Arranque automático con launchd (ver abajo, sólo macOS).
  status       Config, impresora y estado del arranque automático.
  unpair       Borra la config local.
  version      Imprime la versión.

Subcomandos de service (macOS, sin sudo):
  service install     Instala el LaunchAgent: arranca ya, al iniciar
                      sesión y se reinicia solo si se cae.
  service uninstall   Lo detiene y lo quita.
  service start|stop  Arranca / detiene.
  service status      Estado y rutas.

Instalación normal: ./install-mac.sh  (ver docs/INSTALAR-MAC.md)`)
}

func printPostPairHint() {
	fmt.Println("Siguiente paso:")
	fmt.Println("  novasoft-agent printer list           (ver impresoras)")
	fmt.Println("  novasoft-agent printer set <impresora>")
	fmt.Println("  novasoft-agent test-print")
	if runtime.GOOS == "darwin" {
		fmt.Println("  novasoft-agent service install        (arranque automático)")
	} else {
		fmt.Println("  novasoft-agent run")
	}
}

// cmdRun — bajo launchd sin consola (log sólo a archivo); a mano,
// interactivo como en Windows.
func cmdRun() error {
	if launchd.IsLaunchd() {
		return runUnderLaunchd()
	}
	return runInteractive()
}

func runUnderLaunchd() error {
	logPath, closer, err := logging.Setup(false)
	if err != nil {
		return fmt.Errorf("no pude abrir el log en %s: %w", logPath, err)
	}
	if closer != nil {
		defer closer.Close()
	}
	log.Printf("=== NovaSoft Print Agent %s — launchd ===", Version)
	// launchd manda SIGTERM al detener: signalContext lo atrapa.
	ctx, cancel := signalContext()
	defer cancel()
	err = runAgent(ctx)
	if err != nil {
		log.Printf("✗ %v", err)
	}
	return err
}

// exePath — ruta absoluta del binario actual (sin symlinks).
func exePath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	return exe, nil
}

func cmdService(args []string) error {
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("el arranque automático sólo está implementado para macOS; en Linux usa systemd con '%s run'", os.Args[0])
	}
	if len(args) == 0 {
		return fmt.Errorf("falta el subcomando — usa: service install|uninstall|start|stop|status")
	}
	switch args[0] {
	case "install":
		exe, err := exePath()
		if err != nil {
			return err
		}
		logDir, err := logging.Dir()
		if err != nil {
			return err
		}
		plist, err := launchd.Install(exe, filepath.Join(logDir, "launchd.log"))
		if err != nil {
			return err
		}
		logPath, _ := logging.Path()
		fmt.Println("✓ Arranque automático instalado y en marcha.")
		fmt.Printf("  LaunchAgent: %s\n", plist)
		fmt.Printf("  Ejecutable:  %s run\n", exe)
		fmt.Printf("  Log:         %s\n", logPath)
		if _, err := config.Load(); err != nil {
			fmt.Println()
			fmt.Println("⚠️  Todavía no hay emparejamiento: el agente reintentará cada 10 s")
			fmt.Println("    hasta que corras 'novasoft-agent pair'.")
		}
		return nil
	case "uninstall":
		if err := launchd.Uninstall(); err != nil {
			return err
		}
		dir, _ := config.DataDir()
		fmt.Println("✓ Arranque automático quitado (el agente ya no corre).")
		fmt.Printf("  La config y los logs siguen en %s\n", dir)
		return nil
	case "start":
		if err := launchd.Start(); err != nil {
			return err
		}
		fmt.Println("✓ Agente arrancado.")
		return nil
	case "stop":
		if err := launchd.Stop(); err != nil {
			return err
		}
		fmt.Println("✓ Agente detenido (vuelve al iniciar sesión o con 'service start').")
		return nil
	case "status":
		printServiceLine()
		logPath, _ := logging.Path()
		cfgPath, _ := config.Path()
		fmt.Printf("Config:        %s\n", cfgPath)
		fmt.Printf("Log:           %s\n", logPath)
		return nil
	default:
		return fmt.Errorf("subcomando de service desconocido: %s — usa: install|uninstall|start|stop|status", args[0])
	}
}

func printServiceLine() {
	if runtime.GOOS != "darwin" {
		return
	}
	st := launchd.Query()
	switch {
	case !st.Installed && !st.Loaded:
		fmt.Println("Servicio:      no instalado (ver 'service install')")
	case st.Running:
		fmt.Printf("Servicio:      corriendo (launchd, PID %d) ✓\n", st.PID)
	case st.Loaded:
		fmt.Println("Servicio:      instalado pero NO corriendo — revisa el log")
	default:
		fmt.Println("Servicio:      instalado pero detenido ('service start')")
	}
	fmt.Println()
}
