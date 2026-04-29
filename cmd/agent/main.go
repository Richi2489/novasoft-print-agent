// NovaSoft Print Agent — binary principal.
//
// Subcomandos (CLI interactivo):
//   pair      Empareja con un restaurante usando un código del wizard.
//   run       Modo standalone: corre el loop directo en la terminal.
//   status    Muestra config, impresoras detectadas, estado del servicio.
//   unpair    Borra la config local.
//   version   Imprime la versión del binary.
//
// Subcomandos de servicio Windows (requieren admin):
//   install   Registra novasoft-agent.exe como servicio Windows.
//   uninstall Desregistra el servicio (lo detiene si está corriendo).
//   start     Inicia el servicio ya instalado.
//   stop      Detiene el servicio.
//   restart   Stop + start.
//
// Cuando el binary es lanzado por el SCM (Service Control Manager),
// service.Interactive() devuelve false y el binary entra al loop del
// servicio sin parsear flags ni subcomandos.
//
// Transporte: desde v0.2.0 el agent usa SSE (HTTP/1.1 streaming) en
// lugar de WebSocket. Motivo: Railway+Fastly strippean el header
// Upgrade: websocket antes de llegar al origin, así que la conexión WS
// nunca completaba. SSE funciona sin config especial por ser HTTP puro.
// Ver ADR-015 del backend para detalles.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/kardianos/service"

	"github.com/Richi2489/novasoft-print-agent/internal/config"
	"github.com/Richi2489/novasoft-print-agent/internal/logfile"
	"github.com/Richi2489/novasoft-print-agent/internal/pairing"
	"github.com/Richi2489/novasoft-print-agent/internal/printer"
	"github.com/Richi2489/novasoft-print-agent/internal/runner"
)

// Version se inyecta en build vía -ldflags "-X main.Version=vX.Y.Z".
// El default tiene "-dev" para distinguir un build manual sin ldflags.
var Version = "0.3.0-dev"

// DefaultBackendURL apunta al dominio custom de NovaSoft. Si Railway
// migra a otro proyecto/proveedor, el agent emparejado contra
// api.novasoft.mx sigue funcionando — el dominio es estable. Para
// preview deploys o troubleshooting, usar -backend=URL.
const DefaultBackendURL = "https://api.novasoft.mx"

func main() {
	// Propagar Version al runner para que el IPC StatusResponse la
	// reporte. Sin esto, el tray vería "0.3.0-dev" hardcoded del runner
	// aun cuando el binary tenga una versión inyectada por -ldflags.
	runner.AgentVersion = Version

	// Default backend para el flow de pair desde IPC (tray). Sin esto
	// runner.PairAndApply usa un fallback hardcoded — sincronizar con
	// la const de este archivo evita drift si cambiamos uno y olvidamos
	// el otro.
	runner.SetDefaultBackend(DefaultBackendURL)

	// Detectar modo de ejecución antes de cualquier otra cosa.
	// service.Interactive() retorna false cuando el binary fue lanzado
	// por el SCM de Windows (sin terminal, sin args). En ese caso
	// no parseamos flags y dejamos que el servicio tome control.
	if !service.Interactive() {
		// Modo servicio: log a archivo SOLO (no hay stderr útil), y
		// bloqueamos en s.Run() hasta que SCM pida Stop.
		if err := logfile.Configure(false); err != nil {
			// Sin logs el debugging es duro — al menos intentamos
			// mandar a stderr por si Windows captura algo.
			fmt.Fprintf(os.Stderr, "configure logging: %v\n", err)
		}
		if err := runAsService(); err != nil {
			log.Fatalf("servicio: %v", err)
		}
		return
	}

	// Modo interactivo: log a archivo + stderr.
	if err := logfile.Configure(true); err != nil {
		fmt.Fprintf(os.Stderr, "configure logging: %v\n", err)
	}

	// Flags globales — útiles antes del subcomando.
	backendFlag := flag.String("backend", DefaultBackendURL, "URL del backend (ej: https://api.novasoft.mx)")
	flag.Parse()

	args := flag.Args()
	if len(args) == 0 {
		usage()
		os.Exit(1)
	}

	cmd := args[0]
	switch cmd {
	case "pair":
		if err := cmdPair(*backendFlag); err != nil {
			fmt.Fprintf(os.Stderr, "✗ %s\n", err)
			os.Exit(1)
		}
	case "run":
		if err := cmdRun(); err != nil {
			fmt.Fprintf(os.Stderr, "✗ %s\n", err)
			os.Exit(1)
		}
	case "status":
		if err := cmdStatus(); err != nil {
			fmt.Fprintf(os.Stderr, "✗ %s\n", err)
			os.Exit(1)
		}
	case "unpair":
		if err := cmdUnpair(); err != nil {
			fmt.Fprintf(os.Stderr, "✗ %s\n", err)
			os.Exit(1)
		}
	case "version":
		fmt.Println(Version)
	case "install", "uninstall", "start", "stop", "restart":
		if err := controlService(cmd); err != nil {
			fmt.Fprintf(os.Stderr, "✗ %s\n", err)
			os.Exit(1)
		}
		fmt.Printf("✓ Servicio %s OK\n", cmd)
	default:
		fmt.Fprintf(os.Stderr, "comando desconocido: %s\n\n", cmd)
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `NovaSoft Print Agent `+Version+`

Uso:
  novasoft-agent.exe [-backend=URL] <comando>

Comandos de operación:
  pair       Empareja con un restaurante usando un código del wizard.
  run        Corre el agent directo en la terminal (no como servicio).
  status     Muestra la config actual y las impresoras detectadas.
  unpair     Borra la config local.
  version    Imprime la versión.

Comandos de servicio Windows (requieren PowerShell como Administrador):
  install    Registra el agent como servicio Windows (auto-start).
  uninstall  Desregistra el servicio.
  start      Inicia el servicio.
  stop       Detiene el servicio.
  restart    Stop + start.

Ejemplos:
  novasoft-agent.exe pair
  novasoft-agent.exe -backend=https://api.novasoft.mx pair
  novasoft-agent.exe run
  novasoft-agent.exe install
  novasoft-agent.exe start
`)
}

// cmdPair pide el código por stdin, llama al backend, guarda config.
func cmdPair(backendURL string) error {
	fmt.Println("=== NovaSoft Print Agent — Emparejamiento ===")
	fmt.Print("Ingresa el código que aparece en NovaSoft: ")

	reader := bufio.NewReader(os.Stdin)
	raw, err := reader.ReadString('\n')
	if err != nil {
		return fmt.Errorf("leyendo código: %w", err)
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fmt.Errorf("no ingresaste ningún código")
	}

	fmt.Println("→ Emparejando…")
	resp, err := pairing.Pair(backendURL, raw, Version)
	if err != nil {
		return err
	}

	cfg := &config.Config{
		AgentID:      resp.AgentID,
		AgentToken:   resp.AgentToken,
		WebsocketURL: resp.WebsocketURL,
		BackendURL:   backendURL,
	}
	if err := config.Save(cfg); err != nil {
		return fmt.Errorf("guardando config: %w", err)
	}

	path, _ := config.Path()
	fmt.Println()
	fmt.Println("✓ Emparejado correctamente")
	fmt.Printf("  Agent ID: %s\n", resp.AgentID)
	fmt.Printf("  Config:   %s\n", path)
	fmt.Println()
	fmt.Println("Próximos pasos:")
	fmt.Println("  Para correr como servicio (recomendado):")
	fmt.Println("    novasoft-agent.exe install")
	fmt.Println("    novasoft-agent.exe start")
	fmt.Println()
	fmt.Println("  O para correr en terminal (modo legacy):")
	fmt.Println("    novasoft-agent.exe run")
	return nil
}

// cmdRun corre el runner standalone con cancel por SIGINT/SIGTERM.
// Misma lógica que el modo servicio pero con terminal-attached y
// signal handling para Ctrl-C.
func cmdRun() error {
	ctx, cancel := signalContext()
	defer cancel()

	if err := runner.Run(ctx); err != nil {
		return err
	}

	fmt.Println("✓ Agent cerrado.")
	return nil
}

// cmdStatus imprime info diagnóstica para troubleshoot.
func cmdStatus() error {
	cfg, err := config.Load()
	cfgPath, _ := config.Path()
	logPath, _ := logfile.Path()

	fmt.Printf("Version:       %s\n", Version)
	fmt.Printf("Config path:   %s\n", cfgPath)
	fmt.Printf("Log path:      %s\n", logPath)
	fmt.Println()

	if err != nil {
		if errors.Is(err, config.ErrNotConfigured) {
			fmt.Println("Config:        (no hay — ejecuta 'pair' primero)")
		} else {
			fmt.Printf("Config:        error — %v\n", err)
		}
	} else {
		fmt.Printf("Agent ID:      %s\n", cfg.AgentID)
		fmt.Printf("Backend URL:   %s\n", cfg.BackendURL)
		fmt.Printf("Stream URL:    %s/printing/agents/stream\n",
			strings.TrimRight(cfg.BackendURL, "/"))
		if cfg.PrinterName != "" {
			fmt.Printf("Printer:       %s (fijada en config)\n", cfg.PrinterName)
		}
	}
	fmt.Println()

	printers, err := printer.ListSystem()
	if err != nil {
		return fmt.Errorf("listando impresoras: %w", err)
	}
	fmt.Printf("Impresoras detectadas (%d):\n", len(printers))
	for i, p := range printers {
		fmt.Printf("  %d. %s\n", i+1, p.Name)
	}
	if selected := printer.SelectThermalPrinter(printers); selected != nil {
		fmt.Printf("Preferida:     %s\n", selected.Name)
	}

	// Estado del servicio Windows si está instalado.
	fmt.Println()
	fmt.Print("Servicio Windows: ")
	s, _, sErr := newService()
	if sErr != nil {
		fmt.Printf("error inicializando wrapper — %v\n", sErr)
	} else {
		st, stErr := s.Status()
		switch {
		case errors.Is(stErr, service.ErrNotInstalled):
			fmt.Println("no instalado (corre 'install')")
		case stErr != nil:
			fmt.Printf("error consultando estado — %v\n", stErr)
		case st == service.StatusRunning:
			fmt.Println("corriendo ✓")
		case st == service.StatusStopped:
			fmt.Println("detenido (corre 'start')")
		default:
			fmt.Printf("estado desconocido (%d)\n", st)
		}
	}

	return nil
}

// cmdUnpair borra el config tras confirmación.
func cmdUnpair() error {
	fmt.Print("¿Seguro que quieres borrar la configuración? (y/n): ")
	reader := bufio.NewReader(os.Stdin)
	ans, _ := reader.ReadString('\n')
	ans = strings.TrimSpace(strings.ToLower(ans))
	if ans != "y" && ans != "yes" {
		fmt.Println("Cancelado.")
		return nil
	}
	if err := config.Delete(); err != nil {
		return err
	}
	fmt.Println("✓ Config borrada.")
	return nil
}

// signalContext construye un context que se cancela al recibir
// SIGINT (Ctrl-C) o SIGTERM. Permite graceful shutdown del run loop.
func signalContext() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigs
		fmt.Println()
		log.Println("→ señal recibida, cerrando…")
		cancel()
	}()
	return ctx, cancel
}
